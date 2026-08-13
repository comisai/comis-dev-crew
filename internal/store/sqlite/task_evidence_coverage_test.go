package sqlite

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/comisai/comis-dev-crew/internal/application"
	"github.com/comisai/comis-dev-crew/internal/domain"
)

func TestTaskEvidenceStatusReducersCoverEveryClosedPosture(t *testing.T) {
	validation := map[domain.CandidateOutcome]application.ValidationEvidenceStatus{
		domain.CandidateAccepted:                   application.ValidationEvidenceAccepted,
		domain.CandidateRejected:                   application.ValidationEvidenceRejected,
		domain.CandidateUnknown:                    application.ValidationEvidenceUnknown,
		domain.CandidateOutcome("outside-catalog"): application.ValidationEvidenceUnknown,
	}
	for outcome, want := range validation {
		if got := validationDiagnosticStatus(outcome); got != want {
			t.Fatalf("validationDiagnosticStatus(%q) = %q, want %q", outcome, got, want)
		}
	}
	cleanup := map[application.TaskCleanupStage]application.CleanupEvidenceStatus{
		application.CleanupPrepared:                     application.CleanupEvidencePrepared,
		application.CleanupHostReleased:                 application.CleanupEvidenceHostReleased,
		application.CleanupRemovalAuthorized:            application.CleanupEvidenceRemovalAuthorized,
		application.CleanupCompleted:                    application.CleanupEvidenceCompleted,
		application.TaskCleanupStage("outside-catalog"): application.CleanupEvidenceUnknown,
	}
	for stage, want := range cleanup {
		if got := cleanupDiagnosticStatus(stage); got != want {
			t.Fatalf("cleanupDiagnosticStatus(%q) = %q, want %q", stage, got, want)
		}
	}
	for _, state := range []domain.TaskState{domain.TaskDelivered, domain.TaskCleanupHeld, domain.TaskCleaned} {
		if !taskDeliveryComplete(state) {
			t.Fatalf("taskDeliveryComplete(%q) = false", state)
		}
	}
	if taskDeliveryComplete(domain.TaskWorking) {
		t.Fatal("taskDeliveryComplete(working) = true")
	}
}

func TestTaskEvidenceReadFailsClosedAtInputAndStorageBoundaries(t *testing.T) {
	store, err := Open(context.Background(), filepath.Join(canonicalTempDir(t), "evidence-boundaries.db"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.ReadTaskEvidence(context.Background(), "../invalid"); err == nil {
		t.Fatal("ReadTaskEvidence(invalid handle) error = nil")
	}
	if _, err := store.ReadTaskObservation(nil, "task-evidence-boundary"); err == nil {
		t.Fatal("ReadTaskObservation(nil context) error = nil")
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err := store.TaskEvidenceSnapshot(cancelled); err == nil {
		t.Fatal("TaskEvidenceSnapshot(cancelled) error = nil")
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ReadTaskEvidence(context.Background(), "task-evidence-boundary"); err == nil {
		t.Fatal("ReadTaskEvidence(closed store) error = nil")
	}
}

func TestTaskEvidenceRejectsJudgmentThatDiffersFromReconciliationHead(t *testing.T) {
	store, task, _, now := openUnknownCandidateReconciliationFixture(t, "task-evidence-head-conflict")
	t.Cleanup(func() { _ = store.Close() })
	authority, err := store.ReadTaskReconciliationAuthority(context.Background(), task.Handle)
	if err != nil {
		t.Fatal(err)
	}
	mutation := candidateReconciliationMutation(authority, now, "operation-reconcile-head-conflict")
	result, err := store.CommitTaskCandidateReconciliation(context.Background(), mutation)
	if err != nil {
		t.Fatal(err)
	}
	sealed := candidateEvidence(t, result.Task, mutation.Snapshot.HeadRevision)
	if _, _, err := store.CommitCandidateEvidence(
		context.Background(), task.Handle, sealed, []string{"unit"}, []string{"ci/unit"},
		sealed.Bundle().ProducedAt, candidateEvidencePublications(t, result.Task, sealed),
	); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec(
		"UPDATE task_candidate_reconciliations SET head_revision = ? WHERE task_handle = ?",
		strings.Repeat("e", 40), task.Handle,
	); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ReadTaskEvidence(context.Background(), task.Handle); err == nil ||
		!strings.Contains(err.Error(), "judged head differs") {
		t.Fatalf("ReadTaskEvidence(conflicting origin) error = %v", err)
	}
}

func TestTaskEvidenceEmptyAndDecisionPosturesRemainExplicit(t *testing.T) {
	store, err := Open(context.Background(), filepath.Join(canonicalTempDir(t), "evidence-postures.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	task := storeTask("task-evidence-postures", 1)
	if err := store.CreateTask(context.Background(), task); err != nil {
		t.Fatal(err)
	}
	evidence, err := store.ReadTaskEvidence(context.Background(), task.Handle)
	if err != nil {
		t.Fatal(err)
	}
	if evidence.Candidate.Status != application.CandidateEvidenceNone ||
		evidence.Activity.Status != application.ActivityEvidenceNone ||
		evidence.Decision.Status != application.DecisionEvidenceNone ||
		evidence.Validation.Status != application.ValidationEvidenceNotStarted ||
		evidence.Delivery.Status != application.DeliveryEvidenceNotStarted ||
		evidence.Cleanup.Status != application.CleanupEvidenceNotStarted {
		t.Fatalf("empty evidence = %#v", evidence)
	}
	insertReport := func(id, kind, key string, stateVersion int64) {
		t.Helper()
		if _, err := store.db.Exec(`INSERT INTO reports(
			task_handle, local_report_id, subject_digest, schema_version, brief_revision,
			brief_revision_hash, kind, external_key, summary, details, state_version, accepted_at)
			VALUES (?, ?, ?, 1, ?, ?, ?, ?, 'bounded posture', '', ?, ?)`,
			task.Handle, id, strings.Repeat("f", 64), task.BriefRevision, task.BriefRevisionHash,
			kind, key, stateVersion, formatTime(task.UpdatedAt.Add(time.Duration(stateVersion)*time.Second)),
		); err != nil {
			t.Fatalf("insert %s report: %v", kind, err)
		}
	}
	insertReport("decision-evidence-open", "decision", "decision-evidence", 2)
	evidence, err = store.ReadTaskEvidence(context.Background(), task.Handle)
	if err != nil || evidence.Decision.Status != application.DecisionEvidenceOpen ||
		evidence.Decision.DecisionReportID != "decision-evidence-open" {
		t.Fatalf("open decision evidence = %#v, %v", evidence.Decision, err)
	}
	insertReport("resolution-evidence-closed", "resolution", "decision-evidence", 3)
	evidence, err = store.ReadTaskEvidence(context.Background(), task.Handle)
	if err != nil || evidence.Decision.Status != application.DecisionEvidenceResolved ||
		evidence.Decision.DecisionReportID != "decision-evidence-open" ||
		evidence.Decision.ResolutionReportID != "resolution-evidence-closed" {
		t.Fatalf("resolved decision evidence = %#v, %v", evidence.Decision, err)
	}
}

func TestTaskEvidenceValidationAndCleanupUnknownStatesFailClosed(t *testing.T) {
	store, err := Open(context.Background(), filepath.Join(canonicalTempDir(t), "evidence-unknown.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	task := storeTask("task-evidence-unknown", 1)
	task.State = domain.TaskValidating
	task.ManagedRunID = "managed-run-evidence-unknown"
	task.WorkspaceLeaseID = "workspace-lease-evidence-unknown"
	if err := store.CreateTask(context.Background(), task); err != nil {
		t.Fatal(err)
	}
	evidence, err := store.ReadTaskEvidence(context.Background(), task.Handle)
	if err != nil || evidence.Validation.Status != application.ValidationEvidenceUnknown {
		t.Fatalf("validating without process evidence = %#v, %v", evidence.Validation, err)
	}
	if _, err := store.db.Exec(`INSERT INTO validation_processes(
		operation_id, task_handle, program_id, executable_label, pid,
		start_identity, process_group_identity, state, started_at, observed_at)
		VALUES ('validation-evidence-active', ?, 'go-test', 'go', 123,
		'start-123', 'group-123', 'running', ?, ?)`, task.Handle,
		formatTime(task.UpdatedAt), formatTime(task.UpdatedAt)); err != nil {
		t.Fatal(err)
	}
	evidence, err = store.ReadTaskEvidence(context.Background(), task.Handle)
	if err != nil || evidence.Validation.Status != application.ValidationEvidenceRunning ||
		evidence.Validation.ProcessOperationID != "validation-evidence-active" {
		t.Fatalf("active validation evidence = %#v, %v", evidence.Validation, err)
	}
	if _, err := store.db.Exec("UPDATE tasks SET state = 'cleanup_held' WHERE handle = ?", task.Handle); err != nil {
		t.Fatal(err)
	}
	evidence, err = store.ReadTaskEvidence(context.Background(), task.Handle)
	if err != nil || evidence.Cleanup.Status != application.CleanupEvidenceUnknown {
		t.Fatalf("cleanup hold without durable record = %#v, %v", evidence.Cleanup, err)
	}
}
