package sqlite

import (
	"context"
	"errors"
	"path/filepath"
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
