package application

import (
	"context"
	"time"

	"github.com/comisai/comis-dev-crew/internal/domain"
)

// ComisReportDelivery is one already-durable sparse report ready for the
// authenticated Comis connection. Both wire identities are stable on replay.
type ComisReportDelivery struct {
	OperationID      string
	TaskHandle       string
	LocalReportID    string
	ManagedRunID     string
	ServiceReportID  string
	Kind             domain.WorkerReportKind
	ExternalKey      string
	Summary          string
	Details          string
	WorkerObservedAt *time.Time
	StateVersion     int64
}

// ComisReportAcknowledgement is the exact host retention evidence persisted
// before an outbox item is considered delivered.
type ComisReportAcknowledgement struct {
	ManagedRunID     string
	ServiceReportID  string
	AcceptedSequence int64
	RetainedUntil    time.Time
}

// ComisReportOutbox is the service-owned durable delivery port.
type ComisReportOutbox interface {
	NextComisReport(context.Context) (ComisReportDelivery, bool, error)
	MarkComisReportDelivered(context.Context, string, ComisReportAcknowledgement, time.Time) error
}
