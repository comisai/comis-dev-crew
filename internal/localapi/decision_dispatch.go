package localapi

import (
	"context"

	"github.com/comisai/comis-dev-crew/internal/application"
	"github.com/comisai/comis-dev-crew/internal/domain"
)

// dispatchDecisionAuthority serves the operator's two powers over a question a
// worker asked: answer it, or withdraw it. They are routed together because they
// share one authority and one availability failure, and neither belongs to the
// model facade.
func (handler *Handler) dispatchDecisionAuthority(ctx context.Context, request Request) (Outcome, bool) {
	switch request.Method {
	case MethodCancelDecision:
		var input CancelDecisionInput
		if err := decodeObject(request.Payload, &input); err != nil {
			return invalidPayload(request.OperationID, err), true
		}
		if handler.decisions == nil {
			return rejectedOutcome(request.OperationID, domain.ErrorUnavailable, true, "decision withdrawal is unavailable", "inspect service configuration", nil), true
		}
		result, err := handler.decisions.CancelDecision(ctx, application.CancelDecisionCommand{
			OperationID: request.OperationID, TaskHandle: input.TaskHandle, ExternalKey: input.ExternalKey,
		})
		return handler.taskMutationOutcome(request.OperationID, MethodCancelDecision, result, err), true
	case MethodRespondDecision:
		var input RespondDecisionInput
		if err := decodeObject(request.Payload, &input); err != nil {
			return invalidPayload(request.OperationID, err), true
		}
		if handler.decisions == nil {
			return rejectedOutcome(request.OperationID, domain.ErrorUnavailable, true, "decision answering is unavailable", "inspect service configuration", nil), true
		}
		result, err := handler.decisions.RespondDecision(ctx, application.RespondDecisionCommand{
			OperationID: request.OperationID, TaskHandle: input.TaskHandle,
			ExternalKey: input.ExternalKey, Response: input.Response,
		})
		return handler.taskMutationOutcome(request.OperationID, MethodRespondDecision, result, err), true
	default:
		return Outcome{}, false
	}
}
