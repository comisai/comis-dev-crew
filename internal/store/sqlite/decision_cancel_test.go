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

func cancelDecision(t *testing.T, store *Store, task domain.Task, key, operationID string) application.MutationResult {
	t.Helper()
	result, err := store.CommitDecisionCancellation(context.Background(), application.DecisionCancellationMutation{
		OperationID: operationID, SubjectDigest: strings.Repeat("c", 64),
		TaskHandle: task.Handle, ExternalKey: key, At: fixedAttestationTime(),
	})
	if err != nil {
		t.Fatalf("CommitDecisionCancellation() error = %v", err)
	}
	return result
}

// A cancelled question is closed everywhere at once. The gates that consult
// open-ness — cleanup safety, the re-surfacing cadence, the operator inventory,
// candidate reconciliation and the evidence view — must agree, because a
// question the human withdrew that still blocks cleanup, or still comes back on
// a cadence, is a question nobody can ever close.
func TestDecisionCancellation_ClosesTheQuestionForEveryGate(t *testing.T) {
	store, scout := attestationFixture(t, domain.ShapeScout)
	at := scout.UpdatedAt.Add(time.Minute)
	reportDecision(t, store, scout, "schema-choice", at)
	askTheHuman(t, store, at.Add(time.Second))

	if err := proveAttestationGate(t, store, scout); !errors.Is(err, application.ErrCleanupUnattestedScout) {
		// The scout gate fires first; attest so the decision gate is reachable.
		attestScout(t, store, scout, "operation-attest-cancel", application.ScoutAttestationOpenDecisions, []string{"schema-choice"})
	}

	open, err := store.ListTaskDecisions(context.Background(), "")
	if err != nil || len(open) != 1 {
		t.Fatalf("open decisions before cancellation = %#v, %v", open, err)
	}
	due, err := store.OpenDecisionsAwaitingHuman(context.Background())
	if err != nil || len(due) != 1 {
		t.Fatalf("due decisions before cancellation = %#v, %v", due, err)
	}

	cancelDecision(t, store, scout, "schema-choice", "operation-cancel-0001")

	closed, err := store.ListTaskDecisions(context.Background(), "")
	if err != nil {
		t.Fatalf("ListTaskDecisions() error = %v", err)
	}
	if len(closed) != 0 {
		t.Errorf("the inventory still lists a cancelled question: %#v", closed)
	}
	settled, err := store.OpenDecisionsAwaitingHuman(context.Background())
	if err != nil {
		t.Fatalf("OpenDecisionsAwaitingHuman() error = %v", err)
	}
	if len(settled) != 0 {
		t.Errorf("the cadence still owes a cancelled question a repeat: %#v", settled)
	}
	if err := proveAttestationGate(t, store, scout); errors.Is(err, application.ErrCleanupOpenDecision) {
		t.Error("cleanup is still blocked by a cancelled question")
	}
	evidence, err := store.ReadTaskEvidence(context.Background(), scout.Handle)
	if err != nil {
		t.Fatalf("ReadTaskEvidence() error = %v", err)
	}
	if evidence.Decision.Status == application.DecisionEvidenceOpen {
		t.Errorf("the evidence view still calls a cancelled question open: %#v", evidence.Decision)
	}
}

// Cancelling is idempotent by operation identity, and reusing that identity for
// a different question is refused rather than silently cancelling both.
func TestDecisionCancellation_ReplaysByIdentityAndRefusesReuse(t *testing.T) {
	store, scout := attestationFixture(t, domain.ShapeScout)
	at := scout.UpdatedAt.Add(time.Minute)
	reportDecision(t, store, scout, "schema-choice", at)

	first := cancelDecision(t, store, scout, "schema-choice", "operation-cancel-0001")
	replay := cancelDecision(t, store, scout, "schema-choice", "operation-cancel-0001")
	if replay.Task.StateVersion != first.Task.StateVersion {
		t.Errorf("replay state version = %d, want %d", replay.Task.StateVersion, first.Task.StateVersion)
	}

	if _, err := store.CommitDecisionCancellation(context.Background(), application.DecisionCancellationMutation{
		OperationID: "operation-cancel-0001", SubjectDigest: strings.Repeat("d", 64),
		TaskHandle: scout.Handle, ExternalKey: "schema-choice", At: fixedAttestationTime(),
	}); !errors.Is(err, application.ErrConflict) {
		t.Errorf("altered replay error = %v, want ErrConflict", err)
	}
}

// Only a question that exists and is still open can be withdrawn: cancelling an
// unknown key, or one a worker already resolved, is refused rather than recorded
// as though it had an effect.
func TestDecisionCancellation_RefusesWhatItCannotWithdraw(t *testing.T) {
	store, scout := attestationFixture(t, domain.ShapeScout)
	at := scout.UpdatedAt.Add(time.Minute)
	reportDecision(t, store, scout, "schema-choice", at)

	if _, err := store.CommitDecisionCancellation(context.Background(), application.DecisionCancellationMutation{
		OperationID: "operation-cancel-0002", SubjectDigest: strings.Repeat("c", 64),
		TaskHandle: scout.Handle, ExternalKey: "never-asked", At: fixedAttestationTime(),
	}); err == nil {
		t.Error("cancelling an unknown question error = nil")
	}

	resolution := sqliteWorkerReport(scout, "report-resolution-0002", domain.ReportResolution)
	resolution.ExternalKey = "schema-choice"
	if _, err := store.CommitReport(context.Background(), directReportMutation(scout, resolution, at.Add(time.Hour))); err != nil {
		t.Fatalf("CommitReport(resolution) error = %v", err)
	}
	if _, err := store.CommitDecisionCancellation(context.Background(), application.DecisionCancellationMutation{
		OperationID: "operation-cancel-0003", SubjectDigest: strings.Repeat("c", 64),
		TaskHandle: scout.Handle, ExternalKey: "schema-choice", At: fixedAttestationTime(),
	}); err == nil {
		t.Error("cancelling an already-resolved question error = nil")
	}
}
