package sqlite

import (
	"context"
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
