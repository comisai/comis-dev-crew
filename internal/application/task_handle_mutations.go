package application

import (
	"context"

	"github.com/comisai/comis-dev-crew/internal/domain"
)

// taskHandleMutation is the shape every by-handle task command shares: validate
// the operation and the handle, digest the subject, replay if the operation has
// already run, otherwise commit.
//
// Each command carried its own copy of that sequence — five identical failure
// returns whose only difference was the command name. Duplicating them made
// every new command dilute this package's coverage with branches that assert
// nothing new about the command they belong to, so the skeleton is written once.
func taskHandleMutation[Command any](
	mutations *Mutations,
	ctx context.Context,
	operationID string,
	taskHandle string,
	commandName string,
	subject Command,
	commit func(context.Context, string, string) (MutationResult, error),
) (MutationResult, error) {
	if err := validMutationContext(ctx); err != nil {
		return MutationResult{}, err
	}
	if err := domain.ValidateOperationID(operationID); err != nil {
		return MutationResult{}, mutationValidationFailure("operation ID is invalid")
	}
	if err := domain.ValidateTaskHandle(taskHandle); err != nil {
		return MutationResult{}, mutationValidationFailure("task handle is invalid")
	}
	subjectDigest, err := digestMutationSubject(subject)
	if err != nil {
		return MutationResult{}, mutationValidationFailure("mutation subject cannot be encoded")
	}
	if replay, found, err := mutations.store.ReplayMutation(
		ctx, operationID, commandName, subjectDigest,
	); err != nil {
		return MutationResult{}, mutationReplayFailure(err)
	} else if found {
		return replay, nil
	}
	return commit(ctx, subjectDigest, taskHandle)
}

// PauseTask records a request for one task's worker to reach a safe boundary.
//
// The request does not move the task. A worker holding the worktree does not
// stop because the host wrote a state, and a task that claimed "paused" while
// its worker kept committing would hand a developer a worktree changing under
// their editor. The worker reads the request through its own report receipt and
// answers with a paused report; that report is what settles the state.
func (mutations *Mutations) PauseTask(
	ctx context.Context,
	command PauseTaskCommand,
) (MutationResult, error) {
	return taskHandleMutation(
		mutations, ctx, command.OperationID, command.TaskHandle, commandPauseTaskName, command,
		func(ctx context.Context, subjectDigest, taskHandle string) (MutationResult, error) {
			return mutations.store.CommitTaskPauseRequest(ctx, TaskPauseRequestMutation{
				TaskHandle: taskHandle, OperationID: command.OperationID,
				SubjectDigest: subjectDigest, At: mutations.clock(),
			})
		},
	)
}

// CancelTask stops one task at an operator's request, preserving its artifacts.
//
// Stopping and discarding are separate decisions: the worktree, artifacts, run
// binding and lease all survive, and removing them is cleanup's own
// evidence-gated command. A cancel that also removed them would make the stop
// irreversible at the moment an operator is least certain.
func (mutations *Mutations) CancelTask(
	ctx context.Context,
	command CancelTaskCommand,
) (MutationResult, error) {
	return taskHandleMutation(
		mutations, ctx, command.OperationID, command.TaskHandle, commandCancelTaskName, command,
		func(ctx context.Context, subjectDigest, taskHandle string) (MutationResult, error) {
			return mutations.store.CommitTaskCancel(ctx, TaskCancelMutation{
				TaskHandle: taskHandle, OperationID: command.OperationID,
				SubjectDigest: subjectDigest, At: mutations.clock(),
			})
		},
	)
}

// The command names are shared with the durable layer's replay index; a mismatch
// would make a repeated command commit twice rather than replay.
const (
	commandPauseTaskName  = "PauseTask"
	commandCancelTaskName = "CancelTask"
)
