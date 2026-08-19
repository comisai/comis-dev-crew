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
