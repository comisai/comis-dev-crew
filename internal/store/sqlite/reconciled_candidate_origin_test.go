package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/comisai/comis-dev-crew/internal/application"
	"github.com/comisai/comis-dev-crew/internal/domain"
)

func TestReadReconciledCandidateSnapshot_FailsClosedOnInvalidInputPostureAndStorage(t *testing.T) {
	ctx := context.Background()

	t.Run("nil store", func(t *testing.T) {
		var store *Store
		if _, _, err := store.ReadReconciledCandidateSnapshot(ctx, "task-origin-nil-store"); err == nil {
			t.Fatal("ReadReconciledCandidateSnapshot(nil store) error = nil")
		}
	})

	t.Run("invalid task handle", func(t *testing.T) {
		store, _ := openReconciledCandidateOriginFixture(t, "task-origin-invalid-handle")
		if _, _, err := store.ReadReconciledCandidateSnapshot(ctx, ""); err == nil {
			t.Fatal("ReadReconciledCandidateSnapshot(empty handle) error = nil")
		}
	})

	t.Run("absent task", func(t *testing.T) {
		store, _ := openReconciledCandidateOriginFixture(t, "task-origin-absent-owner")
		if _, _, err := store.ReadReconciledCandidateSnapshot(ctx, "task-origin-absent-subject"); err == nil {
			t.Fatal("ReadReconciledCandidateSnapshot(absent task) error = nil")
		}
	})

	t.Run("task is not validating", func(t *testing.T) {
		store := openReconciledCandidateOriginStore(t)
		task := storeTask("task-origin-prepared", 1)
		if err := store.CreateTask(ctx, task); err != nil {
			t.Fatalf("CreateTask() error = %v", err)
		}
		_, _, err := store.ReadReconciledCandidateSnapshot(ctx, task.Handle)
		if !isPreconditionFailure(err) {
			t.Fatalf("ReadReconciledCandidateSnapshot(prepared) error = %v, want precondition", err)
		}
	})

	t.Run("unreadable reconciliation storage", func(t *testing.T) {
		store, task := openReconciledCandidateOriginFixture(t, "task-origin-dropped-storage")
		if _, err := store.db.ExecContext(ctx, "DROP TABLE task_candidate_reconciliations"); err != nil {
			t.Fatalf("drop reconciliation table: %v", err)
		}
		if _, _, err := store.ReadReconciledCandidateSnapshot(ctx, task.Handle); err == nil {
			t.Fatal("ReadReconciledCandidateSnapshot(missing table) error = nil")
		}
	})

	t.Run("closed store", func(t *testing.T) {
		store, task := openReconciledCandidateOriginFixture(t, "task-origin-closed-store")
		if err := store.Close(); err != nil {
			t.Fatalf("Close() error = %v", err)
		}
		if _, _, err := store.ReadReconciledCandidateSnapshot(ctx, task.Handle); err == nil {
			t.Fatal("ReadReconciledCandidateSnapshot(closed) error = nil")
		}
	})
}

func TestReadReconciledCandidateSnapshot_ReturnsDurableOriginForReconciledCandidate(t *testing.T) {
	store, task := openReconciledCandidateOriginFixture(t, "task-origin-durable")
	snapshot, found, err := store.ReadReconciledCandidateSnapshot(context.Background(), task.Handle)
	if err != nil || !found {
		t.Fatalf("ReadReconciledCandidateSnapshot() = %#v, %t, %v", snapshot, found, err)
	}
	if snapshot.TaskHandle != task.Handle || snapshot.RepositoryID != task.RepositoryID ||
		snapshot.Cleanliness != application.WorkspaceClean || snapshot.Validate() != nil {
		t.Fatalf("reconciled snapshot = %#v, want clean durable origin for %q", snapshot, task.Handle)
	}
}

func TestReadReconciledCandidateOrigin_RefusesAmbiguousIncompleteAndInvalidRows(t *testing.T) {
	ctx := context.Background()

	t.Run("unreadable storage", func(t *testing.T) {
		store, task := openReconciledCandidateOriginFixture(t, "task-origin-query-failure")
		transaction := beginReconciledCandidateTx(t, store)
		if _, err := transaction.ExecContext(ctx, "DROP TABLE task_candidate_reconciliations"); err != nil {
			t.Fatalf("drop reconciliation table: %v", err)
		}
		if _, _, err := readReconciledCandidateOrigin(ctx, transaction, task); err == nil {
			t.Fatal("readReconciledCandidateOrigin(missing table) error = nil")
		}
	})

	t.Run("ambiguous durable origins", func(t *testing.T) {
		store, task := openReconciledCandidateOriginFixture(t, "task-origin-ambiguous")
		duplicate := storeOperation("reconcile-recovery-duplicate", 3)
		duplicate.Command = commandReconcileTask
		duplicate.ResultRef = task.Handle
		if err := store.RecordOperation(ctx, duplicate); err != nil {
			t.Fatalf("RecordOperation(duplicate) error = %v", err)
		}
		if _, err := store.db.ExecContext(ctx, `INSERT INTO task_candidate_reconciliations(
			operation_id, task_handle, action, preparation_operation_id, repository_id,
			worktree_path, branch, base_revision, head_revision, cleanliness,
			terminal_session_id, terminal_transition, terminal_observed_at, observed_at,
			started_state_version, completed_state_version)
			SELECT ?, task_handle, action, preparation_operation_id, repository_id,
			worktree_path, branch, base_revision, head_revision, cleanliness,
			terminal_session_id, terminal_transition, terminal_observed_at, observed_at,
			started_state_version, completed_state_version
			FROM task_candidate_reconciliations WHERE task_handle = ?`,
			duplicate.ID, task.Handle); err != nil {
			t.Fatalf("insert duplicate reconciliation: %v", err)
		}
		transaction := beginReconciledCandidateTx(t, store)
		_, _, err := readReconciledCandidateOrigin(ctx, transaction, task)
		if !isPreconditionFailure(err) {
			t.Fatalf("readReconciledCandidateOrigin(ambiguous) error = %v, want precondition", err)
		}
	})

	t.Run("incomplete durable origin", func(t *testing.T) {
		store, task := openReconciledCandidateOriginFixture(t, "task-origin-incomplete")
		if _, err := store.db.ExecContext(ctx,
			"UPDATE task_preparations SET state = 'closed' WHERE task_handle = ?", task.Handle); err != nil {
			t.Fatalf("close task preparation: %v", err)
		}
		transaction := beginReconciledCandidateTx(t, store)
		_, _, err := readReconciledCandidateOrigin(ctx, transaction, task)
		if !isPreconditionFailure(err) {
			t.Fatalf("readReconciledCandidateOrigin(incomplete) error = %v, want precondition", err)
		}
	})

	t.Run("invalid durable snapshot", func(t *testing.T) {
		store, task := openReconciledCandidateOriginFixture(t, "task-origin-invalid-snapshot")
		if _, err := store.db.ExecContext(ctx,
			"UPDATE task_candidate_reconciliations SET branch = '' WHERE task_handle = ?", task.Handle); err != nil {
			t.Fatalf("blank reconciliation branch: %v", err)
		}
		transaction := beginReconciledCandidateTx(t, store)
		if _, _, err := readReconciledCandidateOrigin(ctx, transaction, task); err == nil {
			t.Fatal("readReconciledCandidateOrigin(invalid snapshot) error = nil")
		}
	})

	t.Run("absent durable origin", func(t *testing.T) {
		store := openReconciledCandidateOriginStore(t)
		task := candidateEvidenceTask(t, "task-origin-none")
		if err := store.CreateTask(ctx, task); err != nil {
			t.Fatalf("CreateTask() error = %v", err)
		}
		transaction := beginReconciledCandidateTx(t, store)
		origin, found, err := readReconciledCandidateOrigin(ctx, transaction, task)
		if err != nil || found {
			t.Fatalf("readReconciledCandidateOrigin(absent) = %#v, %t, %v", origin, found, err)
		}
	})
}

func TestCandidateRecoveryHistoryExists_FailsClosedOnUnreadableHistory(t *testing.T) {
	ctx := context.Background()
	store, task := openReconciledCandidateOriginFixture(t, "task-origin-history")
	transaction := beginReconciledCandidateTx(t, store)
	found, err := candidateRecoveryHistoryExists(ctx, transaction, task.Handle)
	if err != nil || !found {
		t.Fatalf("candidateRecoveryHistoryExists(reconciled) = %t, %v, want true", found, err)
	}
	if _, err := transaction.ExecContext(ctx, "DROP TABLE comis_evidence_outbox"); err != nil {
		t.Fatalf("drop evidence outbox: %v", err)
	}
	if _, err := candidateRecoveryHistoryExists(ctx, transaction, task.Handle); err == nil {
		t.Fatal("candidateRecoveryHistoryExists(missing table) error = nil")
	}
}

func TestValidateReconciledCandidateEvidenceAuthority_FailsClosedOnUnreadableOrigin(t *testing.T) {
	ctx := context.Background()
	store, task := openReconciledCandidateOriginFixture(t, "task-origin-authority")
	evidence := candidateEvidence(t, task, strings.Repeat("b", 40))
	transaction := beginReconciledCandidateTx(t, store)
	if _, err := transaction.ExecContext(ctx, "DROP TABLE task_candidate_reconciliations"); err != nil {
		t.Fatalf("drop reconciliation table: %v", err)
	}
	if err := validateReconciledCandidateEvidenceAuthority(ctx, transaction, task, evidence); err == nil {
		t.Fatal("validateReconciledCandidateEvidenceAuthority(missing table) error = nil")
	}
}

func TestResumableReconciledCandidateDelivery_RefusesUnresumablePostures(t *testing.T) {
	ctx := context.Background()

	t.Run("task is not candidate complete", func(t *testing.T) {
		store, task := openReconciledCandidateOriginFixture(t, "task-restart-validating")
		transaction := beginReconciledCandidateTx(t, store)
		resumable, err := resumableReconciledCandidateDelivery(ctx, transaction, task, task.UpdatedAt)
		if err != nil || resumable {
			t.Fatalf("resumableReconciledCandidateDelivery(validating) = %t, %v, want false", resumable, err)
		}
	})

	t.Run("unreadable reports", func(t *testing.T) {
		store, task, at := openResumableReconciledCandidateFixture(t, "task-restart-reports-failure")
		transaction := beginReconciledCandidateTx(t, store)
		if _, err := transaction.ExecContext(ctx, "DROP TABLE reports"); err != nil {
			t.Fatalf("drop reports table: %v", err)
		}
		if _, err := resumableReconciledCandidateDelivery(ctx, transaction, task, at); err == nil {
			t.Fatal("resumableReconciledCandidateDelivery(missing reports) error = nil")
		}
	})

	t.Run("accepted candidate report", func(t *testing.T) {
		store, task, at := openResumableReconciledCandidateFixture(t, "task-restart-reported")
		insertCandidateCompleteReport(t, store, task)
		transaction := beginReconciledCandidateTx(t, store)
		resumable, err := resumableReconciledCandidateDelivery(ctx, transaction, task, at)
		if err != nil || resumable {
			t.Fatalf("resumableReconciledCandidateDelivery(reported) = %t, %v, want false", resumable, err)
		}
	})

	t.Run("absent candidate evidence", func(t *testing.T) {
		store, task, at := openResumableReconciledCandidateFixture(t, "task-restart-no-evidence")
		if _, err := store.db.ExecContext(ctx,
			"DELETE FROM candidate_evidence WHERE task_handle = ?", task.Handle); err != nil {
			t.Fatalf("delete candidate evidence: %v", err)
		}
		transaction := beginReconciledCandidateTx(t, store)
		resumable, err := resumableReconciledCandidateDelivery(ctx, transaction, task, at)
		if err != nil || resumable {
			t.Fatalf("resumableReconciledCandidateDelivery(no evidence) = %t, %v, want false", resumable, err)
		}
	})

	t.Run("evidence version differs from task", func(t *testing.T) {
		store, task, at := openResumableReconciledCandidateFixture(t, "task-restart-stale-evidence")
		if _, err := store.db.ExecContext(ctx,
			"UPDATE candidate_evidence SET state_version = state_version + 1 WHERE task_handle = ?",
			task.Handle); err != nil {
			t.Fatalf("advance stored evidence version: %v", err)
		}
		transaction := beginReconciledCandidateTx(t, store)
		resumable, err := resumableReconciledCandidateDelivery(ctx, transaction, task, at)
		if err != nil || resumable {
			t.Fatalf("resumableReconciledCandidateDelivery(stale) = %t, %v, want false", resumable, err)
		}
	})

	t.Run("corrupt stored evidence", func(t *testing.T) {
		store, task, at := openResumableReconciledCandidateFixture(t, "task-restart-corrupt-evidence")
		if _, err := store.db.ExecContext(ctx,
			"UPDATE candidate_evidence SET outcome = 'invented' WHERE task_handle = ?", task.Handle); err != nil {
			t.Fatalf("corrupt stored evidence outcome: %v", err)
		}
		transaction := beginReconciledCandidateTx(t, store)
		if _, err := resumableReconciledCandidateDelivery(ctx, transaction, task, at); err == nil {
			t.Fatal("resumableReconciledCandidateDelivery(corrupt evidence) error = nil")
		}
	})

	t.Run("unreadable publications", func(t *testing.T) {
		store, task, at := openResumableReconciledCandidateFixture(t, "task-restart-publication-failure")
		transaction := beginReconciledCandidateTx(t, store)
		if _, err := transaction.ExecContext(ctx, "DROP TABLE comis_evidence_outbox"); err != nil {
			t.Fatalf("drop evidence outbox: %v", err)
		}
		if _, err := resumableReconciledCandidateDelivery(ctx, transaction, task, at); err == nil {
			t.Fatal("resumableReconciledCandidateDelivery(missing outbox) error = nil")
		}
	})

	t.Run("undelivered reconciled publications resume", func(t *testing.T) {
		store, task, at := openResumableReconciledCandidateFixture(t, "task-restart-resumable")
		transaction := beginReconciledCandidateTx(t, store)
		resumable, err := resumableReconciledCandidateDelivery(ctx, transaction, task, at)
		if err != nil || !resumable {
			t.Fatalf("resumableReconciledCandidateDelivery(resumable) = %t, %v, want true", resumable, err)
		}
	})
}

func openReconciledCandidateOriginStore(t *testing.T) *Store {
	t.Helper()
	store, err := Open(context.Background(), filepath.Join(canonicalTempDir(t), "devcrew.db"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func openReconciledCandidateOriginFixture(t *testing.T, handle string) (*Store, domain.Task) {
	t.Helper()
	store := openReconciledCandidateOriginStore(t)
	task := candidateEvidenceTask(t, handle)
	if err := store.CreateTask(context.Background(), task); err != nil {
		t.Fatalf("CreateTask() error = %v", err)
	}
	insertEvidenceReconciliation(t, store, task, strings.Repeat("b", 40))
	stored, err := store.GetTask(context.Background(), handle)
	if err != nil {
		t.Fatalf("GetTask() error = %v", err)
	}
	return store, stored
}

func openResumableReconciledCandidateFixture(t *testing.T, handle string) (*Store, domain.Task, time.Time) {
	t.Helper()
	store := openReconciledCandidateOriginStore(t)
	task := candidateEvidenceTask(t, handle)
	if err := store.CreateTask(context.Background(), task); err != nil {
		t.Fatalf("CreateTask() error = %v", err)
	}
	sealed := candidateEvidence(t, task, strings.Repeat("b", 40))
	insertEvidenceReconciliation(t, store, task, sealed.Bundle().HeadRevision)
	publications := candidateEvidencePublications(t, task, sealed)
	updated, judgment, err := store.CommitCandidateEvidence(
		context.Background(), task.Handle, sealed, []string{"unit"}, []string{"ci/unit"},
		sealed.Bundle().ProducedAt, publications,
	)
	if err != nil || judgment.Outcome != domain.CandidateAccepted || updated.State != domain.TaskCandidateComplete {
		t.Fatalf("CommitCandidateEvidence() = %#v, %#v, %v", updated, judgment, err)
	}
	return store, updated, sealed.Bundle().ProducedAt.Add(time.Minute)
}

func insertCandidateCompleteReport(t *testing.T, store *Store, task domain.Task) {
	t.Helper()
	if _, err := store.db.ExecContext(context.Background(), `INSERT INTO reports(
		task_handle, local_report_id, subject_digest, schema_version, brief_revision,
		brief_revision_hash, kind, external_key, summary, details, worker_observed_at,
		state_version, accepted_at)
		VALUES (?, 'report-restart-candidate', ?, 1, ?, ?, 'candidate_complete', ?, 'summary', '', NULL, ?, ?)`,
		task.Handle, strings.Repeat("c", 64), task.BriefRevision, strings.Repeat("e", 64),
		"external-"+task.Handle, task.StateVersion, formatTime(task.UpdatedAt)); err != nil {
		t.Fatalf("insert candidate complete report: %v", err)
	}
}

func beginReconciledCandidateTx(t *testing.T, store *Store) *sql.Tx {
	t.Helper()
	transaction, err := store.db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatalf("BeginTx() error = %v", err)
	}
	t.Cleanup(func() { _ = transaction.Rollback() })
	return transaction
}

func isPreconditionFailure(err error) bool {
	return err != nil && errors.Is(err, application.ErrPrecondition)
}
