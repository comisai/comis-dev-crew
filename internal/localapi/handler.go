package localapi

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/comisai/comis-dev-crew/internal/application"
	"github.com/comisai/comis-dev-crew/internal/domain"
)

const unknownRequestID = "request-unknown"

// Handler authenticates, validates, and dispatches canonical local requests.
type Handler struct {
	queries ReadQueries
	clock   application.Clock
}

// NewHandler binds the canonical queries and an injected deadline clock.
func NewHandler(queries ReadQueries, clock application.Clock) (*Handler, error) {
	if queries == nil {
		return nil, errors.New("create local API handler: queries are required")
	}
	if clock == nil {
		return nil, errors.New("create local API handler: clock is required")
	}
	return &Handler{queries: queries, clock: clock}, nil
}

func (handler *Handler) handle(ctx context.Context, caller CallerClass, data []byte) Outcome {
	var request Request
	if err := decodeObject(data, &request); err != nil {
		return rejectedOutcome(unknownRequestID, domain.ErrorInvalidArgument, false, "invalid request envelope", "send one strict bounded request", err)
	}
	if request.ProtocolVersion != ProtocolVersion {
		return rejectedOutcome(request.OperationID, domain.ErrorInvalidArgument, false, "unsupported local protocol", "use the service protocol version", nil)
	}
	if err := domain.ValidateOperationID(request.OperationID); err != nil {
		return rejectedOutcome(unknownRequestID, domain.ErrorInvalidArgument, false, "invalid operation ID", "use a bounded opaque identifier", err)
	}
	if !request.Method.valid() {
		return rejectedOutcome(request.OperationID, domain.ErrorInvalidArgument, false, "unknown local API method", "use a method from the closed catalog", nil)
	}
	if !methodAllowed(caller, request.Method) {
		return rejectedOutcome(request.OperationID, domain.ErrorUnauthorized, false, "caller cannot use this method", "use the endpoint assigned to the caller class", nil)
	}
	if request.DeadlineAtMs != nil {
		deadline := time.UnixMilli(*request.DeadlineAtMs)
		if !deadline.After(handler.clock()) {
			return rejectedOutcome(request.OperationID, domain.ErrorDeadlineExceeded, true, "request deadline elapsed", "retry with a current bounded deadline", context.DeadlineExceeded)
		}
		var cancel context.CancelFunc
		ctx, cancel = context.WithDeadline(ctx, deadline)
		defer cancel()
	}
	return handler.dispatch(ctx, request)
}

func (handler *Handler) dispatch(ctx context.Context, request Request) Outcome {
	switch request.Method {
	case MethodDiagnose:
		if err := decodeObject(request.Payload, &emptyPayload{}); err != nil {
			return invalidPayload(request.OperationID, err)
		}
		result, err := handler.queries.Diagnose(ctx)
		return queryOutcome(request.OperationID, result.StateVersion, result, err)
	case MethodFleet:
		if err := decodeObject(request.Payload, &emptyPayload{}); err != nil {
			return invalidPayload(request.OperationID, err)
		}
		result, err := handler.queries.Fleet(ctx)
		return queryOutcome(request.OperationID, result.StateVersion, result, err)
	case MethodListTasks:
		if err := decodeObject(request.Payload, &emptyPayload{}); err != nil {
			return invalidPayload(request.OperationID, err)
		}
		result, err := handler.queries.ListTasks(ctx)
		return queryOutcome(request.OperationID, result.StateVersion, result, err)
	case MethodShowTask:
		var payload taskPayload
		if err := decodeObject(request.Payload, &payload); err != nil {
			return invalidPayload(request.OperationID, err)
		}
		result, err := handler.queries.ShowTask(ctx, payload.TaskHandle)
		return queryOutcome(request.OperationID, result.StateVersion, result, err)
	case MethodExplainTask:
		var payload taskPayload
		if err := decodeObject(request.Payload, &payload); err != nil {
			return invalidPayload(request.OperationID, err)
		}
		result, err := handler.queries.ExplainTask(ctx, payload.TaskHandle)
		return queryOutcome(request.OperationID, result.Summary.StateVersion, result, err)
	case MethodOperation:
		var payload operationPayload
		if err := decodeObject(request.Payload, &payload); err != nil {
			return invalidPayload(request.OperationID, err)
		}
		result, err := handler.queries.Operation(ctx, payload.OperationID)
		return queryOutcome(request.OperationID, result.StateVersion, result, err)
	default:
		return rejectedOutcome(request.OperationID, domain.ErrorInvalidArgument, false, "unknown local API method", "use a method from the closed catalog", nil)
	}
}

func methodAllowed(caller CallerClass, method Method) bool {
	switch caller {
	case CallerOperatorCLI, CallerMCPFacade:
		return method.valid()
	case CallerWorkerReport, CallerComisControl:
		return false
	default:
		return false
	}
}

func queryOutcome(operationID string, stateVersion int64, result any, err error) Outcome {
	if err != nil {
		return outcomeFromError(operationID, err)
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		return rejectedOutcome(operationID, domain.ErrorInternal, false, "response encoding failed", "inspect service health", err)
	}
	return Outcome{
		ProtocolVersion: ProtocolVersion,
		OperationID:     operationID,
		Status:          domain.OperationCompleted,
		StateVersion:    &stateVersion,
		Result:          encoded,
	}
}

func invalidPayload(operationID string, cause error) Outcome {
	return rejectedOutcome(operationID, domain.ErrorInvalidArgument, false, "invalid method payload", "send the strict payload for this method", cause)
}

func outcomeFromError(operationID string, err error) Outcome {
	var failure *domain.Failure
	if errors.As(err, &failure) {
		return Outcome{
			ProtocolVersion: ProtocolVersion,
			OperationID:     operationID,
			Status:          domain.OperationRejected,
			Error: &WireError{
				Code:      failure.Code,
				Message:   failure.Message,
				Retryable: failure.Retryable,
				Hint:      failure.Hint,
			},
		}
	}
	return rejectedOutcome(operationID, domain.ErrorInternal, false, "query failed", "inspect service health", err)
}

func rejectedOutcome(operationID string, code domain.ErrorCode, retryable bool, message, hint string, _ error) Outcome {
	return Outcome{
		ProtocolVersion: ProtocolVersion,
		OperationID:     operationID,
		Status:          domain.OperationRejected,
		Error:           &WireError{Code: code, Message: message, Retryable: retryable, Hint: hint},
	}
}
