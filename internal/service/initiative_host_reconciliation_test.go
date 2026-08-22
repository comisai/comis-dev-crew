package service

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/comisai/comis-dev-crew/internal/application"
	"github.com/comisai/comis-dev-crew/internal/domain"
	"github.com/comisai/comis-dev-crew/internal/localapi"
	"github.com/comisai/comis-dev-crew/internal/store/sqlite"
)

func (control *serviceComisControl) ReadInitiativeHostRollup(
	ctx context.Context,
	request application.InitiativeHostRollupRequest,
) (application.InitiativeHostRollup, error) {
	if control.rollupCalls != nil {
		select {
		case control.rollupCalls <- request:
		case <-ctx.Done():
			return application.InitiativeHostRollup{}, ctx.Err()
		}
	}
	if control.rollupGate != nil {
		select {
		case <-control.rollupGate:
		case <-ctx.Done():
			return application.InitiativeHostRollup{}, ctx.Err()
		}
	}
	return control.rollup, control.rollupErr
}

func TestRunRecoversExactHostInitiativeBeforeAdvertisingReadiness(t *testing.T) {
	root := shortTempDir(t)
	databasePath := filepath.Join(root, "state", "devcrew.db")
	socketPath := filepath.Join(root, "run", "devcrew.sock")
	serviceInstanceID := "service-instance-recovery"
	groupID := "managed-run-group-recovery"
	seed := seedServiceHostRecoveryInitiative(t, databasePath, serviceInstanceID, groupID)
	rollupGate := make(chan struct{})
	control := &serviceComisControl{
		rollup: application.InitiativeHostRollup{
			ManagedRunGroupID: groupID,
			MemberManagedRunIDs: []string{
				seed.tasks[1].ManagedRunID, seed.tasks[0].ManagedRunID,
			},
			StateCounts: application.InitiativeHostStateCounts{Active: 2},
			UpdatedAtMs: serviceForwarderClock().UnixMilli(),
		},
		rollupCalls: make(chan application.InitiativeHostRollupRequest, 1), rollupGate: rollupGate,
	}
	ready := make(chan struct{})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- Run(ctx, Config{
			DatabasePath: databasePath, SocketPath: socketPath,
			ServiceInstanceID: serviceInstanceID, Repositories: serviceRepositoryCatalog{},
			WorkerProfiles: func(string, domain.TaskShape) error { return nil },
			WorkerProfileCatalog: func() []application.WorkerProfileSummary {
				return []application.WorkerProfileSummary{{ProfileID: "codex-standard", ConcurrencyLimit: 2}}
			},
			ValidationProfiles: func(string, domain.TaskShape) error { return nil },
			Workspaces:         serviceWorkspacePreparer{root: "/approved/worktrees/task-host-recovery"},
			RuntimeAttachments: serviceRuntimeAttachments{},
			TaskIDs:            func(string) (string, error) { return "task-host-recovery-new", nil },
			RegistrationNonces: func() (string, error) {
				return "registration-nonce_host-recovery", nil
			},
			PreparationTTL: time.Hour, MaxConcurrentTasks: 2, MaxConcurrentTasksPerRepository: 2,
			ComisControl: control, Clock: serviceForwarderClock, Ready: func() { close(ready) },
		})
	}()
	select {
	case request := <-control.rollupCalls:
		if request.ManagedRunGroupID != groupID || request.OperationID == "" {
			t.Fatalf("host rollup request = %#v", request)
		}
	case err := <-done:
		t.Fatalf("Run() before host rollup error = %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("Run() did not attempt host initiative recovery")
	}
	select {
	case <-ready:
		t.Fatal("Run() advertised ready before host initiative recovery settled")
	case <-time.After(100 * time.Millisecond):
	}
	close(rollupGate)
	select {
	case <-ready:
	case err := <-done:
		t.Fatalf("Run() before ready error = %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("Run() did not advertise ready after exact host recovery")
	}
	client, err := localapi.NewClient(socketPath, time.Second)
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	detail, err := client.GetInitiative(context.Background(), "read-host-recovered-initiative", seed.initiative.Handle)
	if err != nil || detail.Initiative.State != domain.InitiativeActive {
		t.Fatalf("GetInitiative() = %#v, %v, want active before readiness", detail, err)
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("Run() cancellation error = %v", err)
	}
}

type serviceHostRecoverySeed struct {
	initiative domain.DevelopmentInitiative
	tasks      []domain.Task
}

func seedServiceHostRecoveryInitiative(
	t *testing.T,
	databasePath string,
	serviceInstanceID string,
	groupID string,
) serviceHostRecoverySeed {
	t.Helper()
	store, err := sqlite.Open(context.Background(), databasePath)
	if err != nil {
		t.Fatalf("open host recovery seed store: %v", err)
	}
	tasks := make([]domain.Task, 0, 2)
	for index, handle := range []string{"task-host-recovery-a", "task-host-recovery-b"} {
		task := serviceTask()
		task.Handle = handle
		task.ServiceInstanceID = serviceInstanceID
		task.ManagedRunID = "managed-run-" + handle
		task.WorkspaceLeaseID = "workspace-lease-" + handle
		task.State = domain.TaskReady
		task.StateVersion = int64(index + 1)
		pinned, pinErr := task.PinBriefRevision()
		if pinErr != nil {
			t.Fatalf("PinBriefRevision(%q) error = %v", handle, pinErr)
		}
		if err := store.CreateTask(context.Background(), pinned); err != nil {
			t.Fatalf("seed host recovery task %q: %v", handle, err)
		}
		tasks = append(tasks, pinned)
	}
	initiative := domain.DevelopmentInitiative{
		SchemaVersion: 1, Handle: "initiative-host-recovery", ManagedRunGroupID: groupID,
		State: domain.InitiativeActive,
		BaseRevisionSet: []domain.InitiativeBaseRevision{{
			RepositoryID: tasks[0].RepositoryID, Revision: tasks[0].BaseRevision,
		}},
		Components: []domain.InitiativeComponent{
			{ComponentHandle: "component-host-recovery-a", RepositoryID: tasks[0].RepositoryID, TaskHandles: []string{tasks[0].Handle}},
			{ComponentHandle: "component-host-recovery-b", RepositoryID: tasks[1].RepositoryID, TaskHandles: []string{tasks[1].Handle}},
		},
		IntegrationPolicyID: "integration-default", StateVersion: 3,
		CreatedAt: tasks[0].CreatedAt, UpdatedAt: tasks[0].UpdatedAt,
	}
	if err := store.CreateInitiative(context.Background(), initiative); err != nil {
		t.Fatalf("seed host recovery initiative: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("close host recovery seed store: %v", err)
	}
	return serviceHostRecoverySeed{initiative: initiative, tasks: tasks}
}
