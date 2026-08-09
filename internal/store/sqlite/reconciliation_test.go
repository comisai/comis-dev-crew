package sqlite

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/comisai/comis-dev-crew/internal/application"
	"github.com/comisai/comis-dev-crew/internal/domain"
)

func TestStartupReconciliationMarksOnlyAmbiguousTasksAndIncompleteOperationsUnknown(t *testing.T) {
	databasePath := filepath.Join(canonicalTempDir(t), "devcrew.db")
	store, err := Open(context.Background(), databasePath)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	states := []domain.TaskState{
		domain.TaskPrepared, domain.TaskReady, domain.TaskLaunching, domain.TaskWorking,
		domain.TaskAwaitingDecision, domain.TaskReconciling, domain.TaskUnknown,
		domain.TaskDelivered, domain.TaskFailed, domain.TaskCancelled, domain.TaskCleaned,
	}
	for index, state := range states {
		task := reconciliationTask(index+1, state)
		if err := store.CreateTask(context.Background(), task); err != nil {
			t.Fatalf("CreateTask(%q) error = %v", state, err)
		}
	}
	accepted := reconciliationOperation("op-accepted", domain.OperationAccepted, 20)
	completed := reconciliationOperation("op-completed", domain.OperationCompleted, 21)
	unknown := reconciliationOperation("op-unknown", domain.OperationUnknown, 22)
	for _, operation := range []domain.OperationRecord{accepted, completed, unknown} {
		if err := store.RecordOperation(context.Background(), operation); err != nil {
			t.Fatalf("RecordOperation(%q) error = %v", operation.Status, err)
		}
	}
	reconcileAt := time.Date(2026, time.August, 9, 16, 45, 0, 0, time.UTC)
	reconciler, err := application.NewStartupReconciler(application.StartupReconcilerConfig{
		Store: store, Clock: func() time.Time { return reconcileAt },
	})
	if err != nil {
		t.Fatalf("NewStartupReconciler() error = %v", err)
	}
	result, err := reconciler.Reconcile(context.Background())
	if err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}
	if result.TasksMarkedUnknown != 4 || result.OperationsMarkedUnknown != 1 || result.StateVersion != 27 {
		t.Fatalf("reconciliation result = %#v, want 4 tasks, 1 operation, version 27", result)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	reopened, err := Open(context.Background(), databasePath)
	if err != nil {
		t.Fatalf("Open(restart) error = %v", err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	tasks, err := reopened.ListTasks(context.Background())
	if err != nil {
		t.Fatalf("ListTasks() error = %v", err)
	}
	for index, task := range tasks {
		original := states[index]
		want := original
		if original == domain.TaskLaunching || original == domain.TaskWorking ||
			original == domain.TaskAwaitingDecision || original == domain.TaskReconciling {
			want = domain.TaskUnknown
		}
		if task.State != want {
			t.Fatalf("task %q state = %q, want %q", task.Handle, task.State, want)
		}
	}
	gotAccepted, err := reopened.GetOperation(context.Background(), accepted.ID)
	if err != nil || gotAccepted.Status != domain.OperationUnknown || gotAccepted.StateVersion != 27 {
		t.Fatalf("accepted operation after reconcile = %#v, %v, want unknown version 27", gotAccepted, err)
	}
	gotCompleted, _ := reopened.GetOperation(context.Background(), completed.ID)
	gotUnknown, _ := reopened.GetOperation(context.Background(), unknown.ID)
	if gotCompleted != completed || gotUnknown != unknown {
		t.Fatalf("stable operations changed: completed=%#v unknown=%#v", gotCompleted, gotUnknown)
	}

	second, err := application.NewStartupReconciler(application.StartupReconcilerConfig{
		Store: reopened, Clock: func() time.Time { return reconcileAt.Add(time.Hour) },
	})
	if err != nil {
		t.Fatalf("NewStartupReconciler(second) error = %v", err)
	}
	secondResult, err := second.Reconcile(context.Background())
	if err != nil || secondResult.TasksMarkedUnknown != 0 || secondResult.OperationsMarkedUnknown != 0 || secondResult.StateVersion != 27 {
		t.Fatalf("second reconciliation = %#v, %v, want idempotent version 27", secondResult, err)
	}
}

func TestStartupReconciliation_FailsClosedOnInvalidTimeEvidenceAndStorage(t *testing.T) {
	t.Run("invalid time", func(t *testing.T) {
		store, err := Open(context.Background(), filepath.Join(canonicalTempDir(t), "devcrew.db"))
		if err != nil {
			t.Fatalf("Open() error = %v", err)
		}
		t.Cleanup(func() { _ = store.Close() })
		if _, err := store.ReconcileStartup(context.Background(), time.Now()); err == nil {
			t.Fatal("ReconcileStartup(non-UTC) error = nil")
		}
	})

	t.Run("backward task time", func(t *testing.T) {
		store, err := Open(context.Background(), filepath.Join(canonicalTempDir(t), "devcrew.db"))
		if err != nil {
			t.Fatalf("Open() error = %v", err)
		}
		t.Cleanup(func() { _ = store.Close() })
		task := reconciliationTask(1, domain.TaskWorking)
		if err := store.CreateTask(context.Background(), task); err != nil {
			t.Fatalf("CreateTask() error = %v", err)
		}
		if _, err := store.ReconcileStartup(context.Background(), task.UpdatedAt.Add(-time.Second)); !errors.Is(err, domain.ErrInvalidTransition) {
			t.Fatalf("ReconcileStartup(backward) error = %v, want ErrInvalidTransition", err)
		}
	})

	t.Run("exhausted operation version", func(t *testing.T) {
		store, err := Open(context.Background(), filepath.Join(canonicalTempDir(t), "devcrew.db"))
		if err != nil {
			t.Fatalf("Open() error = %v", err)
		}
		t.Cleanup(func() { _ = store.Close() })
		operation := reconciliationOperation("op-accepted-exhausted", domain.OperationAccepted, int64(^uint64(0)>>1))
		if err := store.RecordOperation(context.Background(), operation); err != nil {
			t.Fatalf("RecordOperation() error = %v", err)
		}
		if _, err := store.ReconcileStartup(context.Background(), operation.UpdatedAt.Add(time.Minute)); err == nil {
			t.Fatal("ReconcileStartup(exhausted) error = nil")
		}
	})

	t.Run("missing operation schema", func(t *testing.T) {
		store, err := Open(context.Background(), filepath.Join(canonicalTempDir(t), "devcrew.db"))
		if err != nil {
			t.Fatalf("Open() error = %v", err)
		}
		t.Cleanup(func() { _ = store.Close() })
		if _, err := store.db.Exec("DROP TABLE operations"); err != nil {
			t.Fatalf("drop operations table: %v", err)
		}
		if _, err := store.ReconcileStartup(context.Background(), time.Now().UTC()); err == nil {
			t.Fatal("ReconcileStartup(missing operations) error = nil")
		}
	})

	t.Run("corrupt task evidence", func(t *testing.T) {
		store, err := Open(context.Background(), filepath.Join(canonicalTempDir(t), "devcrew.db"))
		if err != nil {
			t.Fatalf("Open() error = %v", err)
		}
		t.Cleanup(func() { _ = store.Close() })
		task := reconciliationTask(1, domain.TaskWorking)
		if err := store.CreateTask(context.Background(), task); err != nil {
			t.Fatalf("CreateTask() error = %v", err)
		}
		if _, err := store.db.Exec("UPDATE tasks SET state = 'invented' WHERE handle = ?", task.Handle); err != nil {
			t.Fatalf("corrupt task: %v", err)
		}
		if _, err := store.ReconcileStartup(context.Background(), time.Now().UTC()); err == nil {
			t.Fatal("ReconcileStartup(corrupt task) error = nil")
		}
	})

	t.Run("backward operation time", func(t *testing.T) {
		store, err := Open(context.Background(), filepath.Join(canonicalTempDir(t), "devcrew.db"))
		if err != nil {
			t.Fatalf("Open() error = %v", err)
		}
		t.Cleanup(func() { _ = store.Close() })
		operation := reconciliationOperation("op-accepted-future", domain.OperationAccepted, 1)
		if err := store.RecordOperation(context.Background(), operation); err != nil {
			t.Fatalf("RecordOperation() error = %v", err)
		}
		if _, err := store.ReconcileStartup(context.Background(), operation.UpdatedAt.Add(-time.Second)); err == nil {
			t.Fatal("ReconcileStartup(backward operation time) error = nil")
		}
	})

	t.Run("corrupt accepted operation", func(t *testing.T) {
		store, err := Open(context.Background(), filepath.Join(canonicalTempDir(t), "devcrew.db"))
		if err != nil {
			t.Fatalf("Open() error = %v", err)
		}
		t.Cleanup(func() { _ = store.Close() })
		operation := reconciliationOperation("op-accepted-corrupt", domain.OperationAccepted, 1)
		if err := store.RecordOperation(context.Background(), operation); err != nil {
			t.Fatalf("RecordOperation() error = %v", err)
		}
		if _, err := store.db.Exec("UPDATE operations SET subject_digest = 'invalid' WHERE id = ?", operation.ID); err != nil {
			t.Fatalf("corrupt accepted operation: %v", err)
		}
		if _, err := store.ReconcileStartup(context.Background(), time.Now().UTC()); err == nil {
			t.Fatal("ReconcileStartup(corrupt operation) error = nil")
		}
	})

	t.Run("closed store", func(t *testing.T) {
		store, err := Open(context.Background(), filepath.Join(canonicalTempDir(t), "devcrew.db"))
		if err != nil {
			t.Fatalf("Open() error = %v", err)
		}
		if err := store.Close(); err != nil {
			t.Fatalf("Close() error = %v", err)
		}
		if _, err := store.ReconcileStartup(context.Background(), time.Now().UTC()); err == nil {
			t.Fatal("ReconcileStartup(closed) error = nil")
		}
	})
}

func TestUpdateReconciledOperation_RequiresValidExactAcceptedRow(t *testing.T) {
	store, err := Open(context.Background(), filepath.Join(canonicalTempDir(t), "devcrew.db"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	transaction, err := store.db.Begin()
	if err != nil {
		t.Fatalf("Begin() error = %v", err)
	}
	t.Cleanup(func() { _ = transaction.Rollback() })
	if err := updateReconciledOperation(context.Background(), transaction, domain.OperationRecord{}); err == nil {
		t.Fatal("updateReconciledOperation(invalid) error = nil")
	}
	operation := reconciliationOperation("op-not-stored", domain.OperationUnknown, 1)
	if err := updateReconciledOperation(context.Background(), transaction, operation); err == nil {
		t.Fatal("updateReconciledOperation(missing row) error = nil")
	}
	if _, err := transaction.Exec("DROP TABLE operations"); err != nil {
		t.Fatalf("drop operations in transaction: %v", err)
	}
	if _, err := nextReconciliationVersion(context.Background(), transaction); err == nil {
		t.Fatal("nextReconciliationVersion(missing schema) error = nil")
	}
}

func reconciliationTask(index int, state domain.TaskState) domain.Task {
	task := storeTask(fmt.Sprintf("task-reconcile-%04d", index), int64(index))
	if state != domain.TaskPrepared {
		task.ManagedRunID = "managed-run-0001"
		task.WorkspaceLeaseID = "workspace-lease-0001"
	}
	task.State = state
	return task
}

func reconciliationOperation(id string, status domain.OperationStatus, version int64) domain.OperationRecord {
	operation := storeOperation(id, version)
	operation.Status = status
	return operation
}
