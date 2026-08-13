package sqlite

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/comisai/comis-dev-crew/internal/domain"
)

func TestTaskCleanupStore_AcceptsExactReconciliationEvidenceWithoutCandidateReport(t *testing.T) {
	store, task, sealed := deliveredCleanupFixture(t, filepath.Join(canonicalTempDir(t), "devcrew.db"))
	replaceCleanupCandidateReportWithReconciliation(t, store, task, sealed.Bundle().HeadRevision)
	var candidateReports int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM reports
		WHERE task_handle = ? AND kind = 'candidate_complete'`, task.Handle).Scan(&candidateReports); err != nil {
		t.Fatal(err)
	}
	if candidateReports != 0 {
		t.Fatalf("candidate reports = %d, want no synthetic worker report", candidateReports)
	}
	record, err := store.BeginTaskCleanup(context.Background(), cleanupTestMutation(task, "cleanup-reconciled-0001"))
	if err != nil || record.HeadRevision != sealed.Bundle().HeadRevision {
		t.Fatalf("BeginTaskCleanup(reconciled candidate) = %#v, %v", record, err)
	}
}

func replaceCleanupCandidateReportWithReconciliation(t *testing.T, store *Store, task domain.Task, headRevision string) {
	t.Helper()
	if _, err := store.db.Exec("DELETE FROM comis_report_outbox WHERE task_handle = ?", task.Handle); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec("DELETE FROM reports WHERE task_handle = ? AND kind = 'candidate_complete'", task.Handle); err != nil {
		t.Fatal(err)
	}
	operation := storeOperation("operation-reconcile-cleanup", task.StateVersion-2)
	operation.Command = "ReconcileTask"
	operation.ResultRef = task.Handle
	if err := store.RecordOperation(context.Background(), operation); err != nil {
		t.Fatal(err)
	}
	var terminalSessionID, terminalTransition, terminalObservedAt string
	if err := store.db.QueryRow(`SELECT terminal_session_id, latest_transition, updated_at
		FROM task_terminal_bindings WHERE task_handle = ?`, task.Handle).Scan(
		&terminalSessionID, &terminalTransition, &terminalObservedAt,
	); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec(`INSERT INTO task_candidate_reconciliations(
		operation_id, task_handle, action, preparation_operation_id,
		repository_id, worktree_path, branch, base_revision, head_revision,
		cleanliness, terminal_session_id, terminal_transition,
		terminal_observed_at, observed_at, started_state_version, completed_state_version)
		VALUES (?, ?, 'validate-clean-candidate', 'prepare-cleanup-0001', ?, ?, ?, ?, ?,
		'clean', ?, ?, ?, ?, ?, ?)`, operation.ID, task.Handle, task.RepositoryID,
		"/approved/worktrees/"+task.Handle, "devcrew/"+task.Handle+"-reconciled", task.BaseRevision,
		headRevision, terminalSessionID, terminalTransition, terminalObservedAt,
		formatTime(task.UpdatedAt.Add(-time.Minute)), operation.StateVersion-1, operation.StateVersion); err != nil {
		t.Fatal(err)
	}
}
