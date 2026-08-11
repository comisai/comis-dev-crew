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

type durableHandbackStore interface {
	CommitTaskHandback(context.Context, application.TaskHandbackMutation) (application.MutationResult, error)
}

func TestTaskHandback_PersistsFreshSnapshotAndStartsDeveloperWorkValidation(t *testing.T) {
	store, task, workspace, now := openPausedHandbackFixture(t, "task-handback-clean")
	t.Cleanup(func() { _ = store.Close() })
	mutation := application.TaskHandbackMutation{
		OperationID: "operation-handback-clean", SubjectDigest: strings.Repeat("d", 64),
		TaskHandle: task.Handle, Action: application.HandbackValidateDeveloperWork,
		Snapshot: application.WorkspaceSnapshot{
			TaskHandle: task.Handle, RepositoryID: task.RepositoryID, WorktreePath: workspace,
			Branch: "devcrew/task-handback-clean", HeadRevision: strings.Repeat("b", 40),
			Cleanliness: application.WorkspaceClean,
		},
		At: now.Add(7 * time.Minute),
	}
	handbacks := any(store).(durableHandbackStore)
	result, err := handbacks.CommitTaskHandback(context.Background(), mutation)
	if err != nil {
		t.Fatalf("CommitTaskHandback() error = %v", err)
	}
	if result.Task.State != domain.TaskValidating || result.Operation.ID != mutation.OperationID {
		t.Fatalf("CommitTaskHandback() = %#v", result)
	}
	replay, err := handbacks.CommitTaskHandback(context.Background(), mutation)
	if err != nil || replay.Task.State != domain.TaskValidating || replay.Operation != result.Operation {
		t.Fatalf("CommitTaskHandback(replay) = %#v, %v", replay, err)
	}
	var storedHead, storedCleanliness string
	if err := store.db.QueryRow(`SELECT head_revision, cleanliness FROM task_handbacks WHERE operation_id = ?`, mutation.OperationID).
		Scan(&storedHead, &storedCleanliness); err != nil {
		t.Fatalf("read task handback: %v", err)
	}
	if storedHead != mutation.Snapshot.HeadRevision || storedCleanliness != string(application.WorkspaceClean) {
		t.Fatalf("stored handback = %q/%q", storedHead, storedCleanliness)
	}
}

func TestTaskHandback_RefusesLiveTerminalAndAlteredWorkspaceAuthority(t *testing.T) {
	store, task, workspace, now := openPausedHandbackFixture(t, "task-handback-refusal")
	t.Cleanup(func() { _ = store.Close() })
	base := application.TaskHandbackMutation{
		OperationID: "operation-handback-refusal", SubjectDigest: strings.Repeat("e", 64),
		TaskHandle: task.Handle, Action: application.HandbackValidateDeveloperWork,
		Snapshot: application.WorkspaceSnapshot{
			TaskHandle: task.Handle, RepositoryID: task.RepositoryID, WorktreePath: workspace,
			Branch: "devcrew/task-handback-refusal", HeadRevision: strings.Repeat("c", 40),
			Cleanliness: application.WorkspaceDirty,
		},
		At: now.Add(7 * time.Minute),
	}
	if _, err := store.db.Exec(`UPDATE task_terminal_bindings SET latest_transition = 'running' WHERE task_handle = ?`, task.Handle); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CommitTaskHandback(context.Background(), base); !errors.Is(err, application.ErrPrecondition) {
		t.Fatalf("CommitTaskHandback(live terminal) error = %v", err)
	}
	if _, err := store.db.Exec(`UPDATE task_terminal_bindings SET latest_transition = 'exited' WHERE task_handle = ?`, task.Handle); err != nil {
		t.Fatal(err)
	}
	altered := base
	altered.OperationID = "operation-handback-altered"
	altered.Snapshot.WorktreePath = workspace + "-other"
	if _, err := store.CommitTaskHandback(context.Background(), altered); !errors.Is(err, application.ErrPrecondition) {
		t.Fatalf("CommitTaskHandback(altered workspace) error = %v", err)
	}
}

func openPausedHandbackFixture(t *testing.T, handle string) (*Store, domain.Task, string, time.Time) {
	t.Helper()
	store, task, workspace, now := openTerminalLifecycleFixture(t, handle, true)
	if _, err := store.CommitTerminalEvent(context.Background(), terminalEventMutation(
		task, "operation-running-"+handle, application.TerminalRunning, now.Add(3*time.Minute),
	)); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CommitWorkerLaunchAcknowledgement(context.Background(), application.WorkerLaunchAcknowledgementMutation{
		OperationID: "operation-ack-" + handle, SubjectDigest: strings.Repeat("f", 64),
		Acknowledgement: terminalLaunchAcknowledgement(task, workspace), At: now.Add(3 * time.Minute),
	}); err != nil {
		t.Fatal(err)
	}
	client := reportClient(t, store, task, now.Add(4*time.Minute))
	if _, err := client.Report(context.Background(), sqliteWorkerReport(task, "report-paused-"+handle, domain.ReportPaused)); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CommitTerminalEvent(context.Background(), terminalEventMutation(
		task, "operation-exited-"+handle, application.TerminalExited, now.Add(5*time.Minute),
	)); err != nil {
		t.Fatal(err)
	}
	paused, err := store.GetTask(context.Background(), task.Handle)
	if err != nil || paused.State != domain.TaskPaused {
		t.Fatalf("paused task = %#v, %v", paused, err)
	}
	return store, paused, workspace, now
}
