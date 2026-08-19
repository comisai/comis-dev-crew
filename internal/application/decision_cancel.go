package application

import (
	"context"

	"github.com/comisai/comis-dev-crew/internal/domain"
)

// commandCancelDecisionName is shared with the durable layer's replay index; a
// mismatch would make a repeated cancellation commit twice rather than replay.
const commandCancelDecisionName = "CancelDecision"

// CancelDecisionCommand withdraws one open question.
type CancelDecisionCommand struct {
	OperationID string `json:"operationId"`
	TaskHandle  string `json:"taskHandle"`
	ExternalKey string `json:"externalKey"`
}

// DecisionCancellationStore is the narrow durable port for withdrawal.
type DecisionCancellationStore interface {
	CommitDecisionCancellation(context.Context, DecisionCancellationMutation) (MutationResult, error)
}

// CancelDecision withdraws a question the human no longer wants answered.
//
// It is the operator's half of §14.2: a decision stays open until its worker
// resolves it or the human cancels it, so without this a question asked in error
// blocks completion and cleanup forever with no way to close it. It answers
// nothing on the worker's behalf — the worker simply stops being asked.
func (mutations *Mutations) CancelDecision(
	ctx context.Context,
	command CancelDecisionCommand,
) (MutationResult, error) {
	if err := domain.ValidateDecisionKey(command.ExternalKey); err != nil {
		return MutationResult{}, mutationValidationFailure("decision reference is invalid")
	}
	return taskHandleMutation(
		mutations, ctx, command.OperationID, command.TaskHandle, commandCancelDecisionName, command,
		func(ctx context.Context, subjectDigest, taskHandle string) (MutationResult, error) {
			return mutations.store.CommitDecisionCancellation(ctx, DecisionCancellationMutation{
				TaskHandle: taskHandle, OperationID: command.OperationID,
				SubjectDigest: subjectDigest, ExternalKey: command.ExternalKey,
				At: mutations.clock(),
			})
		},
	)
}
