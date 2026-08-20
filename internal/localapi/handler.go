package localapi

import (
	"context"
	"errors"
	"regexp"
	"time"

	"github.com/comisai/comis-dev-crew/internal/application"
	"github.com/comisai/comis-dev-crew/internal/domain"
)

const unknownRequestID = "request-unknown"

// unknownRequestMethod names a crossing whose envelope never parsed, so the
// record still counts the call instead of dropping it.
const unknownRequestMethod = "unknown"

// Handler authenticates, validates, and dispatches canonical local requests.
type Handler struct {
	queries             ReadQueries
	initiativeQueries   InitiativeReadQueries
	mutations           TaskMutations
	initiativeMutations InitiativeMutations
	initiativeControls  InitiativeControls
	reconciliation      TaskReconciliation
	interventions       TaskInterventions
	cleanup             TaskCleanup
	primaryCheckouts    PrimaryCheckoutSync
	scoutReviews        ScoutReviewAttestation
	decisions           DecisionAuthority
	serviceInstanceID   string
	clock               application.Clock
	logger              application.BoundaryLogger
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
	if (config.Mutations != nil || config.InitiativeMutations != nil || config.InitiativeControls != nil) &&
		!localServiceInstancePattern.MatchString(config.ServiceInstanceID) {
		return nil, errors.New("create local API handler: service instance identity is required for mutations")
	}
	return &Handler{
		queries: config.Queries, initiativeQueries: config.InitiativeQueries,
		mutations:           config.Mutations,
		initiativeMutations: config.InitiativeMutations, reconciliation: config.Reconciliation,
		initiativeControls: config.InitiativeControls,
		interventions:      config.Interventions, cleanup: config.Cleanup,
		primaryCheckouts:  config.PrimaryCheckouts,
		scoutReviews:      config.ScoutReviews,
		decisions:         config.Decisions,
		serviceInstanceID: config.ServiceInstanceID, clock: config.Clock,
		logger: config.Logger,
	}, nil
}

func (handler *Handler) serve(ctx context.Context, caller CallerClass, data []byte) Outcome {
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
	if outcome, handled := handler.dispatchInitiative(ctx, request); handled {
		return outcome
	}
	// Observation reads are dispatched first and live beside each other, so
	// the transition surface below stays readable as reads accumulate.
	if outcome, handled := handler.dispatchObservation(ctx, request); handled {
		return outcome
	}
	if outcome, handled := handler.dispatchDecisionAuthority(ctx, request); handled {
		return outcome
	}
	switch request.Method {
	case MethodAttestScout:
		var input AttestScoutDecisionsInput
		if err := decodeObject(request.Payload, &input); err != nil {
			return invalidPayload(request.OperationID, err)
		}
		if handler.scoutReviews == nil {
			return rejectedOutcome(request.OperationID, domain.ErrorUnavailable, true, "scout review attestation is unavailable", "inspect service configuration", nil)
		}
		result, err := handler.scoutReviews.AttestScoutDecisions(ctx, application.AttestScoutDecisionsCommand{
			OperationID: request.OperationID, TaskHandle: input.TaskHandle,
			Finding:          input.Finding,
			OpenDecisionKeys: append([]string(nil), input.OpenDecisionKeys...),
		})
		return handler.taskMutationOutcome(request.OperationID, MethodAttestScout, result, err)
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
		var input ListTasksInput
		if err := decodeObject(request.Payload, &input); err != nil {
			return invalidPayload(request.OperationID, err)
		}
		result, err := handler.queries.ListTasks(ctx, input.State)
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
