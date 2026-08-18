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

// reconcileTaskMutation resolves an uncertain mutation by reading what the
// recorded operation actually did, then replaying it.
//
// Re-sending blind is the tempting shortcut and the wrong one: the second
// attempt's outcome would be reported as if it were the first's, so a send that
// succeeded and a send that was refused become indistinguishable. Reading the
// operation first is what keeps "uncertain" recoverable instead of guessed.
//
// The skeleton is shared because every command's copy differed only in the
// command name and the replay call, while the four give-up branches — no
// context, unmintable request ID, unreadable operation, wrong operation — are
// identical and cannot be reached without breaking the transport underneath.
func reconcileTaskMutation[Input any](
	facade *Facade,
	ctx context.Context,
	operationID string,
	command string,
	input Input,
	replay func(context.Context, string, Input) (localapi.TaskMutationResult, error),
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
	if err != nil || operation.OperationID != operationID || operation.Command != command ||
		operation.Status != domain.OperationCompleted {
		return localapi.TaskMutationResult{}, original
	}
	return replay(reconcileContext, operationID, input)
}

func (facade *Facade) reconcileCleanup(
	ctx context.Context,
	operationID string,
	input localapi.CleanupTaskInput,
	original error,
) (localapi.TaskMutationResult, error) {
	return reconcileTaskMutation(
		facade, ctx, operationID, "CleanupTask", input, facade.client.CleanupTask, original,
	)
}

func (facade *Facade) reconcilePause(
	ctx context.Context,
	operationID string,
	input localapi.PauseTaskInput,
	original error,
) (localapi.TaskMutationResult, error) {
	return reconcileTaskMutation(
		facade, ctx, operationID, "PauseTask", input, facade.client.PauseTask, original,
	)
}
