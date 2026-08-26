package comiswire

import (
	"context"
	"errors"
	"time"

	"github.com/comisai/comis-dev-crew/internal/application"
)

// ConsumeMergeApproval implements the application approval port without
// leaking generated protocol DTOs into the coordinator.
func (connection *ControlConnection) ConsumeMergeApproval(
	ctx context.Context,
	request application.MergeApprovalConsumeRequest,
) (application.MergeApprovalReceipt, error) {
	result, err := connection.ConsumeApproval(ctx, ConsumeApprovalRequestParams{
		OperationID: OperationID(request.OperationID), ManagedRunID: ManagedRunID(request.ManagedRunID),
		ApprovalRequestID: ApprovalRequestID(request.ApprovalRequestID),
		MCPOperationID:    OperationID(request.MCPOperationID),
	})
	if err != nil {
		return application.MergeApprovalReceipt{}, err
	}
	var state application.MergeApprovalState
	switch result.State {
	case ApprovalReceiptStateConsumed:
		state = application.MergeApprovalConsumed
	case ApprovalReceiptStateIdenticalReplay:
		state = application.MergeApprovalIdenticalReplay
	default:
		return application.MergeApprovalReceipt{}, errors.New("consume merge approval: receipt state is invalid")
	}
	return application.MergeApprovalReceipt{
		State: state, ApprovalRequestID: string(result.ApprovalRequestID),
		ManagedRunID: string(result.ManagedRunID), MCPOperationID: string(result.MCPOperationID),
		ResolvingPrincipalID: result.ResolvingPrincipalID, OperationFingerprint: result.OperationFingerprint,
		ApprovedAt: time.UnixMilli(result.ApprovedAtMs).UTC(),
		ExpiresAt:  time.UnixMilli(result.ExpiresAtMs).UTC(),
		ConsumedAt: time.UnixMilli(result.ConsumedAtMs).UTC(),
	}, nil
}

var _ application.MergeApprovalConsumer = (*ControlConnection)(nil)
