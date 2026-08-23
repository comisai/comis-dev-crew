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
