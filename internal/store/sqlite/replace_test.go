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

func replaceMutation(
	task domain.Task,
	workspace string,
	now time.Time,
	operationID string,
	workerProfileID string,
) application.TaskReplaceMutation {
	return application.TaskReplaceMutation{
		OperationID: operationID, SubjectDigest: strings.Repeat("c", 64),
		TaskHandle: task.Handle, WorkerProfileID: workerProfileID,
		Snapshot: application.WorkspaceSnapshot{
			TaskHandle: task.Handle, RepositoryID: task.RepositoryID, WorktreePath: workspace,
			Branch: "devcrew/" + task.Handle, HeadRevision: strings.Repeat("b", 40),
			Cleanliness: application.WorkspaceClean,
		},
		At: now.Add(7 * time.Minute),
	}
}

// The work survives the swap and the task becomes launchable again under a new
// brief revision. The revision is what makes this one generation rather than a
// second worker joining the first: the previous worker's reports name a revision
// that is no longer current.
func TestStore_ReplaceReadiesAFreshGenerationAndPreservesTheWork(t *testing.T) {
	store, task, workspace, now := openPausedHandbackFixture(t, "task-replace-0001")

	result, err := store.CommitTaskReplace(context.Background(),
		replaceMutation(task, workspace, now, "operation-replace-0001", "claude-reviewed"))
	if err != nil {
		t.Fatalf("CommitTaskReplace() error = %v", err)
	}
	if result.Task.State != domain.TaskReady {
		t.Fatalf("replaced task state = %q, want ready", result.Task.State)
	}
	if result.Task.WorkerProfileID != "claude-reviewed" {
		t.Errorf("replaced worker = %q", result.Task.WorkerProfileID)
	}
	if result.Task.BriefRevision != task.BriefRevision+1 {
		t.Errorf("brief revision = %d, want %d", result.Task.BriefRevision, task.BriefRevision+1)
	}
	if result.Task.BriefRevisionHash == task.BriefRevisionHash {
		t.Error("a fresh brief revision must re-pin its hash")
	}
	// The work is preserved: the run binding and lease the worktree lives under
	// survive, because replacement changes who continues, not what exists.
	if result.Task.ManagedRunID != task.ManagedRunID || result.Task.WorkspaceLeaseID != task.WorkspaceLeaseID {
		t.Errorf("replacement must preserve host bindings, got %+v", result.Task)
	}
}

// The durable trail names both workers. "The previous worker stopped here and a
// different one continued" is the fact an operator needs afterwards, and neither
// the task row nor the handback trail carries it.
func TestStore_ReplaceRecordsWhichWorkerWasSwappedForWhich(t *testing.T) {
	store, task, workspace, now := openPausedHandbackFixture(t, "task-replace-0002")
	previous := task.WorkerProfileID

	if _, err := store.CommitTaskReplace(context.Background(),
		replaceMutation(task, workspace, now, "operation-replace-0001", "claude-reviewed")); err != nil {
		t.Fatalf("CommitTaskReplace() error = %v", err)
	}

	record, found, err := store.TaskReplacement(context.Background(), task.Handle)
	if err != nil || !found {
		t.Fatalf("TaskReplacement() = %+v, %t, %v", record, found, err)
	}
	if record.PreviousWorkerProfileID != previous || record.WorkerProfileID != "claude-reviewed" {
		t.Errorf("replacement record = %+v, want %q replaced by claude-reviewed", record, previous)
	}
	if record.HeadRevision != strings.Repeat("b", 40) {
		t.Errorf("recorded head = %q, want the inherited tree", record.HeadRevision)
	}
	if record.BriefRevision != task.BriefRevision+1 {
		t.Errorf("recorded brief revision = %d, want the fresh one", record.BriefRevision)
	}
}

// A task that is not paused still has a worker holding the worktree. Swapping
// under it would give two workers the same tree.
func TestStore_ReplaceRefusesATaskThatIsNotPaused(t *testing.T) {
	store, task, workspace, now := openPausedHandbackFixture(t, "task-replace-twice")
	if _, err := store.CommitTaskReplace(context.Background(),
		replaceMutation(task, workspace, now, "operation-replace-0001", "claude-reviewed")); err != nil {
		t.Fatalf("CommitTaskReplace() error = %v", err)
	}

	// The task is ready now, not paused: a second swap would be handing the tree
	// to a third worker while the second has already been told it owns it.
	second := replaceMutation(task, workspace, now, "operation-replace-0002", "codex-reviewed")
	second.At = now.Add(8 * time.Minute)
	_, err := store.CommitTaskReplace(context.Background(), second)

	if !errors.Is(err, application.ErrPrecondition) {
		t.Fatalf("CommitTaskReplace(ready) error = %v, want a precondition refusal", err)
	}
}

// The snapshot must describe the task's own approved worktree. One naming a
// different tree would record a replacement against work the service never
// prepared.
func TestStore_ReplaceRefusesASnapshotForADifferentTree(t *testing.T) {
	store, task, workspace, now := openPausedHandbackFixture(t, "task-replace-0003")
	mutation := replaceMutation(task, workspace, now, "operation-replace-0001", "claude-reviewed")
	mutation.Snapshot.WorktreePath = "/approved/worktrees/somewhere-else"

	_, err := store.CommitTaskReplace(context.Background(), mutation)

	if !errors.Is(err, application.ErrPrecondition) {
		t.Fatalf("CommitTaskReplace(foreign tree) error = %v, want a precondition refusal", err)
	}
}

func TestStore_ReplaceRefusesAnInvalidMutation(t *testing.T) {
	store, task, workspace, now := openPausedHandbackFixture(t, "task-replace-0004")

	for name, mutate := range map[string]func(*application.TaskReplaceMutation){
		"no profile":     func(m *application.TaskReplaceMutation) { m.WorkerProfileID = "" },
		"forged profile": func(m *application.TaskReplaceMutation) { m.WorkerProfileID = "../../bin/sh" },
		"no digest":      func(m *application.TaskReplaceMutation) { m.SubjectDigest = "" },
		"bad snapshot":   func(m *application.TaskReplaceMutation) { m.Snapshot.HeadRevision = "short" },
	} {
		mutation := replaceMutation(task, workspace, now, "operation-replace-"+name, "claude-reviewed")
		mutate(&mutation)
		if _, err := store.CommitTaskReplace(context.Background(), mutation); err == nil {
			t.Errorf("%s: CommitTaskReplace() error = nil, want a refusal", name)
		}
	}
}

func TestStore_ARepeatedReplaceReplays(t *testing.T) {
	store, task, workspace, now := openPausedHandbackFixture(t, "task-replace-0005")
	mutation := replaceMutation(task, workspace, now, "operation-replace-0001", "claude-reviewed")

	first, err := store.CommitTaskReplace(context.Background(), mutation)
	if err != nil {
		t.Fatalf("CommitTaskReplace() error = %v", err)
	}
	second, err := store.CommitTaskReplace(context.Background(), mutation)
	if err != nil {
		t.Fatalf("CommitTaskReplace(replay) error = %v", err)
	}
	if first.Task.StateVersion != second.Task.StateVersion ||
		first.Task.BriefRevision != second.Task.BriefRevision {
		t.Errorf("replayed replacement = %+v, want the first result %+v", second.Task, first.Task)
	}
}

// A task nobody replaced reports no record rather than an error.
func TestStore_ATaskWithNoReplacementReportsNoRecord(t *testing.T) {
	store, task := openReportFixture(t, filepath.Join(canonicalTempDir(t), "devcrew.db"))

	record, found, err := store.TaskReplacement(context.Background(), task.Handle)

	if err != nil {
		t.Fatalf("TaskReplacement() error = %v", err)
	}
	if found {
		t.Errorf("unexpected replacement record = %+v", record)
	}
}

func TestStore_TaskReplacementRefusesAnAbsentContext(t *testing.T) {
	store, task := openReportFixture(t, filepath.Join(canonicalTempDir(t), "devcrew.db"))

	if _, _, err := store.TaskReplacement(nilPromotionContext(), task.Handle); err == nil {
		t.Error("TaskReplacement(nil) error = nil")
	}
}

// A replacement naming a task that does not exist is refused, never recorded
// against nothing.
func TestStore_ReplaceRefusesAnUnknownTask(t *testing.T) {
	store, task, workspace, now := openPausedHandbackFixture(t, "task-replace-unknown")
	mutation := replaceMutation(task, workspace, now, "operation-replace-0001", "claude-reviewed")
	mutation.TaskHandle = "task-absent-0001"
	mutation.Snapshot.TaskHandle = "task-absent-0001"

	if _, err := store.CommitTaskReplace(context.Background(), mutation); !errors.Is(err, application.ErrNotFound) {
		t.Fatalf("CommitTaskReplace(unknown) error = %v, want not-found", err)
	}
}

// A replacement timestamp older than the task it acts on would put the durable
// trail out of order.
func TestStore_ReplaceRefusesAStaleTimestamp(t *testing.T) {
	store, task, workspace, now := openPausedHandbackFixture(t, "task-replace-stale")
	mutation := replaceMutation(task, workspace, now, "operation-replace-0001", "claude-reviewed")
	mutation.At = task.UpdatedAt.Add(-time.Hour).UTC()

	if _, err := store.CommitTaskReplace(context.Background(), mutation); !errors.Is(err, application.ErrPrecondition) {
		t.Fatalf("CommitTaskReplace(stale) error = %v, want a precondition refusal", err)
	}
}

// A validation process still running owns the worktree as surely as a worker
// does. Swapping under it would hand the tree to a new worker while checks are
// still reading and writing it, and the evidence those checks produce would
// describe a tree that changed underneath them.
func TestStore_ReplaceRefusesWhileAValidationProcessStillRuns(t *testing.T) {
	store, task, workspace, now := openPausedHandbackFixture(t, "task-replace-validating")
	if _, err := store.db.Exec(`INSERT INTO validation_processes(
            operation_id, task_handle, program_id, executable_label, pid,
            start_identity, process_group_identity, state, started_at, observed_at)
        VALUES ('validate-replace-live', ?, 'go-test', 'go', 4242, 'start', 'group', 'running', ?, ?)`,
		task.Handle, formatTime(task.UpdatedAt), formatTime(task.UpdatedAt)); err != nil {
		t.Fatalf("seed running validation process: %v", err)
	}

	_, err := store.CommitTaskReplace(context.Background(),
		replaceMutation(task, workspace, now, "operation-replace-0001", "claude-reviewed"))

	if !errors.Is(err, application.ErrPrecondition) {
		t.Fatalf("CommitTaskReplace(validating) error = %v, want a precondition refusal", err)
	}
}

// A terminal that never settled means the previous worker may still be alive.
// Replacing then would put two workers in one worktree, each believing it owns
// the tree.
func TestStore_ReplaceRefusesWhileTheTerminalIsUnsettled(t *testing.T) {
	store, task, workspace, now := openPausedReplaceFixtureWithLiveTerminal(t, "task-replace-live-terminal")

	_, err := store.CommitTaskReplace(context.Background(),
		replaceMutation(task, workspace, now, "operation-replace-0001", "claude-reviewed"))

	if !errors.Is(err, application.ErrPrecondition) {
		t.Fatalf("CommitTaskReplace(live terminal) error = %v, want a precondition refusal", err)
	}
}

// Same as the paused handback fixture but the worker's terminal never exits, so
// the previous worker is not provably gone.
func openPausedReplaceFixtureWithLiveTerminal(
	t *testing.T,
	handle string,
) (*Store, domain.Task, string, time.Time) {
	t.Helper()
	store, task, workspace, now := openTerminalLifecycleFixture(t, handle, true)
	if _, err := store.CommitTerminalEvent(context.Background(), terminalEventMutation(
		task, "operation-running-"+handle, application.TerminalRunning, now.Add(3*time.Minute),
	)); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CommitWorkerLaunchAcknowledgement(context.Background(),
		application.WorkerLaunchAcknowledgementMutation{
			OperationID: "operation-ack-" + handle, SubjectDigest: strings.Repeat("f", 64),
			Acknowledgement: terminalLaunchAcknowledgement(task, workspace), At: now.Add(3 * time.Minute),
		}); err != nil {
		t.Fatal(err)
	}
	client := reportClient(t, store, task, now.Add(4*time.Minute))
	if _, err := client.Report(context.Background(),
		sqliteWorkerReport(task, "report-paused-"+handle, domain.ReportPaused)); err != nil {
		t.Fatal(err)
	}
	paused, err := store.GetTask(context.Background(), task.Handle)
	if err != nil || paused.State != domain.TaskPaused {
		t.Fatalf("paused task = %#v, %v", paused, err)
	}
	return store, paused, workspace, now
}

// A replacement must report a storage failure, never succeed past one. Each case
// removes a table the swap depends on, which is this package's idiom for proving
// that a failed read or write surfaces instead of being swallowed — a silent
// success here would ready a task for a worker whose swap was never recorded.
func TestStore_ReplaceReportsStorageFailuresInsteadOfSwallowingThem(t *testing.T) {
	for name, breakStore := range map[string]func(*Store){
		"missing preparation table": func(store *Store) {
			_, _ = store.db.Exec("ALTER TABLE task_preparations RENAME TO unavailable_task_preparations")
		},
		"missing terminal table": func(store *Store) {
			_, _ = store.db.Exec("ALTER TABLE task_terminal_bindings RENAME TO unavailable_terminal_bindings")
		},
		"missing validation table": func(store *Store) {
			_, _ = store.db.Exec("ALTER TABLE validation_processes RENAME TO unavailable_validation_processes")
		},
		"missing replacement table": func(store *Store) {
			_, _ = store.db.Exec("ALTER TABLE task_replacements RENAME TO unavailable_task_replacements")
		},
	} {
		store, task, workspace, now := openPausedHandbackFixture(t, "task-replace-broken")
		breakStore(store)

		if _, err := store.CommitTaskReplace(context.Background(),
			replaceMutation(task, workspace, now, "operation-replace-0001", "claude-reviewed"),
		); err == nil {
			t.Errorf("%s: CommitTaskReplace() error = nil, want a storage failure", name)
		}
		_ = store.Close()
	}
}

// The same for the read: an unreadable replacement trail is reported, never
// rendered as "this task was never replaced".
func TestStore_TaskReplacementReportsAnUnreadableTrail(t *testing.T) {
	store, task := openReportFixture(t, filepath.Join(canonicalTempDir(t), "devcrew.db"))
	if _, err := store.db.Exec("ALTER TABLE task_replacements RENAME TO unavailable_task_replacements"); err != nil {
		t.Fatalf("break replacement table: %v", err)
	}

	if _, _, err := store.TaskReplacement(context.Background(), task.Handle); err == nil {
		t.Fatal("TaskReplacement() with an unreadable trail error = nil")
	}
}
