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

func discardMutation(taskHandle, operationID string, at time.Time) application.TaskDiscardMutation {
	return application.TaskDiscardMutation{
		OperationID: operationID, SubjectDigest: strings.Repeat("8", 64),
		TaskHandle: taskHandle, ReleaseOperationID: "release-" + operationID,
		ReleasedAt: at, At: at,
	}
}

// A settled task reached that state through the ordinary path, so it carries the
// preparation, host binding and terminal history a discard has to release. A
// hand-built task row would let the test pass against a shape production never
// produces.
func settledTask(t *testing.T, handle string) (*Store, domain.Task) {
	t.Helper()
	store, paused, _, now := openPausedHandbackFixture(t, handle)
	result, err := store.CommitTaskCancel(context.Background(),
		cancelTaskMutation(paused.Handle, "operation-cancel-"+handle, now.Add(6*time.Minute)))
	if err != nil {
		t.Fatalf("CommitTaskCancel() error = %v", err)
	}
	if result.Task.State != domain.TaskCancelled {
		t.Fatalf("cancelled task state = %q", result.Task.State)
	}
	return store, result.Task
}

// Cancellation preserves work on purpose, and cleanup requires delivery evidence
// a cancelled task will never have. Without discard the worktree, lease and run
// binding of every cancelled task stay held with nothing able to release them.
func TestStore_DiscardOpensRemovalForWorkThatNeverDelivered(t *testing.T) {
	store, task := settledTask(t, "task-discard-cancelled")
	at := task.UpdatedAt.Add(time.Minute).UTC()

	record, err := store.BeginTaskDiscard(context.Background(),
		discardMutation(task.Handle, "operation-discard-0001", at))

	if err != nil {
		t.Fatalf("BeginTaskDiscard() error = %v", err)
	}
	if !record.Discard {
		t.Error("the record must say removal was authorised by acknowledgement, not evidence")
	}
	if record.ManagedRunID != task.ManagedRunID || record.WorkspaceLeaseID != task.WorkspaceLeaseID {
		t.Errorf("discard must carry the host authority it will release: %+v", record)
	}
	if record.PullRequestID != "" || record.ReportArtifactHash != "" {
		t.Errorf("a discard has no delivery evidence: %+v", record)
	}
}

// Only work the service has already stopped may be discarded. A running task
// owns its worktree, and a delivered one has cleanup's evidence-gated path.
func TestStore_DiscardRefusesWorkThatIsNotSettled(t *testing.T) {
	store, working := openReportFixture(t, filepath.Join(canonicalTempDir(t), "devcrew.db"))
	at := working.UpdatedAt.Add(time.Minute).UTC()

	_, err := store.BeginTaskDiscard(context.Background(),
		discardMutation(working.Handle, "operation-discard-0001", at))

	if !errors.Is(err, application.ErrPrecondition) {
		t.Fatalf("BeginTaskDiscard(working) error = %v, want a precondition refusal", err)
	}
}

// The discard flag must survive a reload, or a resumed removal would re-apply
// cleanup's evidence gate to a task that has no evidence and refuse forever.
func TestStore_ADiscardRecordStaysADiscardAcrossAReload(t *testing.T) {
	store, task := settledTask(t, "task-discard-reload")
	at := task.UpdatedAt.Add(time.Minute).UTC()
	if _, err := store.BeginTaskDiscard(context.Background(),
		discardMutation(task.Handle, "operation-discard-0001", at)); err != nil {
		t.Fatalf("BeginTaskDiscard() error = %v", err)
	}

	reloaded, found, err := store.GetTaskCleanupRecord(context.Background(), task.Handle)
	if err != nil || !found {
		t.Fatalf("GetTaskCleanupRecord() = %+v, %t, %v", reloaded, found, err)
	}
	if !reloaded.Discard {
		t.Error("a reloaded discard must still be a discard")
	}
}

func TestStore_ARepeatedDiscardReplaysAndAnAlteredOneConflicts(t *testing.T) {
	store, task := settledTask(t, "task-discard-replay")
	at := task.UpdatedAt.Add(time.Minute).UTC()
	mutation := discardMutation(task.Handle, "operation-discard-0001", at)

	first, err := store.BeginTaskDiscard(context.Background(), mutation)
	if err != nil {
		t.Fatalf("BeginTaskDiscard() error = %v", err)
	}
	second, err := store.BeginTaskDiscard(context.Background(), mutation)
	if err != nil {
		t.Fatalf("BeginTaskDiscard(replay) error = %v", err)
	}
	if first.OperationID != second.OperationID || second.Stage != first.Stage {
		t.Errorf("replayed discard = %+v, want %+v", second, first)
	}

	altered := mutation
	altered.SubjectDigest = strings.Repeat("9", 64)
	if _, err := store.BeginTaskDiscard(context.Background(), altered); !errors.Is(err, application.ErrConflict) {
		t.Fatalf("BeginTaskDiscard(altered) error = %v, want a conflict", err)
	}
}

func TestStore_DiscardRefusesAForgedOrStaleMutation(t *testing.T) {
	store, task := settledTask(t, "task-discard-forged")
	at := task.UpdatedAt.Add(time.Minute).UTC()

	for name, mutate := range map[string]func(*application.TaskDiscardMutation){
		"forged operation": func(m *application.TaskDiscardMutation) { m.OperationID = "../../etc" },
		"forged task":      func(m *application.TaskDiscardMutation) { m.TaskHandle = "task discard" },
		"short digest":     func(m *application.TaskDiscardMutation) { m.SubjectDigest = "abc" },
		"local time": func(m *application.TaskDiscardMutation) {
			m.At = at.In(time.FixedZone("local", 3600))
		},
		"stale time": func(m *application.TaskDiscardMutation) {
			m.At = task.UpdatedAt.Add(-time.Hour).UTC()
		},
	} {
		mutation := discardMutation(task.Handle, "operation-discard-"+strings.ReplaceAll(name, " ", "-"), at)
		mutate(&mutation)
		if _, err := store.BeginTaskDiscard(context.Background(), mutation); err == nil {
			t.Errorf("%s: BeginTaskDiscard() error = nil, want a refusal", name)
		}
	}
}

func TestStore_DiscardRefusesAnAbsentCallerAndUnknownTask(t *testing.T) {
	store, _ := openReportFixture(t, filepath.Join(canonicalTempDir(t), "devcrew.db"))
	at := time.Date(2026, time.August, 9, 16, 0, 0, 0, time.UTC)

	if _, err := store.BeginTaskDiscard(nilPromotionContext(),
		discardMutation("task-0001", "operation-discard-0001", at)); err == nil {
		t.Error("BeginTaskDiscard(nil) error = nil")
	}
	if _, err := store.BeginTaskDiscard(context.Background(),
		discardMutation("task-absent-0001", "operation-discard-0002", at)); !errors.Is(err, application.ErrNotFound) {
		t.Errorf("BeginTaskDiscard(unknown) error = %v, want not-found", err)
	}
}

// A discard must report a storage failure, never succeed past one: a silent
// success would release host authority for a worktree the record no longer
// tracks, and the removal that follows would have nothing recording it happened.
func TestStore_DiscardReportsStorageFailuresInsteadOfSwallowingThem(t *testing.T) {
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
		"missing cleanup table": func(store *Store) {
			_, _ = store.db.Exec("ALTER TABLE task_cleanup_operations RENAME TO unavailable_cleanup_operations")
		},
	} {
		store, task := settledTask(t, "task-discard-broken")
		at := task.UpdatedAt.Add(time.Minute).UTC()
		breakStore(store)

		if _, err := store.BeginTaskDiscard(context.Background(),
			discardMutation(task.Handle, "operation-discard-0001", at)); err == nil {
			t.Errorf("%s: BeginTaskDiscard() error = nil, want a storage failure", name)
		}
		_ = store.Close()
	}
}

// Discarding a task that is already held for removal must not start a second
// removal of the same worktree under a different operation.
func TestStore_DiscardRefusesATaskAlreadyHeldForRemoval(t *testing.T) {
	store, task := settledTask(t, "task-discard-twice")
	at := task.UpdatedAt.Add(time.Minute).UTC()
	if _, err := store.BeginTaskDiscard(context.Background(),
		discardMutation(task.Handle, "operation-discard-0001", at)); err != nil {
		t.Fatalf("BeginTaskDiscard() error = %v", err)
	}

	_, err := store.BeginTaskDiscard(context.Background(),
		discardMutation(task.Handle, "operation-discard-0002", at.Add(time.Minute)))

	if err == nil {
		t.Fatal("a second discard of the same task error = nil, want a refusal")
	}
}
