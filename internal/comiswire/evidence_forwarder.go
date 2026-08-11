package comiswire

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"time"

	"github.com/comisai/comis-dev-crew/internal/application"
	"github.com/comisai/comis-dev-crew/internal/domain"
)

// EvidenceSender sends one already-durable immutable evidence publication.
// Errors leave the remote outcome uncertain and require same-identity retry.
type EvidenceSender interface {
	PutEvidence(context.Context, PutEvidenceRequestParams) (PutEvidenceResponseResult, error)
}

// EvidenceForwarderConfig composes the durable evidence outbox with one
// authenticated persistent control connection.
type EvidenceForwarderConfig struct {
	Outbox         application.ComisEvidenceOutbox
	Sender         EvidenceSender
	Clock          application.Clock
	PollInterval   time.Duration
	MinimumBackoff time.Duration
	MaximumBackoff time.Duration
}

// EvidenceForwarder owns retry supervision but no publication state.
type EvidenceForwarder struct {
	config EvidenceForwarderConfig
}

// NewEvidenceForwarder validates durable forwarding dependencies.
func NewEvidenceForwarder(config EvidenceForwarderConfig) (*EvidenceForwarder, error) {
	if config.Outbox == nil || config.Sender == nil || config.Clock == nil || config.PollInterval <= 0 ||
		config.MinimumBackoff <= 0 || config.MaximumBackoff < config.MinimumBackoff {
		return nil, errors.New("create Comis evidence forwarder: dependencies or bounds are invalid")
	}
	return &EvidenceForwarder{config: config}, nil
}

// Run sends the oldest publication and records only an exact host
// acknowledgement. Cancellation joins the loop.
func (forwarder *EvidenceForwarder) Run(ctx context.Context) error {
	if forwarder == nil || ctx == nil {
		return errors.New("run Comis evidence forwarder: forwarder and context are required")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	backoff := forwarder.config.MinimumBackoff
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		delivery, found, err := forwarder.config.Outbox.NextComisEvidence(ctx)
		if err != nil {
			return fmt.Errorf("run Comis evidence forwarder: read outbox: %w", err)
		}
		if !found {
			backoff = forwarder.config.MinimumBackoff
			if err := waitControlBackoff(ctx, forwarder.config.PollInterval); err != nil {
				return err
			}
			continue
		}
		request, err := evidenceForwarderRequest(delivery)
		if err != nil {
			return fmt.Errorf("run Comis evidence forwarder: %w", err)
		}
		result, err := forwarder.config.Sender.PutEvidence(ctx, request)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if err := waitControlBackoff(ctx, backoff); err != nil {
				return err
			}
			backoff = nextReportBackoff(backoff, forwarder.config.MaximumBackoff)
			continue
		}
		deliveredAt := forwarder.config.Clock()
		ack, err := evidenceForwarderAcknowledgement(delivery, result, deliveredAt)
		if err != nil {
			return fmt.Errorf("run Comis evidence forwarder: %w", err)
		}
		if err := forwarder.config.Outbox.MarkComisEvidenceDelivered(
			ctx, delivery.OperationID, ack, deliveredAt,
		); err != nil {
			return fmt.Errorf("run Comis evidence forwarder: mark delivered: %w", err)
		}
		backoff = forwarder.config.MinimumBackoff
	}
}

func evidenceForwarderRequest(delivery application.ComisEvidenceDelivery) (PutEvidenceRequestParams, error) {
	if domain.ValidateOperationID(delivery.OperationID) != nil ||
		domain.ValidateAuthorityReference("managedRunId", delivery.ManagedRunID) != nil ||
		domain.ValidateAuthorityReference("evidenceRef", delivery.EvidenceRef) != nil ||
		domain.ValidateAuthorityReference("evidenceKind", delivery.Kind) != nil ||
		delivery.ObservedAt.IsZero() || delivery.ObservedAt.Location() != time.UTC ||
		len(delivery.Body) == 0 || len(delivery.Body) > MaxEvidenceBytes || delivery.StateVersion < 1 ||
		fmt.Sprintf("%x", sha256.Sum256(delivery.Body)) != delivery.ContentHash {
		return PutEvidenceRequestParams{}, errors.New("durable Comis evidence publication is invalid")
	}
	verification, err := evidenceForwarderVerification(delivery.VerificationLevel)
	if err != nil {
		return PutEvidenceRequestParams{}, err
	}
	request := PutEvidenceRequestParams{
		OperationID: OperationID(delivery.OperationID), ManagedRunID: ManagedRunID(delivery.ManagedRunID),
		EvidenceRef: EvidenceRef(delivery.EvidenceRef), Kind: EvidenceKind(delivery.Kind),
		SubjectDigest: delivery.SubjectDigest, ObservedAtMs: delivery.ObservedAt.UnixMilli(),
		ContentHash: delivery.ContentHash, VerificationLevel: verification,
		BodyBase64: base64.StdEncoding.EncodeToString(delivery.Body),
	}
	if request.ObservedAtMs < 0 {
		return PutEvidenceRequestParams{}, errors.New("durable Comis evidence observation time is invalid")
	}
	if delivery.ExpiresAt != nil {
		if delivery.ExpiresAt.Location() != time.UTC || !delivery.ExpiresAt.After(delivery.ObservedAt) {
			return PutEvidenceRequestParams{}, errors.New("durable Comis evidence expiry is invalid")
		}
		expiresAtMs := delivery.ExpiresAt.UnixMilli()
		request.ExpiresAtMs = &expiresAtMs
	}
	if delivery.Delivery != nil {
		converted, convertErr := evidenceForwarderDelivery(*delivery.Delivery)
		if convertErr != nil {
			return PutEvidenceRequestParams{}, convertErr
		}
		request.Delivery = &converted
	}
	document := PutEvidenceRequest{
		JSONRPC: JSONRPCVersion, ID: request.OperationID, Method: MethodManagedRunsPutEvidence, Params: request,
	}
	if err := validateGeneratedDocument(schemaPutEvidenceRequest, document); err != nil {
		return PutEvidenceRequestParams{}, fmt.Errorf("invalid durable Comis evidence: %w", err)
	}
	return request, nil
}

func evidenceForwarderVerification(value application.ComisEvidenceVerification) (EvidenceVerificationLevel, error) {
	switch value {
	case application.ComisEvidenceReported:
		return EvidenceVerificationLevelReported, nil
	case application.ComisEvidenceAdapterVerified:
		return EvidenceVerificationLevelAdapterVerified, nil
	default:
		return "", errors.New("durable Comis evidence verification is invalid")
	}
}

func evidenceForwarderDelivery(
	delivery application.ComisEvidenceDeliveryRequest,
) (PutEvidenceRequestParamsDelivery, error) {
	switch delivery.Kind {
	case application.ComisEvidenceReference:
		if delivery.FileName != "" || delivery.MediaType != "" {
			return PutEvidenceRequestParamsDelivery{}, errors.New("durable Comis evidence reference metadata is invalid")
		}
		return PutEvidenceRequestParamsDelivery{Kind: EvidenceDeliveryKindReference}, nil
	case application.ComisEvidenceAttachment:
		if delivery.FileName == "" || delivery.MediaType == "" {
			return PutEvidenceRequestParamsDelivery{}, errors.New("durable Comis evidence attachment metadata is invalid")
		}
		fileName := delivery.FileName
		mediaType := delivery.MediaType
		return PutEvidenceRequestParamsDelivery{
			Kind: EvidenceDeliveryKindAttachment, FileName: &fileName, MediaType: &mediaType,
		}, nil
	default:
		return PutEvidenceRequestParamsDelivery{}, errors.New("durable Comis evidence delivery kind is invalid")
	}
}

func evidenceForwarderAcknowledgement(
	delivery application.ComisEvidenceDelivery,
	result PutEvidenceResponseResult,
	deliveredAt time.Time,
) (application.ComisEvidenceAcknowledgement, error) {
	document := PutEvidenceResponse{JSONRPC: JSONRPCVersion, ID: OperationID(delivery.OperationID), Result: result}
	if err := validateGeneratedDocument(schemaPutEvidenceResponse, document); err != nil {
		return application.ComisEvidenceAcknowledgement{}, fmt.Errorf("invalid Comis evidence acknowledgement: %w", err)
	}
	verification, err := evidenceForwarderVerification(delivery.VerificationLevel)
	if err != nil || result.ManagedRunID != ManagedRunID(delivery.ManagedRunID) ||
		result.EvidenceRef != EvidenceRef(delivery.EvidenceRef) || result.ContentHash != delivery.ContentHash ||
		result.VerificationLevel != verification || deliveredAt.IsZero() || deliveredAt.Location() != time.UTC {
		return application.ComisEvidenceAcknowledgement{}, errors.New("Comis evidence acknowledgement identity is invalid")
	}
	ack := application.ComisEvidenceAcknowledgement{
		ManagedRunID: delivery.ManagedRunID, EvidenceRef: delivery.EvidenceRef,
		ContentHash: delivery.ContentHash, VerificationLevel: delivery.VerificationLevel,
	}
	if result.RetainedUntilMs != nil {
		retainedUntil := time.UnixMilli(*result.RetainedUntilMs).UTC()
		if !retainedUntil.After(deliveredAt) {
			return application.ComisEvidenceAcknowledgement{}, errors.New("Comis evidence acknowledgement retention is invalid")
		}
		ack.RetainedUntil = &retainedUntil
	}
	return ack, nil
}
