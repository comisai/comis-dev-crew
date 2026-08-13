package reporter

import (
	"context"
	"errors"
	"time"
	"unicode/utf8"

	"github.com/comisai/comis-dev-crew/internal/domain"
)

const (
	runtimeAttentionPollInterval  = 250 * time.Millisecond
	runtimeAttentionCallTimeout   = 4 * time.Second
	maximumAttentionResponseRunes = 16_384
)

// AttentionResponseState is the closed service-to-worker delivery state.
type AttentionResponseState string

const (
	AttentionResponsePending   AttentionResponseState = "pending"
	AttentionResponseDelivered AttentionResponseState = "delivered"
)

// AttentionResponseRequest carries only service-derived run authority. The
// worker contributes the bounded decision key through the protected socket.
type AttentionResponseRequest struct {
	OperationID  string
	ManagedRunID string
	ExternalKey  string
}

// AttentionResponse is the authenticated result returned by the service's
// Comis control adapter.
type AttentionResponse struct {
	ManagedRunID string
	ExternalKey  string
	State        AttentionResponseState
	Response     string
}

// AttentionResponseReceiver retrieves one exact keyed response through the
// service-owned authenticated control connection.
type AttentionResponseReceiver interface {
	ReceiveRuntimeAttentionResponse(context.Context, AttentionResponseRequest) (AttentionResponse, error)
}

type runtimeAttentionOutcome struct {
	ExternalKey string                 `json:"externalKey"`
	State       AttentionResponseState `json:"state"`
	Response    *string                `json:"response,omitempty"`
}

func (server *RuntimeServer) receiveAttentionResponse(
	ctx context.Context,
	request runtimeRequest,
	launch *RuntimeLaunchConfig,
) RuntimeOutcome {
	if request.Report != nil || request.Acknowledgement != nil || domain.ValidateDecisionKey(request.ExternalKey) != nil {
		return runtimeRejected("malformed_request")
	}
	if launch == nil {
		return runtimeRejected("attention_binding_unavailable")
	}
	if server.attentionResponses == nil || server.newAttentionOperationID == nil {
		return runtimeRejected("attention_response_unavailable")
	}
	operationID, err := server.newAttentionOperationID()
	if err != nil || domain.ValidateOperationID(operationID) != nil {
		return runtimeRejected("attention_response_unavailable")
	}
	callContext, cancel := context.WithTimeout(ctx, runtimeAttentionCallTimeout)
	defer cancel()
	response, err := server.attentionResponses.ReceiveRuntimeAttentionResponse(callContext, AttentionResponseRequest{
		OperationID: operationID, ManagedRunID: launch.Expected.ManagedRunID, ExternalKey: request.ExternalKey,
	})
	if err != nil || response.ManagedRunID != launch.Expected.ManagedRunID || response.ExternalKey != request.ExternalKey {
		return runtimeRejected("attention_response_unavailable")
	}
	attention := runtimeAttentionOutcome{ExternalKey: response.ExternalKey, State: response.State}
	switch response.State {
	case AttentionResponsePending:
		if response.Response != "" {
			return runtimeRejected("attention_response_unavailable")
		}
	case AttentionResponseDelivered:
		if !validAttentionResponseContent(response.Response) {
			return runtimeRejected("attention_response_unavailable")
		}
		delivered := response.Response
		attention.Response = &delivered
	default:
		return runtimeRejected("attention_response_unavailable")
	}
	return RuntimeOutcome{Version: runtimeProtocolVersion, AttentionResponse: &attention}
}

// AwaitDecision waits until Comis delivers the exact private response for one
// decision key. Pending and temporarily unavailable host state remain silent.
func (client *RuntimeClient) AwaitDecision(ctx context.Context, externalKey string) (string, error) {
	if ctx == nil {
		return "", errors.New("await runtime decision: context is required")
	}
	if err := domain.ValidateDecisionKey(externalKey); err != nil {
		return "", errors.New("await runtime decision: external key is invalid")
	}
	for {
		outcome, err := client.call(ctx, runtimeRequest{
			Version: runtimeProtocolVersion, Kind: "attention_response", ExternalKey: externalKey,
		})
		if err != nil {
			return "", err
		}
		if outcome.Error != nil {
			if outcome.Error.Code != "attention_response_unavailable" {
				return "", errors.New("await runtime decision: attachment rejected the request")
			}
		} else {
			response, pending, err := validateRuntimeAttentionOutcome(outcome, externalKey)
			if err != nil {
				return "", err
			}
			if !pending {
				return response, nil
			}
		}
		if err := waitRuntimeAttentionPoll(ctx); err != nil {
			return "", err
		}
	}
}

func validateRuntimeAttentionOutcome(outcome RuntimeOutcome, externalKey string) (string, bool, error) {
	if outcome.AttentionResponse == nil || outcome.Brief != nil || outcome.Receipt != nil ||
		outcome.Acknowledgement != nil || outcome.Error != nil || outcome.AttentionResponse.ExternalKey != externalKey {
		return "", false, errors.New("await runtime decision: attachment returned an invalid response")
	}
	attention := outcome.AttentionResponse
	switch attention.State {
	case AttentionResponsePending:
		if attention.Response != nil {
			return "", false, errors.New("await runtime decision: pending response carried content")
		}
		return "", true, nil
	case AttentionResponseDelivered:
		if attention.Response == nil || !validAttentionResponseContent(*attention.Response) {
			return "", false, errors.New("await runtime decision: delivered response is invalid")
		}
		return *attention.Response, false, nil
	default:
		return "", false, errors.New("await runtime decision: response state is invalid")
	}
}

func validAttentionResponseContent(response string) bool {
	return response != "" && utf8.ValidString(response) && utf8.RuneCountInString(response) <= maximumAttentionResponseRunes
}

func waitRuntimeAttentionPoll(ctx context.Context) error {
	timer := time.NewTimer(runtimeAttentionPollInterval)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
