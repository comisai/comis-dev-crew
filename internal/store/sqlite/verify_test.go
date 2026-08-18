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

func verifyMutation(taskHandle, operationID string, at time.Time) application.TaskVerifyMutation {
	return application.TaskVerifyMutation{
		TaskHandle: taskHandle, OperationID: operationID,
		SubjectDigest: strings.Repeat("e", 64), At: at,
	}
}

// Verify opens validation; it does not judge. The reviewed profile, the
// candidate inspection and the evidence refresh all belong to the supervisor
// that already owns them, and a command that settled the outcome here would be a
// second judge with a different view of the same tree.
func TestStore_VerifyOpensValidationWithoutJudgingTheTask(t *testing.T) {
	store, task := openReportFixture(t, filepath.Join(canonicalTempDir(t), "devcrew.db"))
	at := time.Date(2026, time.August, 9, 16, 0, 0, 0, time.UTC)

	result, err := store.CommitTaskVerify(context.Background(),
		verifyMutation(task.Handle, "operation-verify-0001", at))
	if err != nil {
		t.Fatalf("CommitTaskVerify() error = %v", err)
	}
	if result.Task.State != domain.TaskValidating {
		t.Fatalf("verified state = %q, want validating", result.Task.State)
	}
	// Nothing here may reach a terminal judgement.
	for _, forbidden := range []domain.TaskState{
		domain.TaskCandidateComplete, domain.TaskDelivered, domain.TaskFailed,
	} {
		if result.Task.State == forbidden {
			t.Errorf("verify must not decide the outcome, got %q", result.Task.State)
		}
	}
}

// A task already validating is left exactly as it is. Restarting would abandon a
// run that is mid-flight and whose process the service is still tracking.
func TestStore_VerifyLeavesAValidationAlreadyInFlightAlone(t *testing.T) {
	store, task := openReportFixture(t, filepath.Join(canonicalTempDir(t), "devcrew.db"))
	at := time.Date(2026, time.August, 9, 16, 0, 0, 0, time.UTC)
	first, err := store.CommitTaskVerify(context.Background(),
		verifyMutation(task.Handle, "operation-verify-0001", at))
	if err != nil {
		t.Fatalf("CommitTaskVerify() error = %v", err)
	}

	second, err := store.CommitTaskVerify(context.Background(),
		verifyMutation(task.Handle, "operation-verify-0002", at.Add(time.Minute)))
	if err != nil {
		t.Fatalf("CommitTaskVerify(second) error = %v", err)
	}
	if second.Task.State != domain.TaskValidating {
		t.Errorf("second verify state = %q", second.Task.State)
	}
	if second.Task.StateVersion < first.Task.StateVersion {
		t.Errorf("second verify moved the task backwards: %d < %d",
			second.Task.StateVersion, first.Task.StateVersion)
	}
}

// A settled task has nothing left to validate, and a prepared one has no work
// yet. Opening validation on either would start a run against a tree that either
// no longer matters or does not exist.
func TestStore_VerifyRefusesATaskWithNothingToValidate(t *testing.T) {
	store, _ := openReportFixture(t, filepath.Join(canonicalTempDir(t), "devcrew.db"))
	at := time.Date(2026, time.August, 9, 16, 0, 0, 0, time.UTC)
	for name, state := range map[string]domain.TaskState{
		"prepared": domain.TaskPrepared,
		"cleaned":  domain.TaskCleaned,
		"failed":   domain.TaskFailed,
	} {
		task := storeTask("task-verify-"+name, 1)
		task.State = state
		if state != domain.TaskPrepared {
			// A settled task carries the host binding it was activated under;
			// creating one without it is refused before the state is even read.
			task.ManagedRunID = "managed-run-" + name
			task.WorkspaceLeaseID = "workspace-lease-" + name
		}
		if err := store.CreateTask(context.Background(), task); err != nil {
			t.Fatalf("CreateTask(%s) error = %v", name, err)
		}
		if _, err := store.CommitTaskVerify(context.Background(),
			verifyMutation(task.Handle, "operation-verify-"+name, at)); err == nil {
			t.Errorf("%s: CommitTaskVerify() error = nil, want a refusal", name)
		}
	}
}

func TestStore_VerifyRefusesANonUTCTime(t *testing.T) {
	store, task := openReportFixture(t, filepath.Join(canonicalTempDir(t), "devcrew.db"))
	at := time.Date(2026, time.August, 9, 16, 0, 0, 0, time.FixedZone("local", 3600))

	if _, err := store.CommitTaskVerify(context.Background(),
		verifyMutation(task.Handle, "operation-verify-0001", at)); !errors.Is(err, application.ErrPrecondition) {
		t.Fatalf("CommitTaskVerify(local time) error = %v, want a precondition refusal", err)
	}
}

func TestStore_ARepeatedVerifyReplays(t *testing.T) {
	store, task := openReportFixture(t, filepath.Join(canonicalTempDir(t), "devcrew.db"))
	at := time.Date(2026, time.August, 9, 16, 0, 0, 0, time.UTC)
	mutation := verifyMutation(task.Handle, "operation-verify-0001", at)

	first, err := store.CommitTaskVerify(context.Background(), mutation)
	if err != nil {
		t.Fatalf("CommitTaskVerify() error = %v", err)
	}
	second, err := store.CommitTaskVerify(context.Background(), mutation)
	if err != nil {
		t.Fatalf("CommitTaskVerify(replay) error = %v", err)
	}
	if first.Task.StateVersion != second.Task.StateVersion {
		t.Errorf("replayed state version = %d, want %d", second.Task.StateVersion, first.Task.StateVersion)
	}
}

// A mutation must not be recorded as having happened before the state it acts
// on. Accepting a stale timestamp would put the durable trail out of order, and
// a later reader could not tell which change came first.
func TestStore_TaskMutationsRefuseATimestampOlderThanTheTaskItself(t *testing.T) {
	store, task := openReportFixture(t, filepath.Join(canonicalTempDir(t), "devcrew.db"))
	stale := task.UpdatedAt.Add(-time.Hour).UTC()

	if _, err := store.CommitTaskVerify(context.Background(),
		verifyMutation(task.Handle, "operation-verify-stale", stale)); !errors.Is(err, application.ErrPrecondition) {
		t.Errorf("CommitTaskVerify(stale) error = %v, want a precondition refusal", err)
	}

	paused, task2, at := pausedTaskFixture(t)
	_ = at
	if _, err := paused.CommitTaskResume(context.Background(),
		resumeMutation(task2.Handle, "operation-resume-stale", task2.CreatedAt.Add(-time.Hour).UTC()),
	); !errors.Is(err, application.ErrPrecondition) {
		t.Errorf("CommitTaskResume(stale) error = %v, want a precondition refusal", err)
	}
}

// An unknown handle is refused, never treated as a no-op success: an operator
// who mistyped must not be told the work they meant to act on was acted on.
func TestStore_TaskMutationsRefuseAnUnknownHandle(t *testing.T) {
	store, _ := openReportFixture(t, filepath.Join(canonicalTempDir(t), "devcrew.db"))
	at := time.Date(2026, time.August, 9, 16, 0, 0, 0, time.UTC)

	if _, err := store.CommitTaskVerify(context.Background(),
		verifyMutation("task-absent-0001", "operation-verify-absent", at)); !errors.Is(err, application.ErrNotFound) {
		t.Errorf("CommitTaskVerify(unknown) error = %v, want not-found", err)
	}
	if _, err := store.CommitTaskResume(context.Background(),
		resumeMutation("task-absent-0001", "operation-resume-absent", at)); !errors.Is(err, application.ErrNotFound) {
		t.Errorf("CommitTaskResume(unknown) error = %v, want not-found", err)
	}
}
