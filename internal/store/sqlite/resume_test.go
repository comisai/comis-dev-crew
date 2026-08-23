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

func resumeMutation(taskHandle, operationID string, at time.Time) application.TaskResumeMutation {
	return application.TaskResumeMutation{
		TaskHandle: taskHandle, OperationID: operationID,
		SubjectDigest: strings.Repeat("d", 64), ObservedHeadRevision: strings.Repeat("b", 40),
		At: at,
	}
}

func pausedTaskFixture(t *testing.T) (*Store, domain.Task, time.Time) {
	t.Helper()
	store, task, _, _ := openPausedHandbackFixture(t, "task-resume-store-0001")
	return store, task, task.UpdatedAt
}

func TestStore_ResumeReadiesAPausedTaskForAnAuthenticatedRelaunch(t *testing.T) {
	store, task, at := pausedTaskFixture(t)

	result, err := store.CommitTaskResume(context.Background(),
		resumeMutation(task.Handle, "operation-resume-0001", at.Add(time.Minute)))
	if err != nil {
		t.Fatalf("CommitTaskResume() error = %v", err)
	}
	if result.Task.State != domain.TaskReady {
		t.Errorf("resumed state = %q, want ready", result.Task.State)
	}
	launch, found, err := store.TaskResumeLaunch(context.Background(), task.Handle)
	if err != nil || !found || launch.TaskHandle != task.Handle ||
		launch.HeadRevision != strings.Repeat("b", 40) || launch.StateVersion != result.Task.StateVersion {
		t.Fatalf("TaskResumeLaunch() = %#v, %t, %v", launch, found, err)
	}
}

func TestStore_ResumedTerminalGenerationCannotReuseThePreviousLaunchEvidence(t *testing.T) {
	store, task, workspace, _ := openPausedHandbackFixture(t, "task-resume-terminal-generation")
	resumeAt := task.UpdatedAt.Add(time.Minute)
	resumed, err := store.CommitTaskResume(context.Background(),
		resumeMutation(task.Handle, "operation-resume-terminal-generation", resumeAt))
	if err != nil {
		t.Fatal(err)
	}
	started, err := store.CommitTaskStart(context.Background(), application.TaskStartMutation{
		TaskHandle: task.Handle, OperationID: "operation-start-resumed-generation",
		SubjectDigest: strings.Repeat("e", 64), At: resumeAt.Add(time.Minute),
	})
	if err != nil {
		t.Fatal(err)
	}
	created := terminalEventMutation(
		started.Task, "operation-created-resumed-generation", application.TerminalCreated,
		resumeAt.Add(2*time.Minute),
	)
	created.TerminalSessionID = "terminal-session-resumed"
	if _, err := store.CommitTerminalEvent(context.Background(), created); err != nil {
		t.Fatalf("CommitTerminalEvent(resumed created) error = %v", err)
	}
	running := created
	running.OperationID = "operation-running-resumed-generation"
	running.Transition = application.TerminalRunning
	running.At = resumeAt.Add(3 * time.Minute)
	runningResult, err := store.CommitTerminalEvent(context.Background(), running)
	if err != nil {
		t.Fatalf("CommitTerminalEvent(resumed running) error = %v", err)
	}
	if runningResult.Task.State != domain.TaskLaunching {
		t.Fatalf("old launch acknowledgement advanced resumed task to %q", runningResult.Task.State)
	}
	acknowledgement := terminalLaunchAcknowledgement(resumed.Task, workspace)
	acknowledged, err := store.CommitWorkerLaunchAcknowledgement(context.Background(),
		application.WorkerLaunchAcknowledgementMutation{
			OperationID: "operation-ack-resumed-generation", SubjectDigest: strings.Repeat("f", 64),
			Acknowledgement: acknowledgement, At: resumeAt.Add(4 * time.Minute),
		})
	if err != nil {
		t.Fatalf("CommitWorkerLaunchAcknowledgement(resumed) error = %v", err)
	}
	if acknowledged.Task.State != domain.TaskWorking {
		t.Fatalf("resumed acknowledgement state = %q, want working", acknowledged.Task.State)
	}
	var acknowledgementCount int
	if err := store.db.QueryRowContext(context.Background(),
		"SELECT COUNT(*) FROM task_launch_acknowledgements WHERE task_handle = ?", task.Handle,
	).Scan(&acknowledgementCount); err != nil || acknowledgementCount != 2 {
		t.Fatalf("launch acknowledgement generations = %d, %v, want 2", acknowledgementCount, err)
	}
}

func TestStore_ResumeRefusesATaskThatIsNotPaused(t *testing.T) {
	store, task := openReportFixture(t, filepath.Join(canonicalTempDir(t), "devcrew.db"))
	at := time.Date(2026, time.August, 9, 16, 0, 0, 0, time.UTC)

	_, err := store.CommitTaskResume(context.Background(),
		resumeMutation(task.Handle, "operation-resume-0001", at))
	if !errors.Is(err, application.ErrPrecondition) {
		t.Fatalf("CommitTaskResume(working) error = %v, want a precondition refusal", err)
	}
}

func TestStore_ResumeLaunchReadDistinguishesNoGenerationFromFailure(t *testing.T) {
	store, _ := openReportFixture(t, filepath.Join(canonicalTempDir(t), "devcrew.db"))
	launch, found, err := store.TaskResumeLaunch(context.Background(), "task-without-resume-generation")
	if err != nil || found || launch != (application.TaskResumeLaunch{}) {
		t.Fatalf("TaskResumeLaunch(absent) = %#v, %t, %v", launch, found, err)
	}
	var unavailable *Store
	if _, _, err := unavailable.TaskResumeLaunch(context.Background(), "task-without-store"); err == nil {
		t.Fatal("TaskResumeLaunch(unavailable store) error = nil")
	}
}

func TestStore_ResumeRefusesUntilThePreviousTerminalIsSettled(t *testing.T) {
	store, task := openReportFixture(t, filepath.Join(canonicalTempDir(t), "devcrew.db"))
	at := time.Date(2026, time.August, 23, 19, 0, 0, 0, time.UTC)
	if _, err := store.CommitReport(context.Background(),
		directReportMutation(task, sqliteWorkerReport(task, "report-paused-unsettled", domain.ReportPaused), at)); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CommitTaskResume(context.Background(),
		resumeMutation(task.Handle, "operation-resume-unsettled", at.Add(time.Minute))); !errors.Is(err, application.ErrPrecondition) {
		t.Fatalf("CommitTaskResume(unsettled terminal) error = %v", err)
	}
}

// The head is the durable record of which tree was proven clean. A resume that
// accepted an absent or malformed one would record that some tree was checked
// without saying which.
func TestStore_ResumeRefusesAnUnprovenHead(t *testing.T) {
	store, task, at := pausedTaskFixture(t)

	for name, head := range map[string]string{
		"absent": "",
		"short":  strings.Repeat("b", 39),
		"forged": strings.Repeat("z", 40),
		"branch": "refs/heads/main",
	} {
		mutation := resumeMutation(task.Handle, "operation-resume-"+name, at.Add(time.Minute))
		mutation.ObservedHeadRevision = head
		if _, err := store.CommitTaskResume(context.Background(), mutation); !errors.Is(err, application.ErrPrecondition) {
			t.Errorf("%s head: error = %v, want a precondition refusal", name, err)
		}
	}
}

func TestStore_ARepeatedResumeReplays(t *testing.T) {
	store, task, at := pausedTaskFixture(t)
	mutation := resumeMutation(task.Handle, "operation-resume-0001", at.Add(time.Minute))

	first, err := store.CommitTaskResume(context.Background(), mutation)
	if err != nil {
		t.Fatalf("CommitTaskResume() error = %v", err)
	}
	second, err := store.CommitTaskResume(context.Background(), mutation)
	if err != nil {
		t.Fatalf("CommitTaskResume(replay) error = %v", err)
	}
	if first.Task.StateVersion != second.Task.StateVersion {
		t.Errorf("replayed state version = %d, want %d", second.Task.StateVersion, first.Task.StateVersion)
	}
}
