package mcpadapter

import (
	"context"
	"errors"

	"github.com/comisai/comis-dev-crew/internal/domain"
	"github.com/comisai/comis-dev-crew/internal/localapi"
)

func (facade *Facade) reconcilePreparation(ctx context.Context, operationID string, input localapi.PrepareTaskInput, original error) (localapi.PrepareTaskResult, error) {
	if ctx == nil {
		return localapi.PrepareTaskResult{}, original
	}
	reconcileContext, cancel := context.WithTimeout(context.WithoutCancel(ctx), facade.reconcileTimeout)
	defer cancel()
	requestID, err := facade.newOperationID()
	if err != nil || domain.ValidateOperationID(requestID) != nil {
		return localapi.PrepareTaskResult{}, original
	}
	operation, err := facade.client.Operation(reconcileContext, requestID, operationID)
	if err != nil || operation.OperationID != operationID || operation.Command != "PrepareTask" {
		return localapi.PrepareTaskResult{}, original
	}
	switch operation.Status {
	case domain.OperationCompleted:
		return facade.client.PrepareTask(reconcileContext, operationID, input)
	case domain.OperationRejected:
		if operation.ErrorCode.Valid() {
			return localapi.PrepareTaskResult{}, safeFailure(operation.ErrorCode, false, "task preparation was rejected", "correct the task contract before retrying")
		}
		return localapi.PrepareTaskResult{}, original
	case domain.OperationAccepted, domain.OperationUnknown:
		return localapi.PrepareTaskResult{}, original
	default:
		return localapi.PrepareTaskResult{}, errors.New("unknown operation reconciliation status")
	}
}

func (facade *Facade) reconcileHandback(
	ctx context.Context,
	operationID string,
	input localapi.HandbackTaskInput,
	original error,
) (localapi.TaskMutationResult, error) {
	if ctx == nil {
		return localapi.TaskMutationResult{}, original
	}
	reconcileContext, cancel := context.WithTimeout(context.WithoutCancel(ctx), facade.reconcileTimeout)
	defer cancel()
	requestID, err := facade.newOperationID()
	if err != nil || domain.ValidateOperationID(requestID) != nil {
		return localapi.TaskMutationResult{}, original
	}
	operation, err := facade.client.Operation(reconcileContext, requestID, operationID)
	if err != nil || operation.OperationID != operationID || operation.Command != "HandbackTask" ||
		operation.Status != domain.OperationCompleted {
		return localapi.TaskMutationResult{}, original
	}
	return facade.client.HandbackTask(reconcileContext, operationID, input)
}

func (facade *Facade) reconcileTaskMutation(
	ctx context.Context,
	operationID string,
	input localapi.ReconcileTaskInput,
	original error,
) (localapi.TaskMutationResult, error) {
	if ctx == nil {
		return localapi.TaskMutationResult{}, original
	}
	reconcileContext, cancel := context.WithTimeout(context.WithoutCancel(ctx), facade.reconcileTimeout)
	defer cancel()
	requestID, err := facade.newOperationID()
	if err != nil || domain.ValidateOperationID(requestID) != nil {
		return localapi.TaskMutationResult{}, original
	}
	operation, err := facade.client.Operation(reconcileContext, requestID, operationID)
	if err != nil || operation.OperationID != operationID || operation.Command != "ReconcileTask" ||
		operation.Status != domain.OperationCompleted {
		return localapi.TaskMutationResult{}, original
	}
	return facade.client.ReconcileTask(reconcileContext, operationID, input)
}

func (facade *Facade) reconcileCleanup(
	ctx context.Context,
	operationID string,
	input localapi.CleanupTaskInput,
	original error,
) (localapi.TaskMutationResult, error) {
	if ctx == nil {
		return localapi.TaskMutationResult{}, original
	}
	reconcileContext, cancel := context.WithTimeout(context.WithoutCancel(ctx), facade.reconcileTimeout)
	defer cancel()
	requestID, err := facade.newOperationID()
	if err != nil || domain.ValidateOperationID(requestID) != nil {
		return localapi.TaskMutationResult{}, original
	}
	operation, err := facade.client.Operation(reconcileContext, requestID, operationID)
	if err != nil || operation.OperationID != operationID || operation.Command != "CleanupTask" ||
		operation.Status != domain.OperationCompleted {
		return localapi.TaskMutationResult{}, original
	}
	return facade.client.CleanupTask(reconcileContext, operationID, input)
}
