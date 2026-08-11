package application

import (
	"context"
	"time"
)

// ComisEvidenceVerification is the closed producer-side verification level.
type ComisEvidenceVerification string

const (
	ComisEvidenceReported        ComisEvidenceVerification = "reported"
	ComisEvidenceAdapterVerified ComisEvidenceVerification = "adapter_verified"
)

// ComisEvidenceDeliveryKind selects host-owned delivery behavior.
type ComisEvidenceDeliveryKind string

const (
	ComisEvidenceReference  ComisEvidenceDeliveryKind = "reference"
	ComisEvidenceAttachment ComisEvidenceDeliveryKind = "attachment"
)

// ComisEvidenceDeliveryRequest contains only bounded delivery metadata. The
// immutable body remains the publication authority.
type ComisEvidenceDeliveryRequest struct {
	Kind      ComisEvidenceDeliveryKind
	FileName  string
	MediaType string
}

// ComisEvidencePublication is one immutable, task-bound evidence item stored
// before candidate completion becomes externally reportable.
type ComisEvidencePublication struct {
	OperationID       string
	TaskHandle        string
	EvidenceRef       string
	Kind              string
	SubjectDigest     string
	ObservedAt        time.Time
	ExpiresAt         *time.Time
	ContentHash       string
	VerificationLevel ComisEvidenceVerification
	Body              []byte
	Delivery          *ComisEvidenceDeliveryRequest
}

// ComisEvidenceDelivery is one already-durable publication ready for the
// authenticated persistent control connection.
type ComisEvidenceDelivery struct {
	ComisEvidencePublication
	ManagedRunID string
	StateVersion int64
}

// ComisEvidenceAcknowledgement is the exact host retention evidence persisted
// before a publication is considered delivered.
type ComisEvidenceAcknowledgement struct {
	ManagedRunID      string
	EvidenceRef       string
	ContentHash       string
	VerificationLevel ComisEvidenceVerification
	RetainedUntil     *time.Time
}

// ComisEvidenceOutbox is the service-owned durable evidence delivery port.
type ComisEvidenceOutbox interface {
	NextComisEvidence(context.Context) (ComisEvidenceDelivery, bool, error)
	MarkComisEvidenceDelivered(context.Context, string, ComisEvidenceAcknowledgement, time.Time) error
}
