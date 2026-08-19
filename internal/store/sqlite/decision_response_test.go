package sqlite

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/comisai/comis-dev-crew/internal/application"
	"github.com/comisai/comis-dev-crew/internal/domain"
)

func respondToDecision(t *testing.T, store *Store, task domain.Task, key, answer, operationID string) application.MutationResult {
	t.Helper()
	result, err := store.CommitDecisionResponse(context.Background(), application.DecisionResponseMutation{
		OperationID: operationID, SubjectDigest: strings.Repeat("d", 64),
		TaskHandle: task.Handle, ExternalKey: key, Response: answer, At: fixedAttestationTime(),
	})
	if err != nil {
		t.Fatalf("CommitDecisionResponse() error = %v", err)
	}
	return result
}

// An answered question stops being asked without being closed. Those are two
// different facts: the human is no longer owed a prompt the moment their answer
// lands, but the question still blocks completion until the worker reports it
// resolved, because an answer nobody applied has changed nothing about the work.
func TestDecisionResponse_StopsAskingTheHumanWithoutClosingTheRecord(t *testing.T) {
	store, scout := attestationFixture(t, domain.ShapeScout)
	at := scout.UpdatedAt.Add(time.Minute)
	reportDecision(t, store, scout, "schema-choice", at)
	askTheHuman(t, store, at.Add(time.Second))
	attestScout(t, store, scout, "operation-attest-respond", application.ScoutAttestationOpenDecisions, []string{"schema-choice"})

	due, err := store.OpenDecisionsAwaitingHuman(context.Background())
	if err != nil || len(due) != 1 {
		t.Fatalf("decisions awaiting the human before the answer = %#v, %v", due, err)
	}

	respondToDecision(t, store, scout, "schema-choice", "use the versioned schema", "operation-respond-0001")

	settled, err := store.OpenDecisionsAwaitingHuman(context.Background())
	if err != nil {
		t.Fatalf("OpenDecisionsAwaitingHuman() error = %v", err)
	}
	if len(settled) != 0 {
		t.Errorf("the cadence still owes a repeat for an answered question: %#v", settled)
	}

	stillOpen, err := store.ListTaskDecisions(context.Background(), "")
	if err != nil {
		t.Fatalf("ListTaskDecisions() error = %v", err)
	}
	if len(stillOpen) != 1 {
		t.Fatalf("an answered question left the inventory before the worker applied it: %#v", stillOpen)
	}
	if err := proveAttestationGate(t, store, scout); !errors.Is(err, application.ErrCleanupOpenDecision) {
		t.Errorf("cleanup stopped waiting for a question the worker has not applied: %v", err)
	}

	answer, found, err := store.ReadDecisionResponse(context.Background(), scout.Handle, "schema-choice")
	if err != nil || !found {
		t.Fatalf("ReadDecisionResponse() = %#v, %v, %v", answer, found, err)
	}
	if answer.Response != "use the versioned schema" || answer.ExternalKey != "schema-choice" {
		t.Errorf("stored answer = %#v", answer)
	}
}

// Answering a question that does not exist, or one already settled, is refused
// rather than stored as an answer an operator could believe reached a worker.
func TestDecisionResponse_RefusesAKeyThatIsNotAnOpenQuestion(t *testing.T) {
	store, scout := attestationFixture(t, domain.ShapeScout)
	at := scout.UpdatedAt.Add(time.Minute)
	reportDecision(t, store, scout, "schema-choice", at)
	askTheHuman(t, store, at.Add(time.Second))
	attestScout(t, store, scout, "operation-attest-unknown", application.ScoutAttestationOpenDecisions, []string{"schema-choice"})

	_, err := store.CommitDecisionResponse(context.Background(), application.DecisionResponseMutation{
		OperationID: "operation-respond-unknown", SubjectDigest: strings.Repeat("d", 64),
		TaskHandle: scout.Handle, ExternalKey: "never-asked", Response: "any", At: fixedAttestationTime(),
	})
	if !errors.Is(err, application.ErrPrecondition) {
		t.Fatalf("CommitDecisionResponse(unknown key) error = %v, want a precondition refusal", err)
	}

	cancelDecision(t, store, scout, "schema-choice", "operation-cancel-before-answer")
	_, err = store.CommitDecisionResponse(context.Background(), application.DecisionResponseMutation{
		OperationID: "operation-respond-withdrawn", SubjectDigest: strings.Repeat("d", 64),
		TaskHandle: scout.Handle, ExternalKey: "schema-choice", Response: "late", At: fixedAttestationTime(),
	})
	if !errors.Is(err, application.ErrPrecondition) {
		t.Fatalf("CommitDecisionResponse(withdrawn key) error = %v, want a precondition refusal", err)
	}
}

// The worker asks for its answer by managed run, because that is the identity it
// holds; it never learns the task handle. Serving the answer from durable state
// is what lets the console answer a question while the channel that raised it is
// unavailable — the case the console exists for.
func TestDecisionResponse_IsReadableByTheManagedRunThatAsked(t *testing.T) {
	store, scout := attestationFixture(t, domain.ShapeScout)
	at := scout.UpdatedAt.Add(time.Minute)
	reportDecision(t, store, scout, "schema-choice", at)
	askTheHuman(t, store, at.Add(time.Second))
	attestScout(t, store, scout, "operation-attest-run", application.ScoutAttestationOpenDecisions, []string{"schema-choice"})

	if _, found, err := store.ReadDecisionResponseForManagedRun(
		context.Background(), scout.ManagedRunID, "schema-choice",
	); err != nil || found {
		t.Fatalf("an unanswered question reported an answer: found = %v, err = %v", found, err)
	}

	respondToDecision(t, store, scout, "schema-choice", "use the versioned schema", "operation-respond-run-0001")

	answer, found, err := store.ReadDecisionResponseForManagedRun(
		context.Background(), scout.ManagedRunID, "schema-choice")
	if err != nil || !found {
		t.Fatalf("ReadDecisionResponseForManagedRun() = %#v, %v, %v", answer, found, err)
	}
	if answer.Response != "use the versioned schema" {
		t.Errorf("answer served to the managed run = %#v", answer)
	}

	if _, found, err := store.ReadDecisionResponseForManagedRun(
		context.Background(), "managed-run-someone-else", "schema-choice",
	); err != nil || found {
		t.Fatalf("another run read this answer: found = %v, err = %v", found, err)
	}
}

// Every refusal path is exercised, because an answer accepted on a bad input is
// one an operator believes reached a worker when it never could.
func TestDecisionResponse_RefusesInvalidInputAndUnusableContexts(t *testing.T) {
	store, scout := attestationFixture(t, domain.ShapeScout)
	at := scout.UpdatedAt.Add(time.Minute)
	reportDecision(t, store, scout, "schema-choice", at)
	askTheHuman(t, store, at.Add(time.Second))
	attestScout(t, store, scout, "operation-attest-refusals", application.ScoutAttestationOpenDecisions, []string{"schema-choice"})

	base := application.DecisionResponseMutation{
		OperationID: "operation-respond-refusal", SubjectDigest: strings.Repeat("d", 64),
		TaskHandle: scout.Handle, ExternalKey: "schema-choice",
		Response: "use the versioned schema", At: fixedAttestationTime(),
	}
	for name, mutate := range map[string]func(*application.DecisionResponseMutation){
		"bad key":          func(m *application.DecisionResponseMutation) { m.ExternalKey = "not a key" },
		"empty answer":     func(m *application.DecisionResponseMutation) { m.Response = "" },
		"control sequence": func(m *application.DecisionResponseMutation) { m.Response = "clear \x1b[2J now" },
		"unknown task":     func(m *application.DecisionResponseMutation) { m.TaskHandle = "task-never-created" },
	} {
		t.Run(name, func(t *testing.T) {
			mutation := base
			mutate(&mutation)
			if _, err := store.CommitDecisionResponse(context.Background(), mutation); err == nil {
				t.Fatalf("CommitDecisionResponse(%s) error = nil", name)
			}
		})
	}

	// A second answer to the same question is refused: the first already stopped
	// the asking, and a silent overwrite would change what the worker reads
	// without any record that it changed.
	respondToDecision(t, store, scout, "schema-choice", "use the versioned schema", "operation-respond-first")
	if _, err := store.CommitDecisionResponse(context.Background(), application.DecisionResponseMutation{
		OperationID: "operation-respond-second", SubjectDigest: strings.Repeat("e", 64),
		TaskHandle: scout.Handle, ExternalKey: "schema-choice", Response: "changed my mind",
		At: fixedAttestationTime(),
	}); !errors.Is(err, application.ErrPrecondition) {
		t.Fatalf("CommitDecisionResponse(second answer) error = %v", err)
	}

	//lint:ignore SA1012 This boundary test exercises explicit nil-context rejection.
	if _, _, err := store.ReadDecisionResponse(nil, scout.Handle, "schema-choice"); err == nil {
		t.Error("ReadDecisionResponse(nil context) error = nil")
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err := store.ReadDecisionResponse(canceled, scout.Handle, "schema-choice"); err == nil {
		t.Error("ReadDecisionResponse(cancelled) error = nil")
	}
	//lint:ignore SA1012 This boundary test exercises explicit nil-context rejection.
	if _, _, err := store.ReadDecisionResponseForManagedRun(nil, scout.ManagedRunID, "schema-choice"); err == nil {
		t.Error("ReadDecisionResponseForManagedRun(nil context) error = nil")
	}
	if _, _, err := store.ReadDecisionResponseForManagedRun(canceled, scout.ManagedRunID, "schema-choice"); err == nil {
		t.Error("ReadDecisionResponseForManagedRun(cancelled) error = nil")
	}
	if _, _, err := store.ReadDecisionResponseForManagedRun(context.Background(), "", "schema-choice"); err == nil {
		t.Error("ReadDecisionResponseForManagedRun(no run) error = nil")
	}
	if _, found, err := store.ReadDecisionResponse(context.Background(), scout.Handle, "never-asked"); err != nil || found {
		t.Errorf("ReadDecisionResponse(unanswered) = %v, %v", found, err)
	}
}

// A stored row that cannot be decoded is refused rather than read as a zero
// time. A zero instant would present a real answer as though it had been given
// at the epoch, and both readers must agree on that rather than one of them
// quietly succeeding.
func TestDecisionResponse_RefusesAnUndecodableStoredRow(t *testing.T) {
	store, scout := attestationFixture(t, domain.ShapeScout)
	at := scout.UpdatedAt.Add(time.Minute)
	reportDecision(t, store, scout, "schema-choice", at)
	askTheHuman(t, store, at.Add(time.Second))
	attestScout(t, store, scout, "operation-attest-row", application.ScoutAttestationOpenDecisions, []string{"schema-choice"})
	respondToDecision(t, store, scout, "schema-choice", "use the versioned schema", "operation-respond-row")

	if _, err := store.db.ExecContext(context.Background(),
		`UPDATE task_decision_responses SET responded_at = ? WHERE task_handle = ? AND external_key = ?`,
		"not-a-time", scout.Handle, "schema-choice",
	); err != nil {
		t.Fatal(err)
	}

	if _, _, err := store.ReadDecisionResponse(context.Background(), scout.Handle, "schema-choice"); err == nil {
		t.Error("ReadDecisionResponse(undecodable row) error = nil")
	}
	if _, _, err := store.ReadDecisionResponseForManagedRun(
		context.Background(), scout.ManagedRunID, "schema-choice",
	); err == nil {
		t.Error("ReadDecisionResponseForManagedRun(undecodable row) error = nil")
	}
}
