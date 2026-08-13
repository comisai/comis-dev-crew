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
	var absentStore *Store
	if _, _, err := absentStore.TaskEvidenceSnapshot(context.Background()); err == nil {
		t.Fatal("TaskEvidenceSnapshot(nil store) error = nil")
	}
	store, err := Open(context.Background(), filepath.Join(canonicalTempDir(t), "evidence-boundaries.db"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.ReadTaskObservation(context.Background(), "task-evidence-absent"); err == nil {
		t.Fatal("ReadTaskObservation(absent task) error = nil")
	}
	if _, err := store.ReadTaskEvidence(context.Background(), "../invalid"); err == nil {
		t.Fatal("ReadTaskEvidence(invalid handle) error = nil")
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

func TestTaskEvidenceReadProjectsDurableCleanupOperation(t *testing.T) {
	store, task, _ := deliveredCleanupFixture(t, filepath.Join(canonicalTempDir(t), "cleanup-evidence.db"))
	mutation := cleanupTestMutation(task, "cleanup-evidence-operation")
	record, err := store.BeginTaskCleanup(context.Background(), mutation)
	if err != nil {
		t.Fatal(err)
	}
	evidence, err := store.ReadTaskEvidence(context.Background(), task.Handle)
	if err != nil {
		t.Fatal(err)
	}
	if evidence.Cleanup.Status != application.CleanupEvidencePrepared ||
		evidence.Cleanup.OperationID != record.OperationID {
		t.Fatalf("cleanup evidence = %#v, want prepared operation %q", evidence.Cleanup, record.OperationID)
	}
}

func TestTaskEvidenceReadRejectsMissingDurableEvidenceTables(t *testing.T) {
	tests := []struct {
		name      string
		table     string
		errorText string
	}{
		{name: "candidate evidence", table: "candidate_evidence", errorText: "read task candidate evidence"},
		{name: "candidate reconciliation", table: "task_candidate_reconciliations", errorText: "read task reconciliation evidence"},
		{name: "authenticated reports", table: "reports", errorText: "read task report evidence"},
		{name: "validation processes", table: "validation_processes", errorText: "read task validation evidence"},
		{name: "delivery outbox", table: "comis_evidence_outbox", errorText: "read task delivery evidence"},
		{name: "cleanup holds", table: "task_cleanup_holds", errorText: "read task cleanup holds"},
		{name: "cleanup operations", table: "task_cleanup_operations", errorText: "read task cleanup evidence"},
		{name: "preparation operations", table: "operations", errorText: "read task preparation evidence"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			store, err := Open(context.Background(), filepath.Join(canonicalTempDir(t), "missing-evidence.db"))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = store.Close() })
			task := storeTask("task-missing-evidence", 1)
			if err := store.CreateTask(context.Background(), task); err != nil {
				t.Fatal(err)
			}
			if _, err := store.db.Exec("DROP TABLE " + test.table); err != nil {
				t.Fatalf("drop bounded fixture table: %v", err)
			}
			if _, err := store.ReadTaskEvidence(context.Background(), task.Handle); err == nil ||
				!strings.Contains(err.Error(), test.errorText) {
				t.Fatalf("ReadTaskEvidence(missing %s) error = %v", test.table, err)
			}
			if test.table == "candidate_evidence" {
				if _, _, err := store.TaskEvidenceSnapshot(context.Background()); err == nil ||
					!strings.Contains(err.Error(), test.errorText) {
					t.Fatalf("TaskEvidenceSnapshot(missing %s) error = %v", test.table, err)
				}
			}
		})
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
	reconciled, err := store.ReadTaskEvidence(context.Background(), task.Handle)
	if err != nil || reconciled.Candidate.Status != application.CandidateEvidenceReconciled ||
		reconciled.Candidate.ReconciliationOperationID != mutation.OperationID {
		t.Fatalf("reconciled candidate origin = %#v, %v", reconciled.Candidate, err)
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

func TestTaskEvidenceRejectsCandidateIdentityThatDiffersFromTask(t *testing.T) {
	store, err := Open(context.Background(), filepath.Join(canonicalTempDir(t), "candidate-identity.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	task := candidateEvidenceTask(t, "task-candidate-identity")
	if err := store.CreateTask(context.Background(), task); err != nil {
		t.Fatal(err)
	}
	sealed := candidateEvidence(t, task, strings.Repeat("d", 40))
	if _, _, err := store.CommitCandidateEvidence(
		context.Background(), task.Handle, sealed, []string{"unit"}, []string{"ci/unit"},
		sealed.Bundle().ProducedAt, candidateEvidencePublications(t, task, sealed),
	); err != nil {
		t.Fatal(err)
	}
	different := task
	different.RepositoryID = "different-repository"
	different, err = different.PinBriefRevision()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec(
		"UPDATE tasks SET repository_id = ?, brief_revision_hash = ? WHERE handle = ?",
		different.RepositoryID, different.BriefRevisionHash, task.Handle,
	); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ReadTaskEvidence(context.Background(), task.Handle); err == nil ||
		!strings.Contains(err.Error(), "candidate identity differs") {
		t.Fatalf("ReadTaskEvidence(different candidate identity) error = %v", err)
	}
}

func TestTaskEvidenceReadRejectsCorruptDurableEvidence(t *testing.T) {
	t.Run("candidate payload", func(t *testing.T) {
		store, err := Open(context.Background(), filepath.Join(canonicalTempDir(t), "corrupt-candidate.db"))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = store.Close() })
		task := candidateEvidenceTask(t, "task-corrupt-candidate")
		if err := store.CreateTask(context.Background(), task); err != nil {
			t.Fatal(err)
		}
		sealed := candidateEvidence(t, task, strings.Repeat("c", 40))
		if _, _, err := store.CommitCandidateEvidence(
			context.Background(), task.Handle, sealed, []string{"unit"}, []string{"ci/unit"},
			sealed.Bundle().ProducedAt, candidateEvidencePublications(t, task, sealed),
		); err != nil {
			t.Fatal(err)
		}
		if _, err := store.db.Exec("UPDATE candidate_evidence SET canonical = '{}' WHERE task_handle = ?", task.Handle); err != nil {
			t.Fatal(err)
		}
		if _, err := store.ReadTaskEvidence(context.Background(), task.Handle); err == nil ||
			!strings.Contains(err.Error(), "stored candidate evidence") {
			t.Fatalf("ReadTaskEvidence(corrupt candidate) error = %v", err)
		}
	})

	t.Run("report timestamp", func(t *testing.T) {
		store, err := Open(context.Background(), filepath.Join(canonicalTempDir(t), "corrupt-report.db"))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = store.Close() })
		task := storeTask("task-corrupt-report", 1)
		if err := store.CreateTask(context.Background(), task); err != nil {
			t.Fatal(err)
		}
		if _, err := store.db.Exec(`INSERT INTO reports(
			task_handle, local_report_id, subject_digest, schema_version, brief_revision,
			brief_revision_hash, kind, external_key, summary, details, state_version, accepted_at)
			VALUES (?, 'report-corrupt-time', ?, 1, ?, ?, 'progress', '', 'bounded posture', '', 2, 'invalid-time')`,
			task.Handle, strings.Repeat("f", 64), task.BriefRevision, task.BriefRevisionHash,
		); err != nil {
			t.Fatal(err)
		}
		if _, err := store.ReadTaskEvidence(context.Background(), task.Handle); err == nil ||
			!strings.Contains(err.Error(), "accepted time is invalid") {
			t.Fatalf("ReadTaskEvidence(corrupt report time) error = %v", err)
		}
	})
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
