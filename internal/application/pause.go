package application

import (
	"context"

	"github.com/comisai/comis-dev-crew/internal/domain"
)

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
	if err := validMutationContext(ctx); err != nil {
		return MutationResult{}, err
	}
	if err := domain.ValidateOperationID(command.OperationID); err != nil {
		return MutationResult{}, mutationValidationFailure("operation ID is invalid")
	}
	if err := domain.ValidateTaskHandle(command.TaskHandle); err != nil {
		return MutationResult{}, mutationValidationFailure("task handle is invalid")
	}
	subjectDigest, err := digestMutationSubject(command)
	if err != nil {
		return MutationResult{}, mutationValidationFailure("pause subject cannot be encoded")
	}
	if replay, found, err := mutations.store.ReplayMutation(
		ctx, command.OperationID, commandPauseTaskName, subjectDigest,
	); err != nil {
		return MutationResult{}, mutationReplayFailure(err)
	} else if found {
		return replay, nil
	}
	return mutations.store.CommitTaskPauseRequest(ctx, TaskPauseRequestMutation{
		TaskHandle:    command.TaskHandle,
		OperationID:   command.OperationID,
		SubjectDigest: subjectDigest,
		At:            mutations.clock(),
	})
}

// The command name is shared with the durable layer's replay index; a mismatch
// would make a repeated pause commit twice rather than replay.
const commandPauseTaskName = "PauseTask"
