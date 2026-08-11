package comiswire

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/comisai/comis-dev-crew/internal/application"
	"github.com/comisai/comis-dev-crew/internal/domain"
)

// ReportSender sends one already-durable report. Any returned error leaves the
// remote outcome uncertain, so callers may retry only with the same identities.
type ReportSender interface {
	Report(context.Context, ReportRequestParams) (ReportResponseResult, error)
}

// ReportForwarderConfig composes the durable outbox with the authenticated
// control connection and bounded retry supervision.
type ReportForwarderConfig struct {
	Outbox         application.ComisReportOutbox
	Sender         ReportSender
	Clock          application.Clock
	PollInterval   time.Duration
	MinimumBackoff time.Duration
	MaximumBackoff time.Duration
}

// ReportForwarder delivers the oldest pending report and durably records only
// exact host acknowledgement evidence. It owns no delivery state itself.
type ReportForwarder struct {
	config ReportForwarderConfig
}

// NewReportForwarder validates forwarding dependencies before supervision.
func NewReportForwarder(config ReportForwarderConfig) (*ReportForwarder, error) {
	if config.Outbox == nil {
		return nil, errors.New("create Comis report forwarder: outbox is required")
	}
	if config.Sender == nil {
		return nil, errors.New("create Comis report forwarder: sender is required")
	}
	if config.Clock == nil {
		return nil, errors.New("create Comis report forwarder: clock is required")
	}
	if config.PollInterval <= 0 {
		return nil, errors.New("create Comis report forwarder: poll interval must be positive")
	}
	if config.MinimumBackoff <= 0 || config.MaximumBackoff < config.MinimumBackoff {
		return nil, errors.New("create Comis report forwarder: retry backoff is invalid")
	}
	return &ReportForwarder{config: config}, nil
}

// Run polls durable state and retries uncertain sends with bounded backoff.
// Cancellation joins the loop; store or evidence failures stop supervision.
func (forwarder *ReportForwarder) Run(ctx context.Context) error {
	if ctx == nil {
		return errors.New("run Comis report forwarder: context is required")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	backoff := forwarder.config.MinimumBackoff
	for {
		delivery, found, err := forwarder.config.Outbox.NextComisReport(ctx)
		if err != nil {
			return fmt.Errorf("run Comis report forwarder: read outbox: %w", err)
		}
		if !found {
			backoff = forwarder.config.MinimumBackoff
			if err := waitControlBackoff(ctx, forwarder.config.PollInterval); err != nil {
				return err
			}
			continue
		}
		request, err := reportForwarderRequest(delivery)
		if err != nil {
			return fmt.Errorf("run Comis report forwarder: %w", err)
		}
		result, err := forwarder.config.Sender.Report(ctx, request)
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
		ack, err := reportForwarderAcknowledgement(delivery, result, deliveredAt)
		if err != nil {
			return fmt.Errorf("run Comis report forwarder: %w", err)
		}
		if err := forwarder.config.Outbox.MarkComisReportDelivered(ctx, delivery.OperationID, ack, deliveredAt); err != nil {
			return fmt.Errorf("run Comis report forwarder: mark delivered: %w", err)
		}
		backoff = forwarder.config.MinimumBackoff
	}
}

func reportForwarderRequest(delivery application.ComisReportDelivery) (ReportRequestParams, error) {
	if err := domain.ValidateTaskHandle(delivery.TaskHandle); err != nil {
		return ReportRequestParams{}, errors.New("invalid durable report task identity")
	}
	if err := domain.ValidateLocalReportID(delivery.LocalReportID); err != nil || delivery.StateVersion < 1 {
		return ReportRequestParams{}, errors.New("invalid durable report evidence")
	}
	kind, err := reportForwarderKind(delivery.Kind)
	if err != nil {
		return ReportRequestParams{}, err
	}
	request := ReportRequestParams{
		OperationID: OperationID(delivery.OperationID), ManagedRunID: ManagedRunID(delivery.ManagedRunID),
		ServiceReportID: ServiceReportID(delivery.ServiceReportID), Kind: kind, Summary: delivery.Summary,
	}
	for _, reference := range delivery.ArtifactRefs {
		request.ArtifactRefs = append(request.ArtifactRefs, ArtifactRef(reference))
	}
	if delivery.ExternalKey != "" {
		key := delivery.ExternalKey
		request.ExternalKey = &key
	}
	if delivery.Details != "" {
		details := delivery.Details
		request.Details = &details
	}
	if delivery.WorkerObservedAt != nil {
		observedAt := delivery.WorkerObservedAt.UnixMilli()
		if observedAt < 0 {
			return ReportRequestParams{}, errors.New("invalid durable report observation time")
		}
		request.ObservedAtMs = &observedAt
	}
	if len([]byte(request.Summary))+len([]byte(delivery.Details)) > MaxReportBytes {
		return ReportRequestParams{}, errors.New("durable report exceeds Comis byte limit")
	}
	document := ReportRequest{JSONRPC: JSONRPCVersion, ID: request.OperationID, Method: MethodManagedRunsReport, Params: request}
	if err := validateGeneratedDocument(schemaReportRequest, document); err != nil {
		return ReportRequestParams{}, fmt.Errorf("invalid durable Comis report: %w", err)
	}
	return request, nil
}

func reportForwarderKind(kind domain.WorkerReportKind) (ReportKind, error) {
	switch kind {
	case domain.ReportProgress:
		return ReportKindProgress, nil
	case domain.ReportDecision:
		return ReportKindAttention, nil
	case domain.ReportBlocked:
		return ReportKindBlocked, nil
	case domain.ReportPaused:
		return ReportKindPaused, nil
	case domain.ReportCandidateComplete:
		return ReportKindCandidateComplete, nil
	case domain.ReportFailed:
		return ReportKindFailed, nil
	case domain.ReportResolution:
		return ReportKindResolution, nil
	default:
		return "", errors.New("invalid durable report kind")
	}
}

func reportForwarderAcknowledgement(
	delivery application.ComisReportDelivery,
	result ReportResponseResult,
	deliveredAt time.Time,
) (application.ComisReportAcknowledgement, error) {
	document := ReportResponse{JSONRPC: JSONRPCVersion, ID: OperationID(delivery.OperationID), Result: result}
	if err := validateGeneratedDocument(schemaReportResponse, document); err != nil {
		return application.ComisReportAcknowledgement{}, fmt.Errorf("invalid Comis report acknowledgement: %w", err)
	}
	if string(result.ManagedRunID) != delivery.ManagedRunID || string(result.ServiceReportID) != delivery.ServiceReportID {
		return application.ComisReportAcknowledgement{}, errors.New("comis report acknowledgement identity differs")
	}
	retainedUntil := time.UnixMilli(result.RetainedUntilMs).UTC()
	if deliveredAt.IsZero() || deliveredAt.Location() != time.UTC || !retainedUntil.After(deliveredAt) {
		return application.ComisReportAcknowledgement{}, errors.New("comis report acknowledgement retention is invalid")
	}
	return application.ComisReportAcknowledgement{
		ManagedRunID: string(result.ManagedRunID), ServiceReportID: string(result.ServiceReportID),
		AcceptedSequence: result.AcceptedSequence, RetainedUntil: retainedUntil,
	}, nil
}

func nextReportBackoff(current, maximum time.Duration) time.Duration {
	if current >= maximum || current > maximum/2 {
		return maximum
	}
	return current * 2
}
