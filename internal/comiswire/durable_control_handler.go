package comiswire

import (
	"context"
	"errors"
	"time"

	"github.com/comisai/comis-dev-crew/internal/application"
	"github.com/comisai/comis-dev-crew/internal/domain"
)

// DurableControlMutations is the application-owned inbound lifecycle surface.
type DurableControlMutations interface {
	ActivateManagedRun(context.Context, application.ActivateManagedRunCommand) (application.MutationResult, error)
	AbandonManagedRun(context.Context, application.AbandonManagedRunCommand) (application.MutationResult, error)
}

// DurableControlHandlerConfig binds one authenticated instance to the sole
// durable application mutation coordinator.
type DurableControlHandlerConfig struct {
	Mutations         DurableControlMutations
	ServiceInstanceID ServiceInstanceID
}

// DurableControlHandler translates generated protocol values at the adapter
// boundary and returns acknowledgements only from committed operation results.
type DurableControlHandler struct {
	mutations         DurableControlMutations
	serviceInstanceID string
}

// NewDurableControlHandler validates the configured instance authority.
func NewDurableControlHandler(config DurableControlHandlerConfig) (*DurableControlHandler, error) {
	if config.Mutations == nil {
		return nil, errors.New("create durable Comis control handler: mutations are required")
	}
	if err := domain.ValidateAuthorityReference("serviceInstanceId", string(config.ServiceInstanceID)); err != nil {
		return nil, errors.New("create durable Comis control handler: service instance identity is invalid")
	}
	return &DurableControlHandler{
		mutations: config.Mutations, serviceInstanceID: string(config.ServiceInstanceID),
	}, nil
}

// Activate commits the exact host run and workspace lease before acknowledging.
func (handler *DurableControlHandler) Activate(ctx context.Context, params ActivateRequestParams) (ActivateResponseResult, error) {
	workspaceLeaseID := ""
	if params.WorkspaceLeaseID != nil {
		workspaceLeaseID = string(*params.WorkspaceLeaseID)
	}
	result, err := handler.mutations.ActivateManagedRun(ctx, application.ActivateManagedRunCommand{
		OperationID: string(params.OperationID), ServiceInstanceID: handler.serviceInstanceID,
		ManagedRunID: string(params.ManagedRunID), ExternalRunRef: string(params.ExternalRunRef),
		RegistrationNonce: string(params.RegistrationNonce), WorkspaceLeaseID: workspaceLeaseID,
	})
	if err != nil {
		return ActivateResponseResult{}, controlMutationFailure(err)
	}
	if result.Operation.ID != string(params.OperationID) || result.Operation.Status != domain.OperationCompleted ||
		result.Operation.UpdatedAt.Location() != time.UTC || result.Task.State != domain.TaskReady ||
		result.Task.ManagedRunID != string(params.ManagedRunID) || result.Task.Handle != string(params.ExternalRunRef) ||
		result.Task.WorkspaceLeaseID != workspaceLeaseID {
		return ActivateResponseResult{}, wireFailure(ErrorKindInternalError, "durable activation result is incomplete")
	}
	return ActivateResponseResult{
		ActivatedAtMs: result.Operation.UpdatedAt.UnixMilli(), ExternalRunRef: params.ExternalRunRef,
		ManagedRunID: params.ManagedRunID, State: ManagedRunStateActive,
	}, nil
}

// Abandon closes the exact preparation and returns its fixed terminal mapping.
func (handler *DurableControlHandler) Abandon(ctx context.Context, params AbandonRequestParams) (AbandonResponseResult, error) {
	result, err := handler.mutations.AbandonManagedRun(ctx, application.AbandonManagedRunCommand{
		OperationID: string(params.OperationID), ServiceInstanceID: handler.serviceInstanceID,
		ExternalRunRef: string(params.ExternalRunRef), RegistrationNonce: string(params.RegistrationNonce),
		Reason:      application.AbandonReason(params.Reason),
		Disposition: application.AbandonDisposition(params.Disposition),
	})
	if err != nil {
		return AbandonResponseResult{}, controlMutationFailure(err)
	}
	if result.Operation.ID != string(params.OperationID) || result.Operation.Status != domain.OperationCompleted ||
		result.Preparation == nil || result.Preparation.State != application.PreparationAbandoned ||
		result.Preparation.ExternalRunRef != string(params.ExternalRunRef) ||
		result.Preparation.Disposition != application.AbandonDisposition(params.Disposition) {
		return AbandonResponseResult{}, wireFailure(ErrorKindInternalError, "durable abandonment result is incomplete")
	}
	return AbandonResponseResult{
		Disposition: params.Disposition, ExternalRunRef: params.ExternalRunRef,
		State:              ManagedRunStateAbandoned,
		TerminalTransition: AbandonTerminalTransitionUnboundPreparationAbandoned,
	}, nil
}

func controlMutationFailure(err error) error {
	if errors.Is(err, context.DeadlineExceeded) {
		return wireFailure(ErrorKindDeadlineExceeded, "control mutation deadline elapsed")
	}
	var failure *domain.Failure
	if !errors.As(err, &failure) {
		return wireFailure(ErrorKindInternalError, "durable control mutation failed")
	}
	switch failure.Code {
	case domain.ErrorInvalidArgument:
		return wireFailure(ErrorKindInvalidParams, "control mutation fields are invalid")
	case domain.ErrorConflict:
		return wireFailure(ErrorKindReplayConflict, "control operation replay conflicts")
	case domain.ErrorNotFound, domain.ErrorPrecondition:
		return wireFailure(ErrorKindPreconditionFailed, "managed-run preparation precondition failed")
	case domain.ErrorDeadlineExceeded:
		return wireFailure(ErrorKindDeadlineExceeded, "control mutation deadline elapsed")
	default:
		return wireFailure(ErrorKindInternalError, "durable control mutation failed")
	}
}
