package application

import (
	"context"

	"github.com/comisai/comis-dev-crew/internal/domain"
)

// commandRespondDecisionName is shared with the durable layer's replay index; a
// mismatch would make a repeated answer commit twice rather than replay.
const commandRespondDecisionName = "RespondDecision"

// RespondDecisionCommand answers one open question.
type RespondDecisionCommand struct {
	OperationID string `json:"operationId"`
	TaskHandle  string `json:"taskHandle"`
	ExternalKey string `json:"externalKey"`
	Response    string `json:"response"`
}

// DecisionResponseStore is the narrow durable port for answering.
type DecisionResponseStore interface {
	CommitDecisionResponse(context.Context, DecisionResponseMutation) (MutationResult, error)
}

// RespondDecision records the human's answer to a question a worker asked.
//
// It is the operator's half of §14.2 for the case the channel cannot serve: the
// console exists to keep the product usable without an agent or MCP connection,
// and a question that can only ever be answered through a chat route leaves a
// task wedged whenever that route is unavailable. The answer does not close the
// question — the worker still has to apply it — so this settles who has replied,
// never whether the work may proceed.
func (mutations *Mutations) RespondDecision(
	ctx context.Context,
	command RespondDecisionCommand,
) (MutationResult, error) {
	if err := domain.ValidateDecisionKey(command.ExternalKey); err != nil {
		return MutationResult{}, mutationValidationFailure("decision reference is invalid")
	}
	if err := domain.ValidateDecisionResponse(command.Response); err != nil {
		return MutationResult{}, mutationValidationFailure("decision answer is invalid")
	}
	return taskHandleMutation(
		mutations, ctx, command.OperationID, command.TaskHandle, commandRespondDecisionName, command,
		func(ctx context.Context, subjectDigest, taskHandle string) (MutationResult, error) {
			return mutations.store.CommitDecisionResponse(ctx, DecisionResponseMutation{
				TaskHandle: taskHandle, OperationID: command.OperationID,
				SubjectDigest: subjectDigest, ExternalKey: command.ExternalKey,
				Response: command.Response, At: mutations.clock(),
			})
		},
	)
}
