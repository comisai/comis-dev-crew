package sqlite

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/comisai/comis-dev-crew/internal/application"
	"github.com/comisai/comis-dev-crew/internal/domain"
)

func TestMutations_PrepareAndBindReplayAcrossRestartWithoutDuplicateTask(t *testing.T) {
	databasePath := filepath.Join(canonicalTempDir(t), "devcrew.db")
	clock := time.Date(2026, time.August, 9, 13, 0, 0, 0, time.UTC)
	store, err := Open(context.Background(), databasePath)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	mutations := sqliteMutations(t, store, &sequenceIDs{ids: []string{"task-0001"}}, clock)
	prepared, err := mutations.PrepareTask(context.Background(), sqlitePrepareCommand())
	if err != nil {
		t.Fatalf("PrepareTask() error = %v", err)
	}
	if prepared.Task.State != domain.TaskPrepared || prepared.Task.StateVersion != 1 || prepared.Operation.StateVersion != 1 {
		t.Fatalf("prepared result = %#v, want atomic state version 1", prepared)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	reopened, err := Open(context.Background(), databasePath)
	if err != nil {
		t.Fatalf("Open(restart) error = %v", err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	replayedMutations := sqliteMutations(t, reopened, &sequenceIDs{ids: []string{"task-unused"}}, clock.Add(time.Minute))
	replayed, err := replayedMutations.PrepareTask(context.Background(), sqlitePrepareCommand())
	if err != nil {
		t.Fatalf("PrepareTask(replay) error = %v", err)
	}
	if replayed.Task.Handle != prepared.Task.Handle || replayed.Operation != prepared.Operation {
		t.Fatalf("prepare replay = %#v, want original %#v", replayed, prepared)
	}
	altered := sqlitePrepareCommand()
	altered.AcceptanceCriteria = []string{"Altered acceptance must conflict."}
	if _, err := replayedMutations.PrepareTask(context.Background(), altered); !errors.Is(err, application.ErrConflict) {
		t.Fatalf("PrepareTask(altered replay) error = %v, want ErrConflict", err)
	}

	bound, err := replayedMutations.AcknowledgeBinding(context.Background(), application.AcknowledgeBindingCommand{
		OperationID: "op-bind-0001", TaskHandle: prepared.Task.Handle,
		ManagedRunID: "managed-run-0001", WorkspaceLeaseID: "workspace-lease-0001",
	})
	if err != nil {
		t.Fatalf("AcknowledgeBinding() error = %v", err)
	}
	if bound.Task.State != domain.TaskReady || bound.Task.StateVersion != 2 || bound.Operation.StateVersion != 2 {
		t.Fatalf("bound result = %#v, want atomic ready state version 2", bound)
	}
	boundReplay, err := replayedMutations.AcknowledgeBinding(context.Background(), application.AcknowledgeBindingCommand{
		OperationID: "op-bind-0001", TaskHandle: prepared.Task.Handle,
		ManagedRunID: "managed-run-0001", WorkspaceLeaseID: "workspace-lease-0001",
	})
	if err != nil || boundReplay.Task.StateVersion != 2 {
		t.Fatalf("binding replay = %#v, %v, want original version 2", boundReplay, err)
	}
	if _, err := replayedMutations.AcknowledgeBinding(context.Background(), application.AcknowledgeBindingCommand{
		OperationID: "op-bind-0001", TaskHandle: prepared.Task.Handle,
		ManagedRunID: "managed-run-altered", WorkspaceLeaseID: "workspace-lease-0001",
	}); !errors.Is(err, application.ErrConflict) {
		t.Fatalf("AcknowledgeBinding(altered replay) error = %v, want ErrConflict", err)
	}

	tasks, err := reopened.ListTasks(context.Background())
	if err != nil {
		t.Fatalf("ListTasks() error = %v", err)
	}
	if len(tasks) != 1 || tasks[0].ManagedRunID != "managed-run-0001" || tasks[0].WorkspaceLeaseID != "workspace-lease-0001" {
		t.Fatalf("durable tasks = %#v, want one exactly bound task", tasks)
	}
	started, err := replayedMutations.StartTask(context.Background(), application.StartTaskCommand{
		OperationID: "op-start-0001", TaskHandle: prepared.Task.Handle,
	})
	if err != nil {
		t.Fatalf("StartTask() error = %v", err)
	}
	if started.Task.State != domain.TaskLaunching || started.Task.StateVersion != 3 || started.Operation.StateVersion != 3 {
		t.Fatalf("started result = %#v, want atomic launching state version 3", started)
	}
	startReplay, err := replayedMutations.StartTask(context.Background(), application.StartTaskCommand{
		OperationID: "op-start-0001", TaskHandle: prepared.Task.Handle,
	})
	if err != nil || !reflect.DeepEqual(startReplay, started) {
		t.Fatalf("StartTask(replay) = %#v, %v, want %#v", startReplay, err, started)
	}
	if _, err := replayedMutations.StartTask(context.Background(), application.StartTaskCommand{
		OperationID: "op-start-0001", TaskHandle: "task-altered",
	}); !errors.Is(err, application.ErrConflict) {
		t.Fatalf("StartTask(altered replay) error = %v, want ErrConflict", err)
	}
}

func TestMutations_ConcurrentIdenticalPrepareCreatesOneLogicalTask(t *testing.T) {
	store, err := Open(context.Background(), filepath.Join(canonicalTempDir(t), "devcrew.db"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	ids := &sequenceIDs{ids: []string{"task-0001", "task-0002"}}
	mutations := sqliteMutations(t, store, ids, time.Date(2026, time.August, 9, 13, 0, 0, 0, time.UTC))
	start := make(chan struct{})
	results := make(chan application.MutationResult, 2)
	errorsFound := make(chan error, 2)
	for range 2 {
		go func() {
			<-start
			result, err := mutations.PrepareTask(context.Background(), sqlitePrepareCommand())
			results <- result
			errorsFound <- err
		}()
	}
	close(start)
	first := <-results
	second := <-results
	for range 2 {
		if err := <-errorsFound; err != nil {
			t.Fatalf("concurrent PrepareTask() error = %v", err)
		}
	}
	if first.Task.Handle != second.Task.Handle {
		t.Fatalf("concurrent task handles = %q/%q, want one logical task", first.Task.Handle, second.Task.Handle)
	}
	tasks, err := store.ListTasks(context.Background())
	if err != nil || len(tasks) != 1 {
		t.Fatalf("ListTasks() = %#v, %v, want one task", tasks, err)
	}
}

func TestMutationStore_DirectReplayAndInvalidMutationsFailClosed(t *testing.T) {
	store, err := Open(context.Background(), filepath.Join(canonicalTempDir(t), "devcrew.db"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	ctx := context.Background()
	preparedAt := time.Date(2026, time.August, 9, 13, 0, 0, 0, time.UTC)
	prepared := application.PreparedTaskMutation{
		Task: storeTask("task-direct-0001", 1), OperationID: "op-direct-prepare-0001",
		SubjectDigest: strings.Repeat("a", 64), At: preparedAt,
	}
	prepared.Task.CreatedAt = preparedAt
	prepared.Task.UpdatedAt = preparedAt

	if replay, found, err := store.ReplayMutation(ctx, "op-missing", commandPrepareTask, strings.Repeat("a", 64)); err != nil || found {
		t.Fatalf("ReplayMutation(missing) = %#v, %t, %v, want absent", replay, found, err)
	}
	created, err := store.CommitPreparedTask(ctx, prepared)
	if err != nil {
		t.Fatalf("CommitPreparedTask() error = %v", err)
	}
	directReplay, err := store.CommitPreparedTask(ctx, prepared)
	if err != nil || !reflect.DeepEqual(directReplay, created) {
		t.Fatalf("CommitPreparedTask(replay) = %#v, %v, want %#v", directReplay, err, created)
	}
	altered := prepared
	altered.SubjectDigest = strings.Repeat("b", 64)
	if _, err := store.CommitPreparedTask(ctx, altered); !errors.Is(err, application.ErrConflict) {
		t.Fatalf("CommitPreparedTask(altered replay) error = %v, want ErrConflict", err)
	}
	duplicateTask := prepared
	duplicateTask.OperationID = "op-direct-prepare-0002"
	duplicateTask.SubjectDigest = strings.Repeat("c", 64)
	if _, err := store.CommitPreparedTask(ctx, duplicateTask); !errors.Is(err, application.ErrConflict) {
		t.Fatalf("CommitPreparedTask(duplicate task) error = %v, want ErrConflict", err)
	}
	invalidPrepared := prepared
	invalidPrepared.Task.State = domain.TaskReady
	if _, err := store.CommitPreparedTask(ctx, invalidPrepared); err == nil {
		t.Fatal("CommitPreparedTask(invalid) error = nil")
	}
	invalidOutcome := prepared
	invalidOutcome.Task.Handle = "task-invalid-outcome"
	invalidOutcome.OperationID = "op-invalid-outcome"
	invalidOutcome.SubjectDigest = strings.Repeat("f", 64)
	invalidOutcome.At = time.Time{}
	if _, err := store.CommitPreparedTask(ctx, invalidOutcome); err == nil {
		t.Fatal("CommitPreparedTask(invalid outcome time) error = nil")
	}
	if _, err := store.GetTask(ctx, invalidOutcome.Task.Handle); !errors.Is(err, application.ErrNotFound) {
		t.Fatalf("GetTask(rolled back invalid outcome) error = %v, want ErrNotFound", err)
	}

	missingBinding := application.TaskBindingMutation{
		TaskHandle: "task-missing", Binding: domain.TaskBinding{ManagedRunID: "managed-run-0001", WorkspaceLeaseID: "workspace-lease-0001"},
		OperationID: "op-bind-missing", SubjectDigest: strings.Repeat("d", 64), At: preparedAt.Add(time.Minute),
	}
	if _, err := store.CommitTaskBinding(ctx, missingBinding); !errors.Is(err, application.ErrNotFound) {
		t.Fatalf("CommitTaskBinding(missing) error = %v, want ErrNotFound", err)
	}
	invalidBinding := missingBinding
	invalidBinding.TaskHandle = prepared.Task.Handle
	invalidBinding.OperationID = "op-bind-invalid"
	invalidBinding.Binding.WorkspaceLeaseID = ""
	if _, err := store.CommitTaskBinding(ctx, invalidBinding); err == nil {
		t.Fatal("CommitTaskBinding(invalid) error = nil")
	}
	binding := missingBinding
	binding.TaskHandle = prepared.Task.Handle
	binding.OperationID = "op-direct-bind-0001"
	bound, err := store.CommitTaskBinding(ctx, binding)
	if err != nil {
		t.Fatalf("CommitTaskBinding() error = %v", err)
	}
	boundReplay, err := store.CommitTaskBinding(ctx, binding)
	if err != nil || !reflect.DeepEqual(boundReplay, bound) {
		t.Fatalf("CommitTaskBinding(replay) = %#v, %v, want %#v", boundReplay, err, bound)
	}
	alteredBinding := binding
	alteredBinding.SubjectDigest = strings.Repeat("e", 64)
	if _, err := store.CommitTaskBinding(ctx, alteredBinding); !errors.Is(err, application.ErrConflict) {
		t.Fatalf("CommitTaskBinding(altered replay) error = %v, want ErrConflict", err)
	}
}

func TestMutationStore_RejectsIncompleteCorruptAndMissingReplayOutcomes(t *testing.T) {
	store, err := Open(context.Background(), filepath.Join(canonicalTempDir(t), "devcrew.db"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	ctx := context.Background()
	now := time.Date(2026, time.August, 9, 13, 0, 0, 0, time.UTC)
	incomplete := storeOperation("op-incomplete", 1)
	incomplete.Command = commandPrepareTask
	incomplete.SubjectDigest = strings.Repeat("a", 64)
	incomplete.CreatedAt, incomplete.UpdatedAt = now, now
	if err := store.RecordOperation(ctx, incomplete); err != nil {
		t.Fatalf("RecordOperation(incomplete) error = %v", err)
	}
	if _, _, err := store.ReplayMutation(ctx, incomplete.ID, incomplete.Command, incomplete.SubjectDigest); err == nil {
		t.Fatal("ReplayMutation(incomplete) error = nil")
	}

	missingTask := storeOperation("op-missing-task", 2)
	missingTask.Command = commandPrepareTask
	missingTask.SubjectDigest = strings.Repeat("b", 64)
	missingTask.ResultRef = "task-does-not-exist"
	missingTask.CreatedAt, missingTask.UpdatedAt = now, now
	if err := store.RecordOperation(ctx, missingTask); err != nil {
		t.Fatalf("RecordOperation(missing task) error = %v", err)
	}
	if _, _, err := store.ReplayMutation(ctx, missingTask.ID, missingTask.Command, missingTask.SubjectDigest); err == nil {
		t.Fatal("ReplayMutation(missing task) error = nil")
	}
	if _, _, err := store.ReplayMutation(ctx, missingTask.ID, missingTask.Command, strings.Repeat("c", 64)); !errors.Is(err, application.ErrConflict) {
		t.Fatalf("ReplayMutation(altered) error = %v, want ErrConflict", err)
	}

	const corruptInsert = `INSERT INTO operations (
        id, schema_version, command, subject_digest, status, error_code,
        result_ref, state_version, created_at, updated_at
    ) VALUES (?, 1, ?, ?, 'corrupt', '', '', 3, ?, ?)`
	if _, err := store.db.ExecContext(ctx, corruptInsert, "op-corrupt", commandPrepareTask, strings.Repeat("d", 64), formatTime(now), formatTime(now)); err != nil {
		t.Fatalf("insert corrupt operation: %v", err)
	}
	if _, _, err := store.ReplayMutation(ctx, "op-corrupt", commandPrepareTask, strings.Repeat("d", 64)); err == nil {
		t.Fatal("ReplayMutation(corrupt) error = nil")
	}
}

func TestMutationStore_RejectsExhaustedVersionAndClosedDatabase(t *testing.T) {
	store, err := Open(context.Background(), filepath.Join(canonicalTempDir(t), "devcrew.db"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	now := time.Date(2026, time.August, 9, 13, 0, 0, 0, time.UTC)
	exhausted := storeOperation("op-exhausted", int64(^uint64(0)>>1))
	exhausted.CreatedAt, exhausted.UpdatedAt = now, now
	if err := store.RecordOperation(context.Background(), exhausted); err != nil {
		t.Fatalf("RecordOperation(exhausted) error = %v", err)
	}
	prepared := application.PreparedTaskMutation{
		Task: storeTask("task-exhausted", 1), OperationID: "op-after-exhaustion",
		SubjectDigest: strings.Repeat("a", 64), At: now,
	}
	prepared.Task.CreatedAt, prepared.Task.UpdatedAt = now, now
	if _, err := store.CommitPreparedTask(context.Background(), prepared); err == nil {
		t.Fatal("CommitPreparedTask(exhausted) error = nil")
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if _, _, err := store.ReplayMutation(context.Background(), prepared.OperationID, commandPrepareTask, prepared.SubjectDigest); err == nil {
		t.Fatal("ReplayMutation(closed) error = nil")
	}
	if _, err := store.CommitPreparedTask(context.Background(), prepared); err == nil {
		t.Fatal("CommitPreparedTask(closed) error = nil")
	}
	binding := application.TaskBindingMutation{
		TaskHandle: prepared.Task.Handle, Binding: domain.TaskBinding{ManagedRunID: "managed-run-0001", WorkspaceLeaseID: "workspace-lease-0001"},
		OperationID: "op-bind-closed", SubjectDigest: strings.Repeat("b", 64), At: now,
	}
	if _, err := store.CommitTaskBinding(context.Background(), binding); err == nil {
		t.Fatal("CommitTaskBinding(closed) error = nil")
	}
}

type configuredCatalog struct{}

func (configuredCatalog) ValidateRepository(context.Context, string) error { return nil }

type sequenceIDs struct {
	mu  sync.Mutex
	ids []string
}

func (source *sequenceIDs) next() (string, error) {
	source.mu.Lock()
	defer source.mu.Unlock()
	if len(source.ids) == 0 {
		return "", errors.New("no fixture task IDs remain")
	}
	id := source.ids[0]
	source.ids = source.ids[1:]
	return id, nil
}

func sqliteMutations(t *testing.T, store *Store, ids *sequenceIDs, now time.Time) *application.Mutations {
	t.Helper()
	mutations, err := application.NewMutations(application.MutationConfig{
		Store: store, Repositories: configuredCatalog{}, TaskIDs: ids.next, Clock: func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("NewMutations() error = %v", err)
	}
	return mutations
}

func sqlitePrepareCommand() application.PrepareTaskCommand {
	return application.PrepareTaskCommand{
		OperationID: "op-prepare-0001", ServiceInstanceID: "service-instance-0001",
		Shape: domain.ShapeShip, RepositoryID: "product-api", BaseRevision: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		AcceptanceCriteria: []string{"The requested behavior is proven."}, Constraints: []string{"Preserve unrelated changes."},
		ValidationProfile: "go-default", DeliveryMode: domain.DeliveryPullRequest, WorkerProfileID: "fixture-worker",
	}
}
