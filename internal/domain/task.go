package domain

import "time"

// TaskShape is the closed E0 development-task shape.
type TaskShape string

const (
	ShapeShip  TaskShape = "ship"
	ShapeScout TaskShape = "scout"
)

func (shape TaskShape) valid() bool {
	return shape == ShapeShip || shape == ShapeScout
}

// DeliveryMode is the closed delivery set.
type DeliveryMode string

const (
	DeliveryPullRequest        DeliveryMode = "pull_request"
	DeliveryLocalBranch        DeliveryMode = "local_branch"
	DeliveryReport             DeliveryMode = "report"
	DeliveryMergeAfterApproval DeliveryMode = "merge_after_approval"
)

func (mode DeliveryMode) valid() bool {
	switch mode {
	case DeliveryPullRequest, DeliveryReport:
		return true
	}
	return false
}

// ValidForShape reports whether one shape may deliver through this mode.
func (mode DeliveryMode) ValidForShape(shape TaskShape) bool {
	if !mode.valid() || !shape.valid() {
		return false
	}
	if shape == ShapeScout {
		return mode == DeliveryReport
	}
	return mode == DeliveryPullRequest
}

// RequiresMergeAuthority reports whether an accepted delivery mode needs the
// separate merge credential. No E0 delivery mode does.
func (mode DeliveryMode) RequiresMergeAuthority() bool {
	return false
}

// TaskState is the closed E0 lifecycle. Unknown is a durable state, not a
// synonym for idle or failure.
type TaskState string

const (
	TaskPrepared          TaskState = "prepared"
	TaskReady             TaskState = "ready"
	TaskLaunching         TaskState = "launching"
	TaskWorking           TaskState = "working"
	TaskAwaitingDecision  TaskState = "awaiting_decision"
	TaskBlocked           TaskState = "blocked"
	TaskPaused            TaskState = "paused"
	TaskReconciling       TaskState = "reconciling"
	TaskValidating        TaskState = "validating"
	TaskCandidateComplete TaskState = "candidate_complete"
	TaskDelivering        TaskState = "delivering"
	TaskDelivered         TaskState = "delivered"
	TaskFailed            TaskState = "failed"
	TaskCancelled         TaskState = "cancelled"
	TaskUnknown           TaskState = "unknown"
	TaskCleanupHeld       TaskState = "cleanup_held"
	TaskCleaned           TaskState = "cleaned"
)

func (state TaskState) valid() bool {
	switch state {
	case TaskPrepared, TaskReady, TaskLaunching, TaskWorking, TaskAwaitingDecision,
		TaskBlocked, TaskPaused, TaskReconciling, TaskValidating, TaskCandidateComplete,
		TaskDelivering, TaskDelivered, TaskFailed, TaskCancelled, TaskUnknown,
		TaskCleanupHeld, TaskCleaned:
		return true
	default:
		return false
	}
}

// SatisfiesInitiativeDependency reports whether a task has accepted candidate
// evidence or a later delivery posture. Initiative integration consumes the
// accepted head; host acknowledgement may settle independently afterward.
func (state TaskState) SatisfiesInitiativeDependency() bool {
	switch state {
	case TaskCandidateComplete, TaskDelivering, TaskDelivered, TaskCleanupHeld, TaskCleaned:
		return true
	default:
		return false
	}
}

// Task is the pure E0 durable domain record. Comis protocol DTOs do not appear
// here; only exact opaque host-authority references cross the adapter boundary.
type Task struct {
	SchemaVersion         int
	Handle                string
	ServiceInstanceID     string
	ManagedRunID          string
	WorkspaceLeaseID      string
	ExecutionAttachmentID string
	AttachmentTargetName  string
	State                 TaskState
	Shape                 TaskShape
	RepositoryID          string
	BaseRevision          string
	BriefRevision         int64
	BriefRevisionHash     string
	AcceptanceCriteria    []string
	Constraints           []string
	ConsumedContracts     []PinnedContract
	ValidationProfile     string
	DeliveryMode          DeliveryMode
	WorkerProfileID       string
	ReportCursor          int64
	StateVersion          int64
	CreatedAt             time.Time
	UpdatedAt             time.Time
}

// Validate enforces the E0 record and shape/delivery relationship.
func (task Task) Validate() error {
	if task.SchemaVersion != 1 {
		return &ValidationError{Field: "schemaVersion", Reason: "must equal 1"}
	}
	identifiers := []struct {
		field string
		value string
	}{
		{field: "handle", value: task.Handle},
		{field: "repositoryId", value: task.RepositoryID},
		{field: "validationProfile", value: task.ValidationProfile},
		{field: "workerProfileId", value: task.WorkerProfileID},
	}
	for _, identifier := range identifiers {
		if err := validateOpaqueID(identifier.field, identifier.value); err != nil {
			return err
		}
	}
	if err := validateAuthorityReference("serviceInstanceId", task.ServiceInstanceID); err != nil {
		return err
	}
	hasRun := task.ManagedRunID != ""
	hasLease := task.WorkspaceLeaseID != ""
	if hasRun != hasLease {
		return &ValidationError{Field: "binding", Reason: "managed run and workspace lease must be present together"}
	}
	if hasRun {
		if err := validateAuthorityReference("managedRunId", task.ManagedRunID); err != nil {
			return err
		}
		if err := validateAuthorityReference("workspaceLeaseId", task.WorkspaceLeaseID); err != nil {
			return err
		}
	}
	hasAttachmentID := task.ExecutionAttachmentID != ""
	hasAttachmentTarget := task.AttachmentTargetName != ""
	if hasAttachmentID != hasAttachmentTarget {
		return &ValidationError{Field: "executionAttachment", Reason: "attachment identity and target must be present together"}
	}
	if hasAttachmentID {
		if err := validateAuthorityReference("executionAttachmentId", task.ExecutionAttachmentID); err != nil {
			return err
		}
		if err := ValidateAttachmentTargetName(task.AttachmentTargetName); err != nil {
			return err
		}
	}
	if !task.State.valid() {
		return &ValidationError{Field: "state", Reason: "must be a known E0 task state"}
	}
	if !task.State.allowsUnboundPreparation() && !hasRun {
		return &ValidationError{Field: "binding", Reason: "active and terminal task states require exact host binding"}
	}
	if !task.Shape.valid() {
		return &ValidationError{Field: "shape", Reason: "must be ship or scout"}
	}
	if err := validateRevision(task.BaseRevision); err != nil {
		return err
	}
	if task.BriefRevision < 1 {
		return &ValidationError{Field: "briefRevision", Reason: "must be positive"}
	}
	if err := validateContractTextList("acceptanceCriteria", task.AcceptanceCriteria, true); err != nil {
		return err
	}
	if err := validateContractTextList("constraints", task.Constraints, false); err != nil {
		return err
	}
	if err := validateSHA256("briefRevisionHash", task.BriefRevisionHash); err != nil {
		return err
	}
	wantBriefHash, err := task.briefRevisionDigest()
	if err != nil {
		return err
	}
	if task.BriefRevisionHash != wantBriefHash {
		return &ValidationError{Field: "briefRevisionHash", Reason: "does not pin the canonical worker brief"}
	}
	if !task.DeliveryMode.valid() {
		return &ValidationError{Field: "deliveryMode", Reason: "must be an E0 delivery mode"}
	}
	if !task.DeliveryMode.ValidForShape(task.Shape) {
		return &ValidationError{
			Field:  "deliveryMode",
			Reason: "ship tasks deliver changes; scout tasks deliver a report",
		}
	}
	if task.ReportCursor < 0 {
		return &ValidationError{Field: "reportCursor", Reason: "must not be negative"}
	}
	if task.StateVersion < 1 {
		return &ValidationError{Field: "stateVersion", Reason: "must be positive"}
	}
	return validateTimes(task.CreatedAt, task.UpdatedAt)
}

func (state TaskState) allowsUnboundPreparation() bool {
	switch state {
	case TaskPrepared, TaskReconciling, TaskUnknown, TaskCancelled, TaskCleanupHeld, TaskCleaned:
		return true
	default:
		return false
	}
}
