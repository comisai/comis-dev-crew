package application

import "github.com/comisai/comis-dev-crew/internal/domain"

// CandidateEvidenceStatus is the closed durable candidate observation posture.
type CandidateEvidenceStatus string

const (
	CandidateEvidenceNone       CandidateEvidenceStatus = "none"
	CandidateEvidenceReconciled CandidateEvidenceStatus = "reconciled"
	CandidateEvidenceJudged     CandidateEvidenceStatus = "judged"
)

// ActivityEvidenceStatus is the closed authenticated-report posture.
type ActivityEvidenceStatus string

const (
	ActivityEvidenceNone                ActivityEvidenceStatus = "none"
	ActivityEvidenceAuthenticatedReport ActivityEvidenceStatus = "authenticated_report"
)

// DecisionEvidenceStatus is the closed decision-block posture.
type DecisionEvidenceStatus string

const (
	DecisionEvidenceNone     DecisionEvidenceStatus = "none"
	DecisionEvidenceOpen     DecisionEvidenceStatus = "open"
	DecisionEvidenceResolved DecisionEvidenceStatus = "resolved"
)

// ValidationEvidenceStatus is the closed durable validation posture.
type ValidationEvidenceStatus string

const (
	ValidationEvidenceNotStarted ValidationEvidenceStatus = "not_started"
	ValidationEvidenceRunning    ValidationEvidenceStatus = "running"
	ValidationEvidenceAccepted   ValidationEvidenceStatus = "accepted"
	ValidationEvidenceRejected   ValidationEvidenceStatus = "rejected"
	ValidationEvidenceUnknown    ValidationEvidenceStatus = "unknown"
)

// DeliveryEvidenceStatus is the closed host-delivery posture.
type DeliveryEvidenceStatus string

const (
	DeliveryEvidenceNotStarted DeliveryEvidenceStatus = "not_started"
	DeliveryEvidencePending    DeliveryEvidenceStatus = "pending"
	DeliveryEvidenceDelivered  DeliveryEvidenceStatus = "delivered"
	DeliveryEvidenceUnknown    DeliveryEvidenceStatus = "unknown"
)

// CleanupEvidenceStatus is the closed cleanup posture.
type CleanupEvidenceStatus string

const (
	CleanupEvidenceNotStarted        CleanupEvidenceStatus = "not_started"
	CleanupEvidenceHeld              CleanupEvidenceStatus = "held"
	CleanupEvidencePrepared          CleanupEvidenceStatus = "prepared"
	CleanupEvidenceHostReleased      CleanupEvidenceStatus = "host_released"
	CleanupEvidenceRemovalAuthorized CleanupEvidenceStatus = "removal_authorized"
	CleanupEvidenceCompleted         CleanupEvidenceStatus = "completed"
	CleanupEvidenceUnknown           CleanupEvidenceStatus = "unknown"
)

// CandidateEvidenceView identifies the current server-verified candidate.
type CandidateEvidenceView struct {
	Status                    CandidateEvidenceStatus `json:"status"`
	HeadRevision              string                  `json:"headRevision"`
	EvidenceDigest            string                  `json:"evidenceDigest"`
	ReconciliationOperationID string                  `json:"reconciliationOperationId"`
}

// ActivityEvidenceView identifies only the latest authenticated sparse report.
type ActivityEvidenceView struct {
	Status       ActivityEvidenceStatus  `json:"status"`
	ReportID     string                  `json:"reportId"`
	ReportKind   domain.WorkerReportKind `json:"reportKind"`
	AcceptedAtMs int64                   `json:"acceptedAtMs"`
}

// DecisionEvidenceView exposes stable decision and resolution references only.
type DecisionEvidenceView struct {
	Status             DecisionEvidenceStatus `json:"status"`
	DecisionReportID   string                 `json:"decisionReportId"`
	ResolutionReportID string                 `json:"resolutionReportId"`
}

// ValidationEvidenceView identifies the latest durable judgment or active check.
type ValidationEvidenceView struct {
	Status             ValidationEvidenceStatus `json:"status"`
	EvidenceDigest     string                   `json:"evidenceDigest"`
	ProcessOperationID string                   `json:"processOperationId"`
}

// DeliveryEvidenceView reconciles durable outbox and forge references.
type DeliveryEvidenceView struct {
	Status              DeliveryEvidenceStatus `json:"status"`
	EvidenceOperationID string                 `json:"evidenceOperationId"`
	EvidenceRef         string                 `json:"evidenceRef"`
	PullRequestID       string                 `json:"pullRequestId"`
}

// CleanupEvidenceView exposes a durable stage and count without hold content.
type CleanupEvidenceView struct {
	Status        CleanupEvidenceStatus `json:"status"`
	OperationID   string                `json:"operationId"`
	OpenHoldCount int                   `json:"openHoldCount"`
}

// TaskAuthorityView exposes the stable opaque host references needed to join views.
type TaskAuthorityView struct {
	ManagedRunID           string `json:"managedRunId"`
	WorkspaceLeaseID       string `json:"workspaceLeaseId"`
	ExecutionAttachmentID  string `json:"executionAttachmentId"`
	PreparationOperationID string `json:"preparationOperationId"`
}

// TaskEvidenceView is the bounded content-free diagnostic projection.
type TaskEvidenceView struct {
	Candidate  CandidateEvidenceView  `json:"candidate"`
	Activity   ActivityEvidenceView   `json:"activity"`
	Decision   DecisionEvidenceView   `json:"decision"`
	Validation ValidationEvidenceView `json:"validation"`
	Delivery   DeliveryEvidenceView   `json:"delivery"`
	Cleanup    CleanupEvidenceView    `json:"cleanup"`
	Authority  TaskAuthorityView      `json:"authority"`
}
