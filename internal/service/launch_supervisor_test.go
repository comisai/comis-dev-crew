package service

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/comisai/comis-dev-crew/internal/application"
	"github.com/comisai/comis-dev-crew/internal/domain"
)

func TestProductionLaunchSupervisor_StartsOnlyAfterVerifiedCreatedAuthority(t *testing.T) {
	task, preparation := productionLaunchFixture(t)
	store := &productionLaunchStoreStub{tasks: []domain.Task{task}, preparation: preparation}
	mutations := &productionLaunchMutationStub{task: task}
	adapter := &productionLaunchHarnessAdapter{}
	supervisor, err := newProductionLaunchSupervisor(productionLaunchSupervisorConfig{
		Store: store, Mutations: mutations, Harnesses: productionLaunchHarnesses{adapter: adapter},
	})
	if err != nil {
		t.Fatal(err)
	}
	command := productionCreatedCommand(task)
	result, err := supervisor.RecordTerminalEvent(context.Background(), command)
	if err != nil || result.Task.State != domain.TaskLaunching {
		t.Fatalf("RecordTerminalEvent(created) = %#v, %v", result, err)
	}
	wantStart := application.StartTaskCommand{
		OperationID: productionStartOperationID(task.Handle), TaskHandle: task.Handle,
	}
	if len(mutations.starts) != 1 || mutations.starts[0] != wantStart ||
		len(mutations.terminals) != 1 || mutations.terminals[0] != command {
		t.Fatalf("created calls = starts %#v, terminals %#v", mutations.starts, mutations.terminals)
	}
	if adapter.request.ManagedRunID != task.ManagedRunID || adapter.request.WorkspaceLeaseID != task.WorkspaceLeaseID ||
		adapter.request.TaskHandle != task.Handle ||
		adapter.request.Attachment.RelayIdentity != preparation.RequestedAttachment.RelayIdentity ||
		adapter.request.Attachment.MountSocketPath != "/run/comis/attachments/"+task.AttachmentTargetName {
		t.Fatalf("verified descriptor request = %#v", adapter.request)
	}
}

func TestProductionLaunchSupervisor_ReplayAndUnverifiedEvidenceHaveNoStartSideEffect(t *testing.T) {
	task, preparation := productionLaunchFixture(t)
	command := productionCreatedCommand(task)
	t.Run("durable replay", func(t *testing.T) {
		replayed := application.MutationResult{
			Task: task, Operation: domain.OperationRecord{ID: command.OperationID, Status: domain.OperationCompleted},
		}
		storeFailure := errors.New("store must not be consulted")
		mutations := &productionLaunchMutationStub{task: task, replay: replayed, replayFound: true}
		supervisor, err := newProductionLaunchSupervisor(productionLaunchSupervisorConfig{
			Store: &productionLaunchStoreStub{listErr: storeFailure}, Mutations: mutations,
			Harnesses: productionLaunchHarnesses{adapter: &productionLaunchHarnessAdapter{}},
		})
		if err != nil {
			t.Fatal(err)
		}
		result, err := supervisor.RecordTerminalEvent(context.Background(), command)
		if err != nil || !reflect.DeepEqual(result, replayed) || len(mutations.starts) != 0 || len(mutations.terminals) != 0 {
			t.Fatalf("RecordTerminalEvent(replay) = %#v, %v, starts %#v, terminals %#v", result, err, mutations.starts, mutations.terminals)
		}
	})

	t.Run("descriptor mismatch", func(t *testing.T) {
		mutations := &productionLaunchMutationStub{task: task}
		adapter := &productionLaunchHarnessAdapter{alterDescriptor: func(descriptor *application.WorkerLaunchDescriptor) {
			descriptor.ExpectedAcknowledgement.WorkspaceLeaseID = "workspace-lease-forged"
		}}
		supervisor, err := newProductionLaunchSupervisor(productionLaunchSupervisorConfig{
			Store: &productionLaunchStoreStub{tasks: []domain.Task{task}, preparation: preparation}, Mutations: mutations,
			Harnesses: productionLaunchHarnesses{adapter: adapter},
		})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := supervisor.RecordTerminalEvent(context.Background(), command); err == nil {
			t.Fatal("RecordTerminalEvent(unverified descriptor) error = nil")
		}
		if len(mutations.starts) != 0 || len(mutations.terminals) != 0 {
			t.Fatalf("unverified descriptor caused durable calls: %#v / %#v", mutations.starts, mutations.terminals)
		}
	})

	t.Run("ambiguous authority", func(t *testing.T) {
		duplicate := task
		duplicate.Handle = "task-production-launch-duplicate"
		duplicate, err := duplicate.PinBriefRevision()
		if err != nil {
			t.Fatal(err)
		}
		mutations := &productionLaunchMutationStub{task: task}
		supervisor, err := newProductionLaunchSupervisor(productionLaunchSupervisorConfig{
			Store: &productionLaunchStoreStub{tasks: []domain.Task{task, duplicate}}, Mutations: mutations,
			Harnesses: productionLaunchHarnesses{adapter: &productionLaunchHarnessAdapter{}},
		})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := supervisor.RecordTerminalEvent(context.Background(), command); err == nil {
			t.Fatal("RecordTerminalEvent(ambiguous authority) error = nil")
		}
		if len(mutations.starts) != 0 || len(mutations.terminals) != 0 {
			t.Fatalf("ambiguous authority caused durable calls: %#v / %#v", mutations.starts, mutations.terminals)
		}
	})
}

func TestProductionLaunchSupervisor_PassesNonCreatedEventsToDurableJoin(t *testing.T) {
	task, _ := productionLaunchFixture(t)
	mutations := &productionLaunchMutationStub{task: task}
	supervisor, err := newProductionLaunchSupervisor(productionLaunchSupervisorConfig{
		Store: &productionLaunchStoreStub{listErr: errors.New("unused")}, Mutations: mutations,
		Harnesses: productionLaunchHarnesses{adapter: &productionLaunchHarnessAdapter{}},
	})
	if err != nil {
		t.Fatal(err)
	}
	command := productionCreatedCommand(task)
	command.OperationID = "terminal-running-production"
	command.Transition = application.TerminalRunning
	if _, err := supervisor.RecordTerminalEvent(context.Background(), command); err != nil {
		t.Fatalf("RecordTerminalEvent(running) error = %v", err)
	}
	if len(mutations.starts) != 0 || len(mutations.terminals) != 1 || mutations.terminals[0] != command {
		t.Fatalf("running calls = starts %#v, terminals %#v", mutations.starts, mutations.terminals)
	}
	if _, err := supervisor.ActivateManagedRun(context.Background(), application.ActivateManagedRunCommand{}); err != nil {
		t.Fatalf("ActivateManagedRun() error = %v", err)
	}
	if _, err := supervisor.AbandonManagedRun(context.Background(), application.AbandonManagedRunCommand{}); err != nil {
		t.Fatalf("AbandonManagedRun() error = %v", err)
	}
	if _, err := newProductionLaunchSupervisor(productionLaunchSupervisorConfig{}); err == nil {
		t.Fatal("newProductionLaunchSupervisor(empty) error = nil")
	}
}

func TestProductionLaunchSupervisor_PropagatesClosedBoundaryFailures(t *testing.T) {
	task, preparation := productionLaunchFixture(t)
	command := productionCreatedCommand(task)
	tests := []struct {
		name      string
		store     *productionLaunchStoreStub
		mutations *productionLaunchMutationStub
	}{
		{
			name: "reconciliation", store: &productionLaunchStoreStub{},
			mutations: &productionLaunchMutationStub{task: task, replayErr: errors.New("reconciliation failed")},
		},
		{
			name: "task list", store: &productionLaunchStoreStub{listErr: errors.New("task list failed")},
			mutations: &productionLaunchMutationStub{task: task},
		},
		{
			name: "preparation", store: &productionLaunchStoreStub{tasks: []domain.Task{task}, prepErr: errors.New("preparation failed")},
			mutations: &productionLaunchMutationStub{task: task},
		},
		{
			name: "start", store: &productionLaunchStoreStub{tasks: []domain.Task{task}, preparation: preparation},
			mutations: &productionLaunchMutationStub{task: task, startErr: errors.New("start failed")},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			supervisor, err := newProductionLaunchSupervisor(productionLaunchSupervisorConfig{
				Store: test.store, Mutations: test.mutations,
				Harnesses: productionLaunchHarnesses{adapter: &productionLaunchHarnessAdapter{}},
			})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := supervisor.RecordTerminalEvent(context.Background(), command); err == nil {
				t.Fatalf("RecordTerminalEvent(%s failure) error = nil", test.name)
			}
		})
	}
}

func productionLaunchFixture(t *testing.T) (domain.Task, application.ManagedRunPreparation) {
	t.Helper()
	task := serviceTask()
	task.Handle = "task-production-launch"
	task.State = domain.TaskReady
	task.ManagedRunID = "managed-run-production-launch"
	task.WorkspaceLeaseID = "workspace-lease-production-launch"
	task.ExecutionAttachmentID = "execution-attachment-production-launch"
	task.AttachmentTargetName = "attachment-cccccccccccccccccccccccccccccccc.sock"
	task.WorkerProfileID = "codex-reviewed"
	task, err := task.PinBriefRevision()
	if err != nil {
		t.Fatal(err)
	}
	return task, application.ManagedRunPreparation{
		ExternalRunRef: task.Handle, RequestedWorkspaceRoot: shortTempDir(t),
		RequestedAttachment: application.PreparedRuntimeAttachment{RelayIdentity: strings.Repeat("ab", 32)},
		State:               application.PreparationOpen,
	}
}

func productionCreatedCommand(task domain.Task) application.RecordTerminalEventCommand {
	return application.RecordTerminalEventCommand{
		OperationID: "terminal-created-production", ManagedRunID: task.ManagedRunID,
		WorkspaceLeaseID: task.WorkspaceLeaseID, TerminalSessionID: "terminal-session-production",
		Transition: application.TerminalCreated,
	}
}

type productionLaunchStoreStub struct {
	tasks       []domain.Task
	preparation application.ManagedRunPreparation
	listErr     error
	prepErr     error
}

func (store *productionLaunchStoreStub) ListTasks(context.Context) ([]domain.Task, error) {
	return append([]domain.Task(nil), store.tasks...), store.listErr
}

func (store *productionLaunchStoreStub) GetManagedRunPreparation(context.Context, string) (application.ManagedRunPreparation, error) {
	return store.preparation, store.prepErr
}

type productionLaunchMutationStub struct {
	task        domain.Task
	replay      application.MutationResult
	replayFound bool
	replayErr   error
	startErr    error
	starts      []application.StartTaskCommand
	terminals   []application.RecordTerminalEventCommand
}

func (mutations *productionLaunchMutationStub) ActivateManagedRun(context.Context, application.ActivateManagedRunCommand) (application.MutationResult, error) {
	return application.MutationResult{}, nil
}

func (mutations *productionLaunchMutationStub) AbandonManagedRun(context.Context, application.AbandonManagedRunCommand) (application.MutationResult, error) {
	return application.MutationResult{}, nil
}

func (mutations *productionLaunchMutationStub) ReconcileTerminalEvent(context.Context, application.RecordTerminalEventCommand) (application.MutationResult, bool, error) {
	return mutations.replay, mutations.replayFound, mutations.replayErr
}

func (mutations *productionLaunchMutationStub) StartTask(_ context.Context, command application.StartTaskCommand) (application.MutationResult, error) {
	mutations.starts = append(mutations.starts, command)
	if mutations.startErr != nil {
		return application.MutationResult{}, mutations.startErr
	}
	started := mutations.task
	started.State = domain.TaskLaunching
	return application.MutationResult{
		Task: started, Operation: domain.OperationRecord{ID: command.OperationID, Status: domain.OperationCompleted},
	}, nil
}

func (mutations *productionLaunchMutationStub) RecordTerminalEvent(_ context.Context, command application.RecordTerminalEventCommand) (application.MutationResult, error) {
	mutations.terminals = append(mutations.terminals, command)
	started := mutations.task
	started.State = domain.TaskLaunching
	return application.MutationResult{Task: started}, nil
}

type productionLaunchHarnesses struct {
	adapter application.WorkerHarnessAdapter
}

func (harnesses productionLaunchHarnesses) ResolveWorkerHarness(string) (application.WorkerHarnessAdapter, error) {
	return harnesses.adapter, nil
}

type productionLaunchHarnessAdapter struct {
	request         application.WorkerLaunchRequest
	alterDescriptor func(*application.WorkerLaunchDescriptor)
}

func (*productionLaunchHarnessAdapter) ID() string { return "codex" }

func (*productionLaunchHarnessAdapter) ProbeVersion(context.Context) (application.HarnessVersionProbe, error) {
	return application.HarnessVersionProbe{Availability: application.HarnessAvailable}, nil
}

func (adapter *productionLaunchHarnessAdapter) BuildLaunchDescriptor(
	_ context.Context,
	request application.WorkerLaunchRequest,
) (application.WorkerLaunchDescriptor, error) {
	adapter.request = request
	descriptor := application.WorkerLaunchDescriptor{
		ProfileID: request.ProfileID, TerminalAllowEntry: "codex-confined", Attachment: request.Attachment,
		ExpectedAcknowledgement: application.LaunchAcknowledgement{
			TaskHandle: request.TaskHandle, ManagedRunID: request.ManagedRunID,
			WorkspaceLeaseID: request.WorkspaceLeaseID, WorkingDirectory: request.WorkingDirectory,
			BriefRevision: request.BriefRevision, BriefRevisionHash: request.BriefRevisionHash,
		},
	}
	if adapter.alterDescriptor != nil {
		adapter.alterDescriptor(&descriptor)
	}
	return descriptor, nil
}

func (*productionLaunchHarnessAdapter) ClassifySemanticActivity(application.HarnessObservation) application.SemanticActivityResult {
	return application.SemanticActivityResult{State: application.ActivityUnknown, Reason: application.SemanticReasonMissing}
}

// Launch supervision never reaches a running worker; the stub refuses so a
// supervisor that started to would fail loudly rather than plan a keystroke.
func (*productionLaunchHarnessAdapter) SendInput(
	context.Context, application.WorkerInputRequest,
) (application.WorkerInputPlan, error) {
	return application.WorkerInputPlan{}, errors.New("launch harness adapter does not deliver input")
}

func (*productionLaunchHarnessAdapter) RequestPause(
	context.Context, application.WorkerControlRequest,
) (application.WorkerInputPlan, error) {
	return application.WorkerInputPlan{}, errors.New("launch harness adapter does not deliver control")
}

func (*productionLaunchHarnessAdapter) RequestStop(
	context.Context, application.WorkerControlRequest,
) (application.WorkerInputPlan, error) {
	return application.WorkerInputPlan{}, errors.New("launch harness adapter does not deliver control")
}

func (*productionLaunchHarnessAdapter) ValidateProfile(
	context.Context, string, domain.TaskShape,
) error {
	return nil
}

func (*productionLaunchHarnessAdapter) Diagnose(context.Context) (application.HarnessDiagnosis, error) {
	return application.HarnessDiagnosis{}, errors.New("launch harness adapter does not diagnose")
}

// Unknown is the honest answer from a stub that observes nothing.
func (*productionLaunchHarnessAdapter) ClassifyProcessRole(
	application.TaskProcessObservation,
) application.ProcessRoleResult {
	return application.ProcessRoleResult{
		Role: application.ProcessRoleUnknown, Reason: application.ProcessRoleReasonUnattributed,
	}
}

// Cancellation joins the same durable mutation surface; the stub accepts it.
func (*productionLaunchMutationStub) CancelManagedRun(
	_ context.Context,
	_ application.CancelManagedRunCommand,
) (application.MutationResult, error) {
	return application.MutationResult{}, nil
}

func TestProductionLaunchSupervisor_CancellationPassesThroughWithoutALaunchSideEffect(t *testing.T) {
	// Stopping a run is a durable state decision. The supervisor exists to
	// coordinate worker launch, so it must not treat a cancel as a reason to
	// touch a terminal — the worker's terminal is reclaimed by cleanup.
	task, preparation := productionLaunchFixture(t)
	store := &productionLaunchStoreStub{tasks: []domain.Task{task}, preparation: preparation}
	mutations := &productionLaunchMutationStub{task: task}
	adapter := &productionLaunchHarnessAdapter{}
	supervisor, err := newProductionLaunchSupervisor(productionLaunchSupervisorConfig{
		Store: store, Mutations: mutations, Harnesses: productionLaunchHarnesses{adapter: adapter},
	})
	if err != nil {
		t.Fatal(err)
	}

	if _, err := supervisor.CancelManagedRun(context.Background(), application.CancelManagedRunCommand{
		OperationID: "operation-cancel", ServiceInstanceID: task.ServiceInstanceID,
		ManagedRunID: "managed-run-cancel", Reason: application.CancelReasonOwnerCancelled,
	}); err != nil {
		t.Fatalf("CancelManagedRun() error = %v", err)
	}
	if adapter.request.TaskHandle != "" {
		t.Errorf("cancellation built a launch request for %q", adapter.request.TaskHandle)
	}
}
