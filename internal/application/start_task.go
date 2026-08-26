package application

import (
	"context"

	"github.com/comisai/comis-dev-crew/internal/domain"
)

// StartTask durably records launch intent before any worker can acknowledge
// its wrapper or begin work.
func (mutations *Mutations) StartTask(ctx context.Context, command StartTaskCommand) (MutationResult, error) {
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
		return MutationResult{}, mutationValidationFailure("start subject cannot be encoded")
	}
	if replay, found, err := mutations.store.ReplayMutation(ctx, command.OperationID, commandStartTask, subjectDigest); err != nil {
		return MutationResult{}, mutationReplayFailure(err)
	} else if found {
		return replay, nil
	}
	result, err := mutations.store.CommitTaskStart(ctx, TaskStartMutation{
		TaskHandle: command.TaskHandle, OperationID: command.OperationID,
		SubjectDigest: subjectDigest, At: mutations.clock(),
		SchedulingLimits: mutations.schedulingLimits,
	})
	return result, mutationCommitFailure(err)
}
