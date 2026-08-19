package application

import (
	"context"
	"errors"
	"time"
)

// AuditEventKind is the closed set of security-relevant facts the service keeps
// beyond the transition log.
//
// The transition log answers what the fleet is doing. These answer who was
// refused and what was rejected — facts that change no task state and would
// otherwise leave no trace at all, which is precisely the trace an operator
// needs when reconstructing an incident.
type AuditEventKind string

const (
	// AuditCleanupRefused is one refused destructive-removal safety check.
	AuditCleanupRefused AuditEventKind = "cleanup_refused"
	// AuditReportAuthenticationFailed is one rejected worker credential.
	AuditReportAuthenticationFailed AuditEventKind = "report_authentication_failed"
)

// Valid reports whether the kind is one this service can produce.
func (kind AuditEventKind) Valid() bool {
	switch kind {
	case AuditCleanupRefused, AuditReportAuthenticationFailed:
		return true
	default:
		return false
	}
}

// AuditReason is the closed ground for one audited outcome. It is a code, never
// prose, so the trail stays content-free and machine-readable.
type AuditReason string

const (
	AuditCleanupOpenHold         AuditReason = "open_hold"
	AuditCleanupOpenDecision     AuditReason = "open_decision"
	AuditCleanupUnattestedScout  AuditReason = "unattested_scout"
	AuditCleanupActiveExecution  AuditReason = "active_execution"
	AuditCleanupUnknownExecution AuditReason = "unknown_execution"
	AuditCleanupEvidenceMissing  AuditReason = "evidence_missing"
	AuditCredentialMismatch      AuditReason = "credential_mismatch"
)

// Valid reports whether the reason is one this service can produce.
func (reason AuditReason) Valid() bool {
	switch reason {
	case AuditCleanupOpenHold, AuditCleanupOpenDecision, AuditCleanupUnattestedScout,
		AuditCleanupActiveExecution, AuditCleanupUnknownExecution, AuditCleanupEvidenceMissing,
		AuditCredentialMismatch:
		return true
	default:
		return false
	}
}

// AuditEvent is one durable content-free security record.
type AuditEvent struct {
	Sequence   int64          `json:"sequence"`
	OccurredAt time.Time      `json:"occurredAt"`
	Kind       AuditEventKind `json:"kind"`
	TaskHandle string         `json:"taskHandle,omitempty"`
	Reason     AuditReason    `json:"reason"`
}

// Validate rejects a record that could not be acted on.
func (event AuditEvent) Validate() error {
	if !event.Kind.Valid() {
		return errors.New("validate audit event: kind is invalid")
	}
	if !event.Reason.Valid() {
		return errors.New("validate audit event: reason is invalid")
	}
	if event.OccurredAt.IsZero() {
		return errors.New("validate audit event: observation time is required")
	}
	return nil
}

// AuditRecorder persists one security record. It is deliberately separate from
// the mutation stores: an audited refusal must outlive the transaction that was
// refused, so it can never share that transaction's fate.
type AuditRecorder interface {
	RecordAuditEvent(context.Context, AuditEvent) error
}

// AuditReader reads the durable trail from a cursor.
type AuditReader interface {
	ReadAuditEvents(context.Context, int64, int) ([]AuditEvent, error)
}

// MaximumAuditPage bounds one audit page. A caller may ask for less; asking for
// more is capped rather than refused.
const MaximumAuditPage = 200

// defaultAuditPage is used when a caller states no preference.
const defaultAuditPage = 100

// AuditPage is one bounded, resumable slice of the durable audit trail.
type AuditPage struct {
	SchemaVersion int          `json:"schemaVersion"`
	CapturedAt    time.Time    `json:"capturedAt"`
	NextCursor    int64        `json:"nextCursor"`
	Events        []AuditEvent `json:"events"`
}
