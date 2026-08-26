package sqlite

import (
	"context"
	"errors"
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

func TestStore_CancellingASettledUnknownTaskReconcilesItWithoutDiscardingAuthority(t *testing.T) {
	store, unknown, at := unknownTaskAfterTerminal(
		t, "task-cancel-settled-unknown", application.TerminalExited,
	)

	result, err := store.CommitTaskCancel(context.Background(),
		cancelTaskMutation(unknown.Handle, "operation-cancel-settled-unknown", at))
	if err != nil {
		t.Fatalf("CommitTaskCancel(settled unknown) error = %v", err)
	}
	if result.Task.State != domain.TaskCancelled || result.Task.StateVersion <= unknown.StateVersion {
		t.Fatalf("cancelled unknown task = %#v, want newer cancelled state", result.Task)
	}
	if result.Task.ManagedRunID != unknown.ManagedRunID ||
		result.Task.WorkspaceLeaseID != unknown.WorkspaceLeaseID ||
		result.Task.ExecutionAttachmentID != unknown.ExecutionAttachmentID {
		t.Fatalf("cancelled unknown task lost durable authority: %#v", result.Task)
	}
}

func TestStore_RefusesToCancelAnUnknownTaskWithoutExactSettledExecutionProof(t *testing.T) {
	for _, test := range []struct {
		name       string
		handle     string
		transition application.TerminalTransition
		mutate     func(*Store, domain.Task)
	}{
		{name: "terminal remains lost", handle: "task-unknown-lost-0001", transition: application.TerminalLost},
		{name: "terminal authority differs", handle: "task-unknown-auth-0001", transition: application.TerminalExited, mutate: func(store *Store, task domain.Task) {
			_, _ = store.db.Exec(
				"UPDATE task_terminal_bindings SET managed_run_id = 'managed-run-other' WHERE task_handle = ?",
				task.Handle,
			)
		}},
		{name: "validation remains active", handle: "task-unknown-valid-0001", transition: application.TerminalExited, mutate: func(store *Store, task domain.Task) {
			_, _ = store.db.Exec(`INSERT INTO validation_processes(
				operation_id, task_handle, program_id, executable_label, pid,
				start_identity, process_group_identity, state, started_at, observed_at)
				VALUES ('validate-cancel-unknown', ?, 'go-test', 'go', 123,
				'start-123', 'group-123', 'running', ?, ?)`,
				task.Handle, formatTime(task.UpdatedAt), formatTime(task.UpdatedAt))
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			store, unknown, at := unknownTaskAfterTerminal(t, test.handle, test.transition)
			if test.mutate != nil {
				test.mutate(store, unknown)
			}

			_, err := store.CommitTaskCancel(context.Background(),
				cancelTaskMutation(unknown.Handle, "operation-cancel-unknown-refused", at))
			if !errors.Is(err, application.ErrPrecondition) {
				t.Fatalf("CommitTaskCancel(unsafe unknown) error = %v, want precondition refusal", err)
			}
			unchanged, readErr := store.GetTask(context.Background(), unknown.Handle)
			if readErr != nil || unchanged.State != domain.TaskUnknown ||
				unchanged.StateVersion != unknown.StateVersion {
				t.Fatalf("refused unknown task = %#v, %v, want unchanged version %d", unchanged, readErr, unknown.StateVersion)
			}
		})
	}
}

func unknownTaskAfterTerminal(
	t *testing.T,
	handle string,
	transition application.TerminalTransition,
) (*Store, domain.Task, time.Time) {
	t.Helper()
	store, task, _, now := openTerminalLifecycleFixture(t, handle, true)
	t.Cleanup(func() { _ = store.Close() })
	if _, err := store.CommitTerminalEvent(context.Background(), terminalEventMutation(
		task, "operation-terminal-running-"+handle, application.TerminalRunning, now.Add(3*time.Minute),
	)); err != nil {
		t.Fatalf("CommitTerminalEvent(running) error = %v", err)
	}
	result, err := store.CommitTerminalEvent(context.Background(), terminalEventMutation(
		task, "operation-terminal-settled-"+handle, transition, now.Add(4*time.Minute),
	))
	if err != nil || result.Task.State != domain.TaskUnknown {
		t.Fatalf("CommitTerminalEvent(%s) = %#v, %v, want unknown", transition, result, err)
	}
	return store, result.Task, now.Add(5 * time.Minute)
}
