package localapi

import (
	"context"
	"encoding/json"
	"errors"
	"regexp"
	"time"

	"github.com/comisai/comis-dev-crew/internal/application"
	"github.com/comisai/comis-dev-crew/internal/domain"
)

const unknownRequestID = "request-unknown"

// Handler authenticates, validates, and dispatches canonical local requests.
type Handler struct {
	queries           ReadQueries
	mutations         TaskMutations
	reconciliation    TaskReconciliation
	interventions     TaskInterventions
	cleanup           TaskCleanup
	primaryCheckouts  PrimaryCheckoutSync
	serviceInstanceID string
	clock             application.Clock
}

var localServiceInstancePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._~-]{0,255}$`)

// NewHandler binds the canonical queries and an injected deadline clock.
func NewHandler(config HandlerConfig) (*Handler, error) {
	if config.Queries == nil {
		return nil, errors.New("create local API handler: queries are required")
	}
	if config.Clock == nil {
		return nil, errors.New("create local API handler: clock is required")
	}
	if config.Mutations != nil && !localServiceInstancePattern.MatchString(config.ServiceInstanceID) {
		return nil, errors.New("create local API handler: service instance identity is required for mutations")
	}
	return &Handler{
		queries: config.Queries, mutations: config.Mutations, reconciliation: config.Reconciliation,
		interventions: config.Interventions, cleanup: config.Cleanup,
		primaryCheckouts:  config.PrimaryCheckouts,
		serviceInstanceID: config.ServiceInstanceID, clock: config.Clock,
	}, nil
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
	case MethodSyncPrimary:
		var input SyncPrimaryInput
		if err := decodeObject(request.Payload, &input); err != nil {
			return invalidPayload(request.OperationID, err)
		}
		if handler.primaryCheckouts == nil {
			return rejectedOutcome(request.OperationID, domain.ErrorUnavailable, true, "primary checkout synchronization is unavailable", "inspect service configuration", nil)
		}
		report, err := handler.primaryCheckouts.SyncPrimary(ctx, application.PrimarySyncCommand{
			OperationID: request.OperationID, RepositoryID: input.RepositoryID,
		})
		return handler.primarySyncOutcome(request.OperationID, report, err)
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
	case MethodWorkerProfiles:
		if err := decodeObject(request.Payload, &emptyPayload{}); err != nil {
			return invalidPayload(request.OperationID, err)
		}
		result, err := handler.queries.ListWorkerProfiles(ctx)
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
	case MethodGetLaunchPlan:
		var payload taskPayload
		if err := decodeObject(request.Payload, &payload); err != nil {
			return invalidPayload(request.OperationID, err)
		}
		result, err := handler.queries.GetLaunchPlan(ctx, payload.TaskHandle)
		return queryOutcome(request.OperationID, result.StateVersion, result, err)
	case MethodOperation:
		var payload operationPayload
		if err := decodeObject(request.Payload, &payload); err != nil {
			return invalidPayload(request.OperationID, err)
		}
		result, err := handler.queries.Operation(ctx, payload.OperationID)
		return queryOutcome(request.OperationID, result.StateVersion, result, err)
	case MethodPrepareTask:
		var input PrepareTaskInput
		if err := decodeObject(request.Payload, &input); err != nil {
			return invalidPayload(request.OperationID, err)
		}
		if handler.mutations == nil {
			return rejectedOutcome(request.OperationID, domain.ErrorUnavailable, true, "mutation service is unavailable", "inspect service configuration", nil)
		}
		result, err := handler.mutations.PrepareTask(ctx, application.PrepareTaskCommand{
			OperationID: request.OperationID, ServiceInstanceID: handler.serviceInstanceID,
			Shape: input.Shape, RepositoryID: input.RepositoryID, BaseRevision: input.BaseRevision,
			AcceptanceCriteria: input.AcceptanceCriteria, Constraints: input.Constraints,
			ValidationProfile: input.ValidationProfile, DeliveryMode: input.DeliveryMode,
			WorkerProfileID: input.WorkerProfileID,
		})
		return handler.prepareOutcome(request.OperationID, result, err)
	case MethodPromoteScout:
		var input PromoteScoutInput
		if err := decodeObject(request.Payload, &input); err != nil {
			return invalidPayload(request.OperationID, err)
		}
		if handler.mutations == nil {
			return rejectedOutcome(request.OperationID, domain.ErrorUnavailable, true, "mutation service is unavailable", "inspect service configuration", nil)
		}
		result, err := handler.mutations.PromoteScout(ctx, application.PromoteScoutCommand{
			OperationID: request.OperationID, ServiceInstanceID: handler.serviceInstanceID,
			ScoutTaskHandle:    input.ScoutTaskHandle,
			AcceptanceCriteria: input.AcceptanceCriteria, Constraints: input.Constraints,
			ValidationProfile: input.ValidationProfile, DeliveryMode: input.DeliveryMode,
			WorkerProfileID: input.WorkerProfileID,
		})
		return handler.prepareOutcome(request.OperationID, result, err)
	case MethodReconcileTask:
		var input ReconcileTaskInput
		if err := decodeObject(request.Payload, &input); err != nil {
			return invalidPayload(request.OperationID, err)
		}
		if handler.reconciliation == nil {
			return rejectedOutcome(request.OperationID, domain.ErrorUnavailable, true, "task reconciliation is unavailable", "inspect service configuration", nil)
		}
		result, err := handler.reconciliation.ReconcileTask(ctx, application.ReconcileTaskCommand{
			OperationID: request.OperationID, TaskHandle: input.TaskHandle, Action: input.Action,
		})
		return handler.taskMutationOutcome(request.OperationID, MethodReconcileTask, result, err)
	case MethodHandbackTask:
		var input HandbackTaskInput
		if err := decodeObject(request.Payload, &input); err != nil {
			return invalidPayload(request.OperationID, err)
		}
		if handler.interventions == nil {
			return rejectedOutcome(request.OperationID, domain.ErrorUnavailable, true, "intervention service is unavailable", "inspect service configuration", nil)
		}
		result, err := handler.interventions.HandbackTask(ctx, application.HandbackTaskCommand{
			OperationID: request.OperationID, TaskHandle: input.TaskHandle, Action: input.Action,
		})
		return handler.taskMutationOutcome(request.OperationID, MethodHandbackTask, result, err)
	case MethodPauseTask:
		return handleTaskHandleMutation(ctx, handler, request, MethodPauseTask,
			handler.mutations == nil, "task mutation service is unavailable",
			func(ctx context.Context, taskHandle string) (application.MutationResult, error) {
				return handler.mutations.PauseTask(ctx, application.PauseTaskCommand{
					OperationID: request.OperationID, TaskHandle: taskHandle,
				})
			})
	case MethodResumeTask:
		return handleTaskHandleMutation(ctx, handler, request, MethodResumeTask,
			handler.interventions == nil, "task intervention service is unavailable",
			func(ctx context.Context, taskHandle string) (application.MutationResult, error) {
				return handler.interventions.ResumeTask(ctx, application.ResumeTaskCommand{
					OperationID: request.OperationID, TaskHandle: taskHandle,
				})
			})
	case MethodVerifyTask:
		return handleTaskHandleMutation(ctx, handler, request, MethodVerifyTask,
			handler.mutations == nil, "task mutation service is unavailable",
			func(ctx context.Context, taskHandle string) (application.MutationResult, error) {
				return handler.mutations.VerifyTask(ctx, application.VerifyTaskCommand{
					OperationID: request.OperationID, TaskHandle: taskHandle,
				})
			})
	case MethodReplaceWorker:
		var input ReplaceWorkerInput
		if err := decodeObject(request.Payload, &input); err != nil {
			return invalidPayload(request.OperationID, err)
		}
		if handler.interventions == nil {
			return rejectedOutcome(request.OperationID, domain.ErrorUnavailable, true, "task intervention service is unavailable", "inspect service configuration", nil)
		}
		result, err := handler.interventions.ReplaceWorker(ctx, application.ReplaceWorkerCommand{
			OperationID: request.OperationID, TaskHandle: input.TaskHandle,
			WorkerProfileID: input.WorkerProfileID,
		})
		return handler.taskMutationOutcome(request.OperationID, MethodReplaceWorker, result, err)
	case MethodSteerTask:
		var input SteerTaskInput
		if err := decodeObject(request.Payload, &input); err != nil {
			return invalidPayload(request.OperationID, err)
		}
		if handler.mutations == nil {
			return rejectedOutcome(request.OperationID, domain.ErrorUnavailable, true, "task mutation service is unavailable", "inspect service configuration", nil)
		}
		result, err := handler.mutations.SteerTask(ctx, application.SteerTaskCommand{
			OperationID: request.OperationID, TaskHandle: input.TaskHandle,
			Instruction: input.Instruction,
		})
		return handler.taskMutationOutcome(request.OperationID, MethodSteerTask, result, err)
	case MethodCancelTask:
		return handleTaskHandleMutation(ctx, handler, request, MethodCancelTask,
			handler.mutations == nil, "task mutation service is unavailable",
			func(ctx context.Context, taskHandle string) (application.MutationResult, error) {
				return handler.mutations.CancelTask(ctx, application.CancelTaskCommand{
					OperationID: request.OperationID, TaskHandle: taskHandle,
				})
			})
	case MethodDiscardTask:
		var input DiscardTaskInput
		if err := decodeObject(request.Payload, &input); err != nil {
			return invalidPayload(request.OperationID, err)
		}
		if handler.cleanup == nil {
			return rejectedOutcome(request.OperationID, domain.ErrorUnavailable, true, "cleanup service is unavailable", "inspect service configuration", nil)
		}
		result, err := handler.cleanup.DiscardTask(ctx, application.DiscardTaskCommand{
			OperationID: request.OperationID, TaskHandle: input.TaskHandle,
			Acknowledged: input.Acknowledged,
		})
		return handler.taskMutationOutcome(request.OperationID, MethodDiscardTask, result, err)
	case MethodCleanupTask:
		var input CleanupTaskInput
		if err := decodeObject(request.Payload, &input); err != nil {
			return invalidPayload(request.OperationID, err)
		}
		if handler.cleanup == nil {
			return rejectedOutcome(request.OperationID, domain.ErrorUnavailable, true, "cleanup service is unavailable", "inspect service configuration", nil)
		}
		result, err := handler.cleanup.CleanupTask(ctx, application.CleanupTaskCommand{
			OperationID: request.OperationID, TaskHandle: input.TaskHandle,
		})
		return handler.taskMutationOutcome(request.OperationID, MethodCleanupTask, result, err)
	default:
		return rejectedOutcome(request.OperationID, domain.ErrorInvalidArgument, false, "unknown local API method", "use a method from the closed catalog", nil)
	}
}

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

// primarySyncOutcome projects one synchronization. A refusal is a completed
// operation carrying its named posture, not an error: the checkout was
// inspected and found unfit to advance, which is an answer the caller acts on.
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
	type safeDependency interface {
		SafeDependencyMessage() string
	}
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

func rejectedOutcome(operationID string, code domain.ErrorCode, retryable bool, message, hint string, _ error) Outcome {
	return Outcome{
		ProtocolVersion: ProtocolVersion,
		OperationID:     operationID,
		Status:          domain.OperationRejected,
		Error:           &WireError{Code: code, Message: message, Retryable: retryable, Hint: hint},
	}
}

// taskHandleMutationInput is the payload every by-handle task mutation takes:
// one task reference and nothing else. Sharing the type is what keeps a new
// command from quietly accepting an extra authority field.
type taskHandleMutationInput struct {
	TaskHandle string `json:"taskHandle"`
}

// handleTaskHandleMutation decodes, checks the mutation surface exists, and
// projects the outcome. Every by-handle command needs exactly this, and a copy
// per command adds two failure branches that say nothing new about the command.
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
	// An absent surface is reported unavailable and retryable, never as though
	// the caller's request were malformed: the request was fine, the deployment
	// is not composed for it.
	if surfaceAbsent {
		return rejectedOutcome(request.OperationID, domain.ErrorUnavailable, true,
			absentMessage, "inspect service configuration", nil)
	}
	result, err := invoke(ctx, input.TaskHandle)
	return handler.taskMutationOutcome(request.OperationID, method, result, err)
}
