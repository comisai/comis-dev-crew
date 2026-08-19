package application

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

type stubAttestationStore struct {
	calls   []string
	result  MutationResult
	err     error
	records map[string]ScoutDecisionInventory
}

func (stub *stubAttestationStore) CommitScoutDecisionAttestation(
	_ context.Context,
	mutation ScoutDecisionAttestationMutation,
) (MutationResult, error) {
	stub.calls = append(
		stub.calls,
		mutation.OperationID+":"+mutation.TaskHandle+":"+string(mutation.Finding)+":"+
			strings.Join(mutation.OpenDecisionKeys, ","),
	)
	return stub.result, stub.err
}

func (stub *stubAttestationStore) ReadScoutDecisionInventory(
	_ context.Context,
	taskHandle string,
) (ScoutDecisionInventory, bool, error) {
	record, found := stub.records[taskHandle]
	return record, found, nil
}

func newScoutReviews(t *testing.T, stub *stubAttestationStore) *ScoutReviews {
	t.Helper()
	reviews, err := NewScoutReviews(ScoutReviewConfig{
		Store: stub, Clock: func() time.Time { return time.Unix(1_800_000_000, 0).UTC() },
	})
	if err != nil {
		t.Fatalf("NewScoutReviews() error = %v", err)
	}
	return reviews
}

// The attestation is a recorded judgement, and the judgement must be stated.
// An empty request is refused rather than read as "nothing was open": absence
// of decisions is exactly what must never be inferred from silence, since the
// failure this exists to prevent is a buried open question erased along with
// the worktree.
func TestAttestScoutDecisions_RefusesToInferAnEmptyInventoryFromSilence(t *testing.T) {
	stub := &stubAttestationStore{}
	reviews := newScoutReviews(t, stub)

	for label, command := range map[string]AttestScoutDecisionsCommand{
		"no finding": {OperationID: "operation-attest-0001", TaskHandle: "task-0001"},
		"unknown finding": {
			OperationID: "operation-attest-0001", TaskHandle: "task-0001",
			Finding: ScoutAttestationFinding("probably-fine"),
		},
		"none open but keys listed": {
			OperationID: "operation-attest-0001", TaskHandle: "task-0001",
			Finding: ScoutAttestationNoOpenDecisions, OpenDecisionKeys: []string{"schema-choice"},
		},
		"open but no keys listed": {
			OperationID: "operation-attest-0001", TaskHandle: "task-0001",
			Finding: ScoutAttestationOpenDecisions,
		},
		"forged key": {
			OperationID: "operation-attest-0001", TaskHandle: "task-0001",
			Finding: ScoutAttestationOpenDecisions, OpenDecisionKeys: []string{"../../etc"},
		},
		"duplicate key": {
			OperationID: "operation-attest-0001", TaskHandle: "task-0001",
			Finding:          ScoutAttestationOpenDecisions,
			OpenDecisionKeys: []string{"schema-choice", "schema-choice"},
		},
		"no operation": {TaskHandle: "task-0001", Finding: ScoutAttestationNoOpenDecisions},
		"forged task": {
			OperationID: "operation-attest-0001", TaskHandle: "../../etc",
			Finding: ScoutAttestationNoOpenDecisions,
		},
	} {
		if _, err := reviews.AttestScoutDecisions(context.Background(), command); err == nil {
			t.Errorf("AttestScoutDecisions(%s) error = nil", label)
		}
	}
	if len(stub.calls) != 0 {
		t.Fatalf("a refused attestation reached the store: %v", stub.calls)
	}
}

// Both findings are recordable, and each reaches the store exactly as stated.
// A positive "nothing is open" is a real answer an operator can be held to, so
// it is stored as such rather than as an absent row.
func TestAttestScoutDecisions_RecordsBothFindingsExactly(t *testing.T) {
	for label, expected := range map[string]struct {
		command AttestScoutDecisionsCommand
		want    string
	}{
		"none open": {
			AttestScoutDecisionsCommand{
				OperationID: "operation-attest-0001", TaskHandle: "task-0001",
				Finding: ScoutAttestationNoOpenDecisions,
			},
			"operation-attest-0001:task-0001:no_open_decisions:",
		},
		"open decisions": {
			AttestScoutDecisionsCommand{
				OperationID:      "operation-attest-0001",
				TaskHandle:       "task-0001",
				Finding:          ScoutAttestationOpenDecisions,
				OpenDecisionKeys: []string{"schema-choice", "rollout-window"},
			},
			"operation-attest-0001:task-0001:open_decisions:schema-choice,rollout-window",
		},
	} {
		stub := &stubAttestationStore{}
		reviews := newScoutReviews(t, stub)
		if _, err := reviews.AttestScoutDecisions(context.Background(), expected.command); err != nil {
			t.Fatalf("AttestScoutDecisions(%s) error = %v", label, err)
		}
		if len(stub.calls) != 1 || stub.calls[0] != expected.want {
			t.Fatalf("%s store calls = %v, want %q", label, stub.calls, expected.want)
		}
	}
}

// An inventory listing open decisions does not itself resolve anything. It
// records what is still open so cleanup and promotion can refuse, which is the
// whole point: the attestation is evidence, not permission.
func TestScoutDecisionInventory_ReportsWhetherItClearsTheSurface(t *testing.T) {
	cleared := ScoutDecisionInventory{Finding: ScoutAttestationNoOpenDecisions}
	if !cleared.ClearsReview() {
		t.Error("a positive attestation must clear the reviewed surface")
	}
	outstanding := ScoutDecisionInventory{
		Finding: ScoutAttestationOpenDecisions, OpenDecisionKeys: []string{"schema-choice"},
	}
	if outstanding.ClearsReview() {
		t.Error("an inventory naming open decisions must not clear the reviewed surface")
	}
}

func TestNewScoutReviews_RequiresEverySeam(t *testing.T) {
	for label, config := range map[string]ScoutReviewConfig{
		"nothing":  {},
		"no store": {Clock: time.Now},
		"no clock": {Store: &stubAttestationStore{}},
	} {
		if _, err := NewScoutReviews(config); err == nil {
			t.Errorf("NewScoutReviews(%s) error = nil", label)
		}
	}
}

func TestAttestScoutDecisions_SurfacesAStoreFailureAndACanceledCaller(t *testing.T) {
	failure := errors.New("store unavailable")
	reviews := newScoutReviews(t, &stubAttestationStore{err: failure})
	if _, err := reviews.AttestScoutDecisions(context.Background(), AttestScoutDecisionsCommand{
		OperationID: "operation-attest-0001", TaskHandle: "task-0001",
		Finding: ScoutAttestationNoOpenDecisions,
	}); !errors.Is(err, failure) {
		t.Fatalf("AttestScoutDecisions() error = %v, want the store failure", err)
	}

	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := newScoutReviews(t, &stubAttestationStore{}).AttestScoutDecisions(canceled, AttestScoutDecisionsCommand{
		OperationID: "operation-attest-0001", TaskHandle: "task-0001",
		Finding: ScoutAttestationNoOpenDecisions,
	}); !errors.Is(err, context.Canceled) {
		t.Fatalf("AttestScoutDecisions(canceled) error = %v", err)
	}
}
