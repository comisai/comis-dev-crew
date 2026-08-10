package service

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/comisai/comis-dev-crew/internal/application"
	"github.com/comisai/comis-dev-crew/internal/domain"
	"github.com/comisai/comis-dev-crew/internal/store/sqlite"
)

func TestFixtureSupervisor_ConsumesPinnedReadyTaskExactlyOnce(t *testing.T) {
	ctx := context.Background()
	store, err := sqlite.Open(ctx, filepath.Join(shortTempDir(t), "state", "devcrew.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	now := time.Date(2026, time.August, 9, 12, 0, 0, 0, time.UTC)
	mutations, err := application.NewMutations(application.MutationConfig{
		Store: store, Repositories: serviceRepositoryCatalog{},
		Workspaces:         serviceWorkspacePreparer{root: "/private/worktrees/managed-run-fixture"},
		RuntimeAttachments: serviceRuntimeAttachments{},
		TaskIDs:            func(string) (string, error) { return "task-fixture-supervised", nil },
		RegistrationNonces: func() (string, error) { return "registration-nonce_supervised", nil },
		PreparationTTL:     time.Hour, Clock: func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	prepared, err := mutations.PrepareTask(ctx, application.PrepareTaskCommand{
		OperationID: "prepare-fixture-supervised", ServiceInstanceID: "service-instance-fixture",
		Shape: domain.ShapeScout, RepositoryID: "product-api", BaseRevision: strings.Repeat("a", 40),
		AcceptanceCriteria: []string{"Emit the deterministic fixture report sequence."},
		Constraints:        []string{"Stop at a validation candidate."}, ValidationProfile: "go-default",
		DeliveryMode: domain.DeliveryReport, WorkerProfileID: "fixture-worker",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := mutations.ActivateManagedRun(ctx, application.ActivateManagedRunCommand{
		OperationID: "activate-fixture-supervised", ServiceInstanceID: "service-instance-fixture",
		ManagedRunID: "managed-run-fixture", ExternalRunRef: prepared.Task.Handle,
		RegistrationNonce: prepared.Preparation.RegistrationNonce, WorkspaceLeaseID: "workspace-lease-fixture",
	}); err != nil {
		t.Fatal(err)
	}
	supervisor, err := newFixtureSupervisor(fixtureSupervisorConfig{
		Store: store, Mutations: mutations, Clock: func() time.Time { return now },
		Decision: "use the bounded fixture choice", PollInterval: time.Millisecond,
		NewCredential: func() (string, error) { return "fixture-reporter-credential-0123456789abcdef", nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	runContext, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- supervisor.Run(runContext) }()
	deadline := time.After(5 * time.Second)
	for {
		task, getErr := store.GetTask(ctx, prepared.Task.Handle)
		if getErr != nil {
			t.Fatal(getErr)
		}
		if task.State == domain.TaskValidating {
			if task.ReportCursor != 4 {
				t.Fatalf("fixture report cursor = %d, want 4", task.ReportCursor)
			}
			break
		}
		select {
		case <-deadline:
			t.Fatalf("fixture task state = %q, want validating", task.State)
		case <-time.After(time.Millisecond):
		}
	}
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("FixtureSupervisor.Run() error = %v, want context cancellation", err)
	}
	reports, err := store.ListAcceptedReports(ctx, prepared.Task.Handle)
	if err != nil || len(reports) != 4 {
		t.Fatalf("accepted reports = %d, %v, want exactly four", len(reports), err)
	}
	startID := fixtureStartOperationID(prepared.Task.Handle)
	operation, err := store.GetOperation(ctx, startID)
	if err != nil || operation.Status != domain.OperationCompleted {
		t.Fatalf("fixture start operation %q = %#v, %v", startID, operation, err)
	}
}

func TestFixtureSupervisor_RejectsInvalidLifecycleAndStoreFailure(t *testing.T) {
	if _, err := newFixtureSupervisor(fixtureSupervisorConfig{}); err == nil {
		t.Fatal("newFixtureSupervisor(empty) error = nil")
	}
	storeFailure := errors.New("private fixture store failure")
	invalid := fixtureSupervisorConfig{
		Store: failingFixtureStore{err: storeFailure}, Mutations: fixtureStarterStub{}, Clock: time.Now,
		PollInterval: time.Millisecond, NewCredential: func() (string, error) { return "unused", nil },
	}
	if _, err := newFixtureSupervisor(invalid); err == nil {
		t.Fatal("newFixtureSupervisor(no decision) error = nil")
	}
	supervisor, err := newFixtureSupervisor(fixtureSupervisorConfig{
		Store: failingFixtureStore{err: storeFailure}, Mutations: fixtureStarterStub{},
		Clock: time.Now, Decision: "bounded", PollInterval: time.Millisecond,
		NewCredential: func() (string, error) { return "fixture-reporter-credential-0123456789abcdef", nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	//lint:ignore SA1012 The boundary test proves nil contexts fail closed.
	if err := supervisor.Run(nil); err == nil {
		t.Fatal("FixtureSupervisor.Run(nil) error = nil")
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if err := supervisor.Run(cancelled); !errors.Is(err, context.Canceled) {
		t.Fatalf("FixtureSupervisor.Run(cancelled) error = %v", err)
	}
	if err := supervisor.Run(context.Background()); !errors.Is(err, storeFailure) {
		t.Fatalf("FixtureSupervisor.Run(store failure) error = %v", err)
	}
}

func TestFixtureSupervisor_FailsClosedAtWorkerCompositionBoundaries(t *testing.T) {
	privateFailure := errors.New("private fixture boundary failure")
	ready := serviceTask()
	ready.State = domain.TaskReady
	ready.WorkerProfileID = "fixture-worker"
	ready.ManagedRunID = "managed-run-fixture"
	ready.WorkspaceLeaseID = "workspace-lease-fixture"
	ready, err := ready.PinBriefRevision()
	if err != nil {
		t.Fatal(err)
	}
	started := ready
	started.State = domain.TaskLaunching
	clock := func() time.Time { return time.Date(2026, time.August, 9, 12, 0, 0, 0, time.UTC) }
	store := failingFixtureStore{}

	startFailure := &fixtureSupervisor{
		mutations: fixtureStarterFunc(func(context.Context, application.StartTaskCommand) (application.MutationResult, error) {
			return application.MutationResult{}, privateFailure
		}),
	}
	if err := startFailure.runFixture(context.Background(), ready); !errors.Is(err, privateFailure) {
		t.Fatalf("runFixture(start failure) error = %v", err)
	}

	renderFailure := &fixtureSupervisor{
		mutations: fixtureStarterFunc(func(context.Context, application.StartTaskCommand) (application.MutationResult, error) {
			return application.MutationResult{Task: domain.Task{}}, nil
		}),
	}
	if err := renderFailure.runFixture(context.Background(), ready); err == nil {
		t.Fatal("runFixture(stale brief) error = nil")
	}

	credentialFailure := &fixtureSupervisor{
		store: store, clock: clock,
		mutations: fixtureStarterFunc(func(context.Context, application.StartTaskCommand) (application.MutationResult, error) {
			return application.MutationResult{Task: started}, nil
		}),
		newCredential: func() (string, error) { return "", privateFailure },
	}
	if err := credentialFailure.runFixture(context.Background(), ready); !errors.Is(err, privateFailure) {
		t.Fatalf("runFixture(credential failure) error = %v", err)
	}

	credentialFailure.newCredential = func() (string, error) { return "short", nil }
	if err := credentialFailure.runFixture(context.Background(), ready); err == nil {
		t.Fatal("runFixture(invalid credential) error = nil")
	}

	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	credentialFailure.newCredential = func() (string, error) {
		return "fixture-reporter-credential-0123456789abcdef", nil
	}
	credentialFailure.decision = "use the bounded fixture choice"
	if err := credentialFailure.runFixture(cancelled, ready); !errors.Is(err, context.Canceled) {
		t.Fatalf("runFixture(cancelled worker) error = %v", err)
	}
}

type failingFixtureStore struct{ err error }

func (store failingFixtureStore) ListTasks(context.Context) ([]domain.Task, error) {
	return nil, store.err
}

func (failingFixtureStore) CommitReport(context.Context, application.ReportMutation) (domain.ReportReceipt, error) {
	return domain.ReportReceipt{}, nil
}

type fixtureStarterStub struct{}

func (fixtureStarterStub) StartTask(context.Context, application.StartTaskCommand) (application.MutationResult, error) {
	return application.MutationResult{}, nil
}

type fixtureStarterFunc func(context.Context, application.StartTaskCommand) (application.MutationResult, error)

func (start fixtureStarterFunc) StartTask(ctx context.Context, command application.StartTaskCommand) (application.MutationResult, error) {
	return start(ctx, command)
}
