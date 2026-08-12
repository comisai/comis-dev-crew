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
	devgit "github.com/comisai/comis-dev-crew/internal/git"
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
		ExecutionAttachmentID: "execution-attachment-fixture",
		AttachmentTargetName:  "attachment-bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb.sock",
	}); err != nil {
		t.Fatal(err)
	}
	preparer := &fixtureCandidatePreparerStub{}
	supervisor, err := newFixtureSupervisor(fixtureSupervisorConfig{
		Store: store, Mutations: mutations, Clock: func() time.Time { return now },
		Decision: "use the bounded fixture choice", PollInterval: time.Millisecond,
		CandidatePreparer: preparer, ArtifactRelativePath: "report.md",
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
	if len(preparer.requests) != 1 || preparer.requests[0].TaskHandle != prepared.Task.Handle ||
		preparer.requests[0].RepositoryID != prepared.Task.RepositoryID ||
		preparer.requests[0].WorktreePath != "/private/worktrees/managed-run-fixture" ||
		preparer.requests[0].BaseRevision != prepared.Task.BaseRevision ||
		preparer.requests[0].ArtifactRelativePath != "report.md" {
		t.Fatalf("fixture candidate preparation = %#v", preparer.requests)
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
		CandidatePreparer: &fixtureCandidatePreparerStub{}, ArtifactRelativePath: "report.md",
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

func TestFixtureSupervisor_IdleCancellationAndTaskSelectionAreDeterministic(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	idle := &fixtureSupervisor{
		store: fixtureStoreFunc{list: func(context.Context) ([]domain.Task, error) {
			cancel()
			return nil, nil
		}},
		pollInterval: time.Hour,
	}
	if err := idle.Run(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("Run(idle cancellation) error = %v", err)
	}

	prepared := serviceTask()
	wrongProfile := prepared
	wrongProfile.State = domain.TaskReady
	wrongProfile.WorkerProfileID = "codex-reviewed"
	none := &fixtureSupervisor{store: fixtureStoreFunc{list: func(context.Context) ([]domain.Task, error) {
		return []domain.Task{prepared, wrongProfile}, nil
	}}}
	if ran, err := none.runNext(context.Background()); err != nil || ran {
		t.Fatalf("runNext(ineligible tasks) = %v, %v", ran, err)
	}

	ready := wrongProfile
	ready.WorkerProfileID = "fixture-worker"
	privateFailure := errors.New("private start failure")
	failing := &fixtureSupervisor{
		store: fixtureStoreFunc{list: func(context.Context) ([]domain.Task, error) {
			return []domain.Task{ready}, nil
		}},
		candidatePreparer: &fixtureCandidatePreparerStub{}, artifactRelativePath: "report.md",
		mutations: fixtureStarterFunc(func(context.Context, application.StartTaskCommand) (application.MutationResult, error) {
			return application.MutationResult{}, privateFailure
		}),
	}
	if ran, err := failing.runNext(context.Background()); ran || !errors.Is(err, privateFailure) {
		t.Fatalf("runNext(start failure) = %v, %v", ran, err)
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
	preparationFailure := &fixtureSupervisor{
		store: failingFixtureStore{err: privateFailure},
	}
	if err := preparationFailure.runFixture(context.Background(), ready); !errors.Is(err, privateFailure) {
		t.Fatalf("runFixture(preparation failure) error = %v", err)
	}
	preparerFailure := &fixtureSupervisor{
		store: failingFixtureStore{}, artifactRelativePath: "report.md",
		candidatePreparer: fixtureCandidatePreparerFunc(func(context.Context, devgit.FixtureCandidateRequest) (devgit.CandidateSnapshot, error) {
			return devgit.CandidateSnapshot{}, privateFailure
		}),
	}
	if err := preparerFailure.runFixture(context.Background(), ready); !errors.Is(err, privateFailure) {
		t.Fatalf("runFixture(candidate preparation failure) error = %v", err)
	}
	preparerFailure.candidatePreparer = fixtureCandidatePreparerFunc(func(context.Context, devgit.FixtureCandidateRequest) (devgit.CandidateSnapshot, error) {
		return devgit.CandidateSnapshot{}, nil
	})
	if err := preparerFailure.runFixture(context.Background(), ready); err == nil {
		t.Fatal("runFixture(incomplete candidate) error = nil")
	}

	startFailure := &fixtureSupervisor{
		store: failingFixtureStore{}, candidatePreparer: &fixtureCandidatePreparerStub{}, artifactRelativePath: "report.md",
		mutations: fixtureStarterFunc(func(context.Context, application.StartTaskCommand) (application.MutationResult, error) {
			return application.MutationResult{}, privateFailure
		}),
	}
	if err := startFailure.runFixture(context.Background(), ready); !errors.Is(err, privateFailure) {
		t.Fatalf("runFixture(start failure) error = %v", err)
	}

	renderFailure := &fixtureSupervisor{
		store: failingFixtureStore{}, candidatePreparer: &fixtureCandidatePreparerStub{}, artifactRelativePath: "report.md",
		mutations: fixtureStarterFunc(func(context.Context, application.StartTaskCommand) (application.MutationResult, error) {
			return application.MutationResult{Task: domain.Task{}}, nil
		}),
	}
	if err := renderFailure.runFixture(context.Background(), ready); err == nil {
		t.Fatal("runFixture(stale brief) error = nil")
	}

	credentialFailure := &fixtureSupervisor{
		store: store, clock: clock, candidatePreparer: &fixtureCandidatePreparerStub{}, artifactRelativePath: "report.md",
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

func (store failingFixtureStore) GetManagedRunPreparation(context.Context, string) (application.ManagedRunPreparation, error) {
	return application.ManagedRunPreparation{RequestedWorkspaceRoot: "/private/worktrees/task-fixture"}, store.err
}

func (failingFixtureStore) CommitReport(context.Context, application.ReportMutation) (domain.ReportReceipt, error) {
	return domain.ReportReceipt{}, nil
}

type fixtureStoreFunc struct {
	list func(context.Context) ([]domain.Task, error)
}

func (store fixtureStoreFunc) ListTasks(ctx context.Context) ([]domain.Task, error) {
	return store.list(ctx)
}

func (fixtureStoreFunc) CommitReport(context.Context, application.ReportMutation) (domain.ReportReceipt, error) {
	return domain.ReportReceipt{}, nil
}

func (fixtureStoreFunc) GetManagedRunPreparation(context.Context, string) (application.ManagedRunPreparation, error) {
	return application.ManagedRunPreparation{}, nil
}

type fixtureCandidatePreparerStub struct {
	requests []devgit.FixtureCandidateRequest
	err      error
}

type fixtureCandidatePreparerFunc func(context.Context, devgit.FixtureCandidateRequest) (devgit.CandidateSnapshot, error)

func (prepare fixtureCandidatePreparerFunc) PrepareFixtureCandidate(
	ctx context.Context,
	request devgit.FixtureCandidateRequest,
) (devgit.CandidateSnapshot, error) {
	return prepare(ctx, request)
}

func (preparer *fixtureCandidatePreparerStub) PrepareFixtureCandidate(
	ctx context.Context,
	request devgit.FixtureCandidateRequest,
) (devgit.CandidateSnapshot, error) {
	if err := ctx.Err(); err != nil {
		return devgit.CandidateSnapshot{}, err
	}
	preparer.requests = append(preparer.requests, request)
	return devgit.CandidateSnapshot{
		RepositoryID: request.RepositoryID, WorktreePath: request.WorktreePath,
		HeadRevision: strings.Repeat("b", 40), Cleanliness: devgit.CandidateClean,
	}, preparer.err
}

type fixtureStarterStub struct{}

func (fixtureStarterStub) StartTask(context.Context, application.StartTaskCommand) (application.MutationResult, error) {
	return application.MutationResult{}, nil
}

type fixtureStarterFunc func(context.Context, application.StartTaskCommand) (application.MutationResult, error)

func (start fixtureStarterFunc) StartTask(ctx context.Context, command application.StartTaskCommand) (application.MutationResult, error) {
	return start(ctx, command)
}
