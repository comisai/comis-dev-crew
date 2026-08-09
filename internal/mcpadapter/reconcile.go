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
