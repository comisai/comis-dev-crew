package application

import "github.com/comisai/comis-dev-crew/internal/domain"

// Completeness states whether a read projection includes all configured sources.
type Completeness string

const (
	CompletenessComplete Completeness = "complete"
	CompletenessPartial  Completeness = "partial"
	CompletenessUnknown  Completeness = "unknown"
)

// HealthStatus is the closed health vocabulary for service projections.
type HealthStatus string

const (
	HealthHealthy     HealthStatus = "healthy"
	HealthDegraded    HealthStatus = "degraded"
	HealthUnavailable HealthStatus = "unavailable"
	HealthUnknown     HealthStatus = "unknown"
)

// CheckStatus is the bounded diagnostic outcome vocabulary.
type CheckStatus string

const (
	CheckPass    CheckStatus = "pass"
	CheckWarning CheckStatus = "warning"
	CheckFail    CheckStatus = "fail"
	CheckUnknown CheckStatus = "unknown"
)

// StateSource names the authority behind a projected task state.
type StateSource string

const StateSourceStore StateSource = "durable_store"

// Confidence states how strongly a source supports a projected observation.
type Confidence string

const (
	ConfidenceVerified Confidence = "verified"
	ConfidenceUnknown  Confidence = "unknown"
)

// Freshness states whether a durable observation is current at snapshot time.
type Freshness string

const (
	FreshnessCurrent Freshness = "current"
	FreshnessUnknown Freshness = "unknown"
)

// NextAction is a closed, non-executable operator action identifier.
type NextAction string

const (
	ActionInspectTask   NextAction = "inspect_task"
	ActionStartTask     NextAction = "start_task"
	ActionResolveBlock  NextAction = "resolve_block"
	ActionInspectHealth NextAction = "inspect_service_health"
	ActionReconcileTask NextAction = "reconcile_task"
	ActionPrepareTask   NextAction = "prepare_task"
	ActionNone          NextAction = "none"
)

// TaskRecoveryEvidenceKind is the closed durable explanation of why an
// unknown task can or cannot enter clean-candidate reconciliation.
type TaskRecoveryEvidenceKind string

const (
	RecoveryTerminalSettledWithoutCandidate TaskRecoveryEvidenceKind = "terminal_settled_without_candidate"
	RecoveryRestartEvidenceUnresolved       TaskRecoveryEvidenceKind = "restart_evidence_unresolved"
	RecoveryRuntimeRelayIdentityUnproven    TaskRecoveryEvidenceKind = "runtime_relay_identity_unproven"
)

// TaskRecoveryEvidence carries server-owned recovery authority to the query
// layer. Authority is populated only for a settled terminal without a worker
// candidate report.
type TaskRecoveryEvidence struct {
	Kind      TaskRecoveryEvidenceKind
	Authority TaskReconciliationAuthority
}

// DiagnosticCheck is one bounded readiness observation.
type DiagnosticCheck struct {
	Name    string      `json:"name"`
	Status  CheckStatus `json:"status"`
	Message string      `json:"message"`
	Hint    string      `json:"hint"`
}

// DiagnosticReport is the stable doctor projection.
type DiagnosticReport struct {
	SchemaVersion int               `json:"schemaVersion"`
	CapturedAtMs  int64             `json:"capturedAtMs"`
	StateVersion  int64             `json:"stateVersion"`
	Completeness  Completeness      `json:"completeness"`
	ServiceHealth HealthStatus      `json:"serviceHealth"`
	ComisHealth   HealthStatus      `json:"comisHealth"`
	Checks        []DiagnosticCheck `json:"checks"`
}

// TaskSummary is the canonical bounded row shared by fleet, list, show, and explain.
type TaskSummary struct {
	TaskHandle       string           `json:"taskHandle"`
	State            domain.TaskState `json:"state"`
	StateReason      string           `json:"stateReason"`
	StateSource      StateSource      `json:"stateSource"`
	StateConfidence  Confidence       `json:"stateConfidence"`
	Freshness        Freshness        `json:"freshness"`
	Custody          string           `json:"custody"`
	WorkerProfileID  string           `json:"workerProfileId"`
	RepositoryID     string           `json:"repositoryId"`
	Head             string           `json:"head"`
	Activity         string           `json:"activity"`
	Processes        string           `json:"processes"`
	Validation       string           `json:"validation"`
	BlockedBy        string           `json:"blockedBy"`
	Attention        string           `json:"attention"`
	StateVersion     int64            `json:"stateVersion"`
	ElapsedMs        int64            `json:"elapsedMs"`
	LastActivityAtMs int64            `json:"lastActivityAtMs"`
	NextSafeActions  []NextAction     `json:"nextSafeActions"`
}

// FleetSnapshot is the canonical current E0 fleet projection.
type FleetSnapshot struct {
	SchemaVersion int           `json:"schemaVersion"`
	CapturedAtMs  int64         `json:"capturedAtMs"`
	StateVersion  int64         `json:"stateVersion"`
	Completeness  Completeness  `json:"completeness"`
	ServiceHealth HealthStatus  `json:"serviceHealth"`
	ComisHealth   HealthStatus  `json:"comisHealth"`
	Tasks         []TaskSummary `json:"tasks"`
}

// TaskList is the versioned task-list projection.
type TaskList struct {
	SchemaVersion int           `json:"schemaVersion"`
	CapturedAtMs  int64         `json:"capturedAtMs"`
	StateVersion  int64         `json:"stateVersion"`
	Completeness  Completeness  `json:"completeness"`
	Tasks         []TaskSummary `json:"tasks"`
}

// TaskDetail is the durable E0 task record plus its shared summary.
type TaskDetail struct {
	SchemaVersion     int                 `json:"schemaVersion"`
	CapturedAtMs      int64               `json:"capturedAtMs"`
	Completeness      Completeness        `json:"completeness"`
	Summary           TaskSummary         `json:"summary"`
	Evidence          TaskEvidenceView    `json:"evidence"`
	Shape             domain.TaskShape    `json:"shape"`
	BaseRevision      string              `json:"baseRevision"`
	BriefRevision     int64               `json:"briefRevision"`
	ValidationProfile string              `json:"validationProfile"`
	DeliveryMode      domain.DeliveryMode `json:"deliveryMode"`
	ReportCursor      int64               `json:"reportCursor"`
	// Present only when a worker was swapped. It answers the question the task
	// row cannot: this brief revision is current because a different worker took
	// over, not because the contract changed.
	Replacement *TaskReplacementView `json:"replacement,omitempty"`
	// Present only on a ship task that came from a scout. It names the
	// investigation this work is justified by, which nothing else on the task
	// records — a promoted ship task is otherwise indistinguishable from one
	// somebody wrote by hand.
	Promotion    *TaskPromotionView `json:"promotion,omitempty"`
	StateVersion int64              `json:"stateVersion"`
	CreatedAtMs  int64              `json:"createdAtMs"`
	UpdatedAtMs  int64              `json:"updatedAtMs"`
}

// TaskExplanation provides a content-free reason and next safe actions.
type TaskExplanation struct {
	SchemaVersion   int              `json:"schemaVersion"`
	CapturedAtMs    int64            `json:"capturedAtMs"`
	Completeness    Completeness     `json:"completeness"`
	Summary         TaskSummary      `json:"summary"`
	Evidence        TaskEvidenceView `json:"evidence"`
	ReasonCode      string           `json:"reasonCode"`
	Explanation     string           `json:"explanation"`
	LikelyRootCause string           `json:"likelyRootCause"`
	NextSafeActions []NextAction     `json:"nextSafeActions"`
}

// OperationView is the stable reconciliation projection for a durable operation.
type OperationView struct {
	SchemaVersion int                    `json:"schemaVersion"`
	CapturedAtMs  int64                  `json:"capturedAtMs"`
	OperationID   string                 `json:"operationId"`
	Command       string                 `json:"command"`
	SubjectDigest string                 `json:"subjectDigest"`
	Status        domain.OperationStatus `json:"status"`
	ErrorCode     domain.ErrorCode       `json:"errorCode,omitempty"`
	StateVersion  int64                  `json:"stateVersion"`
	CreatedAtMs   int64                  `json:"createdAtMs"`
	UpdatedAtMs   int64                  `json:"updatedAtMs"`
}

// LaunchPlan is the safe reviewed launch-requirements projection. The opaque
// run and lease handles are included because the Comis terminal API requires
// both to select the server-owned workspace and attachment authority.
// Executable, argv, environment, working-directory, and host attachment source
// fields are deliberately absent.
type LaunchPlan struct {
	SchemaVersion        int              `json:"schemaVersion"`
	CapturedAtMs         int64            `json:"capturedAtMs"`
	StateVersion         int64            `json:"stateVersion"`
	Completeness         Completeness     `json:"completeness"`
	TaskHandle           string           `json:"taskHandle"`
	State                domain.TaskState `json:"state"`
	StateSource          StateSource      `json:"stateSource"`
	StateConfidence      Confidence       `json:"stateConfidence"`
	Freshness            Freshness        `json:"freshness"`
	WorkerProfileID      string           `json:"workerProfileId"`
	TerminalAllowEntryID string           `json:"terminalAllowEntryId"`
	ManagedRunID         string           `json:"managedRunId"`
	WorkspaceLeaseID     string           `json:"workspaceLeaseId"`
	BriefRevisionHash    string           `json:"briefRevisionHash"`
	AttachmentTargetName string           `json:"attachmentTargetName"`
}

// WorkerProfileSummary is one reviewed dispatch configuration as the read side
// renders it. It carries the identity and posture an operator or liaison needs
// to pick a profile or explain why none is usable — never the executable, the
// argument vector, or the environment allowlist, which are launch authority and
// belong only to the adapter that builds the descriptor.
type WorkerProfileSummary struct {
	ProfileID     string             `json:"profileId"`
	Harness       string             `json:"harness"`
	AllowedShapes []domain.TaskShape `json:"allowedShapes"`
	Availability  string             `json:"availability"`
	// A closed reason code, present only when the profile is not available.
	AvailabilityReason string `json:"availabilityReason,omitempty"`
	// False when the harness cannot prove a worker turn settled. Such a profile
	// is usable but must never be trusted unattended, so the posture is reported
	// rather than inferred from availability.
	Unattended       bool `json:"unattended"`
	ConcurrencyLimit int  `json:"concurrencyLimit"`
}

// WorkerProfileList is the canonical bounded catalog read. It carries the same
// state version the envelope does: the transport treats that value as the
// connection's read-after-write token and checks the two agree, so a projection
// without one cannot travel over the local API at all.
type WorkerProfileList struct {
	SchemaVersion int                    `json:"schemaVersion"`
	CapturedAtMs  int64                  `json:"capturedAtMs"`
	StateVersion  int64                  `json:"stateVersion"`
	Profiles      []WorkerProfileSummary `json:"profiles"`
}

// WorkerProfileCatalog lists the operator-reviewed profiles this deployment
// configured. It is a read: it never ranks, selects, or falls back.
type WorkerProfileCatalog func() []WorkerProfileSummary

// TaskReplacementView is the bounded projection of one worker swap. It names the
// profiles and the tree the new worker inherited, and nothing about why: the
// reason is an operator's, not the service's, and inventing one here would put
// words in their mouth.
type TaskReplacementView struct {
	PreviousWorkerProfileID string               `json:"previousWorkerProfileId"`
	WorkerProfileID         string               `json:"workerProfileId"`
	HeadRevision            string               `json:"headRevision"`
	Cleanliness             WorkspaceCleanliness `json:"cleanliness"`
	BriefRevision           int64                `json:"briefRevision"`
	ObservedAtMs            int64                `json:"observedAtMs"`
}

// TaskPromotionView names the scout one ship task came from and the exact
// evidence digest that justified promoting it.
type TaskPromotionView struct {
	ScoutTaskHandle string `json:"scoutTaskHandle"`
	EvidenceDigest  string `json:"evidenceDigest"`
	PromotedAtMs    int64  `json:"promotedAtMs"`
}
