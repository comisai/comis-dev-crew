package service

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"sync"

	"github.com/comisai/comis-dev-crew/internal/application"
	"github.com/comisai/comis-dev-crew/internal/domain"
)

type productionLaunchStore interface {
	ListTasks(context.Context) ([]domain.Task, error)
	GetManagedRunPreparation(context.Context, string) (application.ManagedRunPreparation, error)
}

type productionLaunchMutations interface {
	ActivateManagedRun(context.Context, application.ActivateManagedRunCommand) (application.MutationResult, error)
	AbandonManagedRun(context.Context, application.AbandonManagedRunCommand) (application.MutationResult, error)
	CancelManagedRun(context.Context, application.CancelManagedRunCommand) (application.MutationResult, error)
	StartTask(context.Context, application.StartTaskCommand) (application.MutationResult, error)
	ReconcileTerminalEvent(context.Context, application.RecordTerminalEventCommand) (application.MutationResult, bool, error)
	RecordTerminalEvent(context.Context, application.RecordTerminalEventCommand) (application.MutationResult, error)
}

type productionLaunchSupervisorConfig struct {
	Store     productionLaunchStore
	Mutations productionLaunchMutations
	Harnesses application.WorkerHarnessResolver
}

// productionLaunchSupervisor is the installed service's sole coordinator for
// turning authenticated terminal creation into durable launch intent.
type productionLaunchSupervisor struct {
	store     productionLaunchStore
	mutations productionLaunchMutations
	harnesses application.WorkerHarnessResolver
	terminal  sync.Mutex
}

func newProductionLaunchSupervisor(config productionLaunchSupervisorConfig) (*productionLaunchSupervisor, error) {
	if config.Store == nil || config.Mutations == nil || config.Harnesses == nil {
		return nil, errors.New("create production launch supervisor: store, mutations, and worker harnesses are required")
	}
	return &productionLaunchSupervisor{
		store: config.Store, mutations: config.Mutations, harnesses: config.Harnesses,
	}, nil
}

func (supervisor *productionLaunchSupervisor) ActivateManagedRun(
	ctx context.Context,
	command application.ActivateManagedRunCommand,
) (application.MutationResult, error) {
	return supervisor.mutations.ActivateManagedRun(ctx, command)
}

// Cancellation passes straight through: the supervisor coordinates worker
// launch, and stopping a run is a durable state decision that needs no launch
// side effect — the worker's terminal is reclaimed by the normal cleanup path.
func (supervisor *productionLaunchSupervisor) CancelManagedRun(
	ctx context.Context,
	command application.CancelManagedRunCommand,
) (application.MutationResult, error) {
	return supervisor.mutations.CancelManagedRun(ctx, command)
}

func (supervisor *productionLaunchSupervisor) AbandonManagedRun(
	ctx context.Context,
	command application.AbandonManagedRunCommand,
) (application.MutationResult, error) {
	return supervisor.mutations.AbandonManagedRun(ctx, command)
}

func (supervisor *productionLaunchSupervisor) RecordTerminalEvent(
	ctx context.Context,
	command application.RecordTerminalEventCommand,
) (application.MutationResult, error) {
	supervisor.terminal.Lock()
	defer supervisor.terminal.Unlock()

	if replay, found, err := supervisor.mutations.ReconcileTerminalEvent(ctx, command); err != nil {
		return application.MutationResult{}, err
	} else if found {
		return replay, nil
	}
	if command.Transition != application.TerminalCreated {
		return supervisor.mutations.RecordTerminalEvent(ctx, command)
	}
	task, ready, err := supervisor.readyTask(ctx, command.ManagedRunID, command.WorkspaceLeaseID)
	if err != nil {
		return application.MutationResult{}, err
	}
	if !ready {
		return supervisor.mutations.RecordTerminalEvent(ctx, command)
	}
	preparation, err := supervisor.store.GetManagedRunPreparation(ctx, task.Handle)
	if err != nil {
		return application.MutationResult{}, fmt.Errorf("production launch supervisor read preparation: %w", err)
	}
	descriptor, err := application.BuildWorkerLaunchDescriptor(ctx, task, preparation, supervisor.harnesses)
	if err != nil {
		return application.MutationResult{}, fmt.Errorf("production launch supervisor verify descriptor: %w", err)
	}
	acknowledgement := descriptor.ExpectedAcknowledgement
	if acknowledgement.TaskHandle != task.Handle || acknowledgement.ManagedRunID != command.ManagedRunID ||
		acknowledgement.WorkspaceLeaseID != command.WorkspaceLeaseID {
		return application.MutationResult{}, errors.New("production launch supervisor: terminal authority differs from descriptor")
	}
	operationID := productionStartOperationID(task.Handle)
	started, err := supervisor.mutations.StartTask(ctx, application.StartTaskCommand{
		OperationID: operationID, TaskHandle: task.Handle,
	})
	if err != nil {
		return application.MutationResult{}, fmt.Errorf("production launch supervisor record launch intent: %w", err)
	}
	if started.Operation.ID != operationID || started.Operation.Status != domain.OperationCompleted ||
		started.Task.Handle != task.Handle || started.Task.State != domain.TaskLaunching ||
		started.Task.ManagedRunID != command.ManagedRunID || started.Task.WorkspaceLeaseID != command.WorkspaceLeaseID {
		return application.MutationResult{}, errors.New("production launch supervisor: durable start result is incomplete")
	}
	return supervisor.mutations.RecordTerminalEvent(ctx, command)
}

func (supervisor *productionLaunchSupervisor) readyTask(
	ctx context.Context,
	managedRunID string,
	workspaceLeaseID string,
) (domain.Task, bool, error) {
	tasks, err := supervisor.store.ListTasks(ctx)
	if err != nil {
		return domain.Task{}, false, fmt.Errorf("production launch supervisor list tasks: %w", err)
	}
	var match domain.Task
	matches := 0
	for _, task := range tasks {
		if task.ManagedRunID == managedRunID && task.WorkspaceLeaseID == workspaceLeaseID {
			match = task
			matches++
		}
	}
	if matches > 1 {
		return domain.Task{}, false, errors.New("production launch supervisor: terminal authority is ambiguous")
	}
	return match, matches == 1 && match.State == domain.TaskReady, nil
}

func productionStartOperationID(taskHandle string) string {
	digest := sha256.Sum256([]byte("production-terminal-created\x00" + taskHandle))
	return fmt.Sprintf("terminal-start-%x", digest[:16])
}
