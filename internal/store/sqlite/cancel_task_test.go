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

func cancelTaskMutation(taskHandle, operationID string, at time.Time) application.TaskCancelMutation {
	return application.TaskCancelMutation{
		TaskHandle: taskHandle, OperationID: operationID,
		SubjectDigest: strings.Repeat("c", 64), At: at,
	}
}

// Cancel stops work; it does not discard it. Removing the worktree here would
// make the stop irreversible at the moment an operator is least certain, and
// would collapse two decisions — stop, and throw away — that have deliberately
// different evidence requirements.
func TestStore_CancellingATaskStopsWorkAndPreservesItsArtifacts(t *testing.T) {
	store, task := openReportFixture(t, filepath.Join(canonicalTempDir(t), "devcrew.db"))
	at := time.Date(2026, time.August, 9, 16, 0, 0, 0, time.UTC)

	result, err := store.CommitTaskCancel(context.Background(),
		cancelTaskMutation(task.Handle, "operation-cancel-0001", at))
	if err != nil {
		t.Fatalf("CommitTaskCancel() error = %v", err)
	}
	if result.Task.State != domain.TaskCancelled {
		t.Fatalf("cancelled task state = %q", result.Task.State)
	}
	if result.Task.WorkspaceLeaseID != task.WorkspaceLeaseID || result.Task.WorkspaceLeaseID == "" {
		t.Errorf("cancel must preserve the workspace lease, got %q", result.Task.WorkspaceLeaseID)
	}
	if result.Task.ManagedRunID != task.ManagedRunID {
		t.Errorf("cancel must not detach the run binding, got %q", result.Task.ManagedRunID)
	}
}

// A cancelled task's worker is gone, so a pause standing against it can never be
// answered. Left in place it would show forever as a pause still pending on work
// that already stopped.
func TestStore_CancellingATaskClearsAPauseThatCanNoLongerBeAnswered(t *testing.T) {
	store, task := openReportFixture(t, filepath.Join(canonicalTempDir(t), "devcrew.db"))
	at := time.Date(2026, time.August, 9, 16, 0, 0, 0, time.UTC)
	if _, err := store.CommitTaskPauseRequest(context.Background(),
		pauseMutation(task.Handle, "operation-pause-0001", at)); err != nil {
		t.Fatalf("CommitTaskPauseRequest() error = %v", err)
	}

	if _, err := store.CommitTaskCancel(context.Background(),
		cancelTaskMutation(task.Handle, "operation-cancel-0001", at.Add(time.Minute))); err != nil {
		t.Fatalf("CommitTaskCancel() error = %v", err)
	}

	standing, err := store.PauseRequest(context.Background(), task.Handle)
	if err != nil {
		t.Fatalf("PauseRequest() error = %v", err)
	}
	if standing {
		t.Error("a cancelled task must not carry an unanswerable pause request")
	}
}

// Two operators can decide to stop the same work. The second reports the settled
// task rather than refusing, so a safe repeat does not read as a fault.
func TestStore_CancellingAnAlreadyCancelledTaskReportsItRatherThanRefusing(t *testing.T) {
	store, task := openReportFixture(t, filepath.Join(canonicalTempDir(t), "devcrew.db"))
	at := time.Date(2026, time.August, 9, 16, 0, 0, 0, time.UTC)
	if _, err := store.CommitTaskCancel(context.Background(),
		cancelTaskMutation(task.Handle, "operation-cancel-0001", at)); err != nil {
		t.Fatalf("CommitTaskCancel() error = %v", err)
	}

	second, err := store.CommitTaskCancel(context.Background(),
		cancelTaskMutation(task.Handle, "operation-cancel-0002", at.Add(time.Minute)))
	if err != nil {
		t.Fatalf("CommitTaskCancel(second operator) error = %v", err)
	}
	if second.Task.State != domain.TaskCancelled {
		t.Errorf("second cancel state = %q, want the settled task", second.Task.State)
	}
}

func TestStore_ARepeatedCancelReplays(t *testing.T) {
	store, task := openReportFixture(t, filepath.Join(canonicalTempDir(t), "devcrew.db"))
	at := time.Date(2026, time.August, 9, 16, 0, 0, 0, time.UTC)
	mutation := cancelTaskMutation(task.Handle, "operation-cancel-0001", at)

	first, err := store.CommitTaskCancel(context.Background(), mutation)
	if err != nil {
		t.Fatalf("CommitTaskCancel() error = %v", err)
	}
	second, err := store.CommitTaskCancel(context.Background(), mutation)
	if err != nil {
		t.Fatalf("CommitTaskCancel(replay) error = %v", err)
	}
	if first.Task.StateVersion != second.Task.StateVersion {
		t.Errorf("replayed cancel state version = %d, want %d", second.Task.StateVersion, first.Task.StateVersion)
	}
}

// A cleaned task has no work left to stop and its worktree is already gone.
// Cancelling it would report a stop that did not happen against artifacts that
// no longer exist.
func TestStore_RefusesToCancelATaskWhoseWorkIsAlreadyGone(t *testing.T) {
	store, _ := openReportFixture(t, filepath.Join(canonicalTempDir(t), "devcrew.db"))
	at := time.Date(2026, time.August, 9, 16, 0, 0, 0, time.UTC)
	cleaned := storeTask("task-cleaned-0001", 1)
	cleaned.State = domain.TaskCleaned
	if err := store.CreateTask(context.Background(), cleaned); err != nil {
		t.Fatalf("CreateTask() error = %v", err)
	}

	if _, err := store.CommitTaskCancel(context.Background(),
		cancelTaskMutation(cleaned.Handle, "operation-cancel-0001", at)); err == nil {
		t.Fatal("CommitTaskCancel(cleaned) error = nil, want a refusal")
	}
}
