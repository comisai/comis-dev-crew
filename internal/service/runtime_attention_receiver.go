package service

import (
	"context"
	"errors"

	"github.com/comisai/comis-dev-crew/internal/comiswire"
	"github.com/comisai/comis-dev-crew/internal/domain"
	"github.com/comisai/comis-dev-crew/internal/reporter"
)

// SetAttentionResponseReceiver binds the sole authenticated Comis response
// lane before any runtime attachment is served.
func (coordinator *runtimeAttachmentCoordinator) SetAttentionResponseReceiver(receiver comiswire.AttentionResponseReceiver) error {
	if receiver == nil {
		return errors.New("configure runtime attention responses: receiver is required")
	}
	coordinator.mu.Lock()
	defer coordinator.mu.Unlock()
	if coordinator.attentionResponses != nil {
		return errors.New("configure runtime attention responses: receiver is already set")
	}
	coordinator.attentionResponses = receiver
	return nil
}

func (coordinator *runtimeAttachmentCoordinator) ReceiveRuntimeAttentionResponse(
	ctx context.Context,
	request reporter.AttentionResponseRequest,
) (reporter.AttentionResponse, error) {
	if ctx == nil || domain.ValidateOperationID(request.OperationID) != nil ||
		domain.ValidateAuthorityReference("managedRunId", request.ManagedRunID) != nil ||
		domain.ValidateDecisionKey(request.ExternalKey) != nil {
		return reporter.AttentionResponse{}, errors.New("receive runtime attention response: request is invalid")
	}
	coordinator.mu.Lock()
	receiver := coordinator.attentionResponses
	coordinator.mu.Unlock()
	if receiver == nil {
		return reporter.AttentionResponse{}, errors.New("receive runtime attention response: Comis control is unavailable")
	}
	result, err := receiver.ReceiveAttentionResponse(ctx, comiswire.ReceiveAttentionResponseRequestParams{
		OperationID: comiswire.OperationID(request.OperationID), ManagedRunID: comiswire.ManagedRunID(request.ManagedRunID),
		ExternalKey: request.ExternalKey,
	})
	if err != nil {
		return reporter.AttentionResponse{}, errors.New("receive runtime attention response: Comis request failed")
	}
	if string(result.ManagedRunID) != request.ManagedRunID || result.ExternalKey != request.ExternalKey {
		return reporter.AttentionResponse{}, errors.New("receive runtime attention response: Comis identity differs")
	}
	response := reporter.AttentionResponse{
		ManagedRunID: string(result.ManagedRunID), ExternalKey: result.ExternalKey,
	}
	switch result.State {
	case comiswire.ManagedRunStatePending:
		if result.Response != nil {
			return reporter.AttentionResponse{}, errors.New("receive runtime attention response: pending result carried content")
		}
		response.State = reporter.AttentionResponsePending
	case comiswire.ManagedRunStateDelivered:
		if result.Response == nil {
			return reporter.AttentionResponse{}, errors.New("receive runtime attention response: delivered result omitted content")
		}
		response.State = reporter.AttentionResponseDelivered
		response.Response = *result.Response
	default:
		return reporter.AttentionResponse{}, errors.New("receive runtime attention response: state is invalid")
	}
	return response, nil
}
