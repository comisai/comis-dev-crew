package application

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/comisai/comis-dev-crew/internal/domain"
)

// ReportMutation is the exact authenticated report and canonical replay
// subject supplied to one atomic store transaction.
type ReportMutation struct {
	Report        domain.AuthenticatedReport
	SubjectDigest string
	AcceptedAt    time.Time
}

// ReportMutationStore is the service-owned durable report transaction port.
type ReportMutationStore interface {
	CommitReport(context.Context, ReportMutation) (domain.ReportReceipt, error)
}

// ReportSinkConfig supplies deterministic report acceptance dependencies.
type ReportSinkConfig struct {
	Store ReportMutationStore
	Clock Clock
}

// ReportSink adapts the authenticated reporter seam to the canonical store.
type ReportSink struct {
	store ReportMutationStore
	clock Clock
}

// NewReportSink creates the application-owned authenticated report consumer.
func NewReportSink(config ReportSinkConfig) (*ReportSink, error) {
	if config.Store == nil || config.Clock == nil {
		return nil, errors.New("create report sink: store and clock are required")
	}
	return &ReportSink{store: config.Store, clock: config.Clock}, nil
}

// AcceptReport validates, canonically digests, and delegates one durable report.
func (sink *ReportSink) AcceptReport(ctx context.Context, report domain.AuthenticatedReport) (domain.ReportReceipt, error) {
	if ctx == nil {
		return domain.ReportReceipt{}, errors.New("accept report: context is required")
	}
	if err := ctx.Err(); err != nil {
		return domain.ReportReceipt{}, err
	}
	if err := domain.ValidateTaskHandle(report.TaskHandle); err != nil {
		return domain.ReportReceipt{}, mutationValidationFailure("report task scope is invalid")
	}
	if err := report.Report.Validate(); err != nil {
		return domain.ReportReceipt{}, mutationValidationFailure("worker report is invalid")
	}
	encoded, err := json.Marshal(report)
	if err != nil {
		return domain.ReportReceipt{}, mutationValidationFailure("report subject cannot be encoded")
	}
	digest := fmt.Sprintf("%x", sha256.Sum256(encoded))
	return sink.store.CommitReport(ctx, ReportMutation{Report: report, SubjectDigest: digest, AcceptedAt: sink.clock()})
}
