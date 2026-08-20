package localapi

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/comisai/comis-dev-crew/internal/application"
	"github.com/comisai/comis-dev-crew/internal/domain"
)

func (handler *Handler) taskMutationOutcome(
	operationID string,
	method Method,
	mutation application.MutationResult,
	err error,
) Outcome {
	if err != nil {
		return outcomeFromError(operationID, err)
	}
	if mutation.Task.Handle == "" || mutation.Task.StateVersion <= 0 || mutation.Operation.ID != operationID ||
		mutation.Operation.Command != string(method) || mutation.Operation.Status != domain.OperationCompleted ||
		mutation.Operation.ResultRef != mutation.Task.Handle || mutation.Operation.StateVersion < 1 ||
		mutation.Operation.StateVersion > mutation.Task.StateVersion {
		return rejectedOutcome(operationID, domain.ErrorInternal, false, "mutation outcome is incomplete", "inspect durable service state", nil)
	}
	result := TaskMutationResult{
		SchemaVersion: 1, OperationID: operationID, TaskHandle: mutation.Task.Handle,
		State: mutation.Task.State, StateVersion: mutation.Task.StateVersion, SideEffect: method.SideEffect(),
	}
	return queryOutcome(operationID, result.StateVersion, result, nil)
}

// primarySyncOutcome projects one synchronization, including a completed refusal posture.
func (handler *Handler) primarySyncOutcome(
	operationID string,
	report application.PrimarySyncReport,
	err error,
) Outcome {
	if err != nil {
		return outcomeFromError(operationID, err)
	}
	if report.RepositoryID == "" || report.Outcome == "" {
		return rejectedOutcome(operationID, domain.ErrorInternal, false, "synchronization outcome is incomplete", "inspect service configuration", nil)
	}
	return queryOutcome(operationID, report.StateVersion, report, nil)
}

func (handler *Handler) prepareOutcome(operationID string, mutation application.MutationResult, err error) Outcome {
	if err != nil {
		return outcomeFromError(operationID, err)
	}
	if mutation.Preparation == nil || mutation.Task.Handle != mutation.Preparation.ExternalRunRef ||
		mutation.Task.State != domain.TaskPrepared || mutation.Task.StateVersion <= 0 ||
		mutation.Operation.ID != operationID || mutation.Operation.Status != domain.OperationCompleted ||
		mutation.Operation.StateVersion != mutation.Task.StateVersion ||
		mutation.Preparation.Validate(handler.clock()) != nil {
		return rejectedOutcome(operationID, domain.ErrorInternal, false, "mutation outcome is incomplete", "inspect durable service state", nil)
	}
	result := PrepareTaskResult{
		SchemaVersion: 1, OperationID: operationID, TaskHandle: mutation.Task.Handle,
		State: mutation.Task.State, StateVersion: mutation.Task.StateVersion,
		SideEffect: MethodPrepareTask.SideEffect(), ManagedRun: *mutation.Preparation,
	}
	return queryOutcome(operationID, result.StateVersion, result, nil)
}

func methodAllowed(caller CallerClass, method Method) bool {
	switch caller {
	case CallerOperatorCLI:
		return method.valid()
	case CallerMCPFacade:
		return method.valid() && !method.operatorOnly()
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
		ProtocolVersion: ProtocolVersion, OperationID: operationID,
		Status: domain.OperationCompleted, StateVersion: &stateVersion, Result: encoded,
	}
}

func invalidPayload(operationID string, cause error) Outcome {
	return rejectedOutcome(operationID, domain.ErrorInvalidArgument, false, "invalid method payload", "send the strict payload for this method", cause)
}

func outcomeFromError(operationID string, err error) Outcome {
	var failure *domain.Failure
	if errors.As(err, &failure) {
		return Outcome{
			ProtocolVersion: ProtocolVersion, OperationID: operationID, Status: domain.OperationRejected,
			Error: &WireError{Code: failure.Code, Message: failure.Message, Retryable: failure.Retryable, Hint: failure.Hint},
		}
	}
	type safeDependency interface{ SafeDependencyMessage() string }
	var dependency safeDependency
	if errors.As(err, &dependency) {
		classified, classifyErr := domain.NewFailure(
			domain.ErrorUnavailable, true, dependency.SafeDependencyMessage(),
			"inspect service dependency health and exact configuration", err,
		)
		if classifyErr == nil {
			return outcomeFromError(operationID, classified)
		}
	}
	return rejectedOutcome(operationID, domain.ErrorInternal, false, "query failed", "inspect service health", err)
}

func rejectedOutcome(operationID string, code domain.ErrorCode, retryable bool, message, hint string, cause error) Outcome {
	return Outcome{
		ProtocolVersion: ProtocolVersion, OperationID: operationID, Status: domain.OperationRejected,
		Error:        &WireError{Code: code, Message: message, Retryable: retryable, Hint: hint},
		failureCause: boundaryFailureCause(code, cause),
	}
}

func boundaryFailureCause(code domain.ErrorCode, cause error) application.BoundaryFailureCause {
	if code != domain.ErrorInternal || cause == nil {
		return ""
	}
	var validation *domain.ValidationError
	if !errors.As(cause, &validation) {
		return ""
	}
	switch validation.Field {
	case "briefRevisionHash", "acceptanceCriteria", "constraints", "consumedContracts":
		return application.BoundaryFailureDurableTaskContractInvalid
	default:
		return ""
	}
}

type taskHandleMutationInput struct {
	TaskHandle string `json:"taskHandle"`
}

func handleTaskHandleMutation(
	ctx context.Context,
	handler *Handler,
	request Request,
	method Method,
	surfaceAbsent bool,
	absentMessage string,
	invoke func(context.Context, string) (application.MutationResult, error),
) Outcome {
	var input taskHandleMutationInput
	if err := decodeObject(request.Payload, &input); err != nil {
		return invalidPayload(request.OperationID, err)
	}
	if surfaceAbsent {
		return rejectedOutcome(request.OperationID, domain.ErrorUnavailable, true,
			absentMessage, "inspect service configuration", nil)
	}
	result, err := invoke(ctx, input.TaskHandle)
	return handler.taskMutationOutcome(request.OperationID, method, result, err)
}
