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

// DeliveryMode is the closed E0 delivery set. Local branch and merge modes are
// intentionally absent until E1.
type DeliveryMode string

const (
	DeliveryPullRequest DeliveryMode = "pull_request"
	DeliveryReport      DeliveryMode = "report"
)

func (mode DeliveryMode) valid() bool {
	return mode == DeliveryPullRequest || mode == DeliveryReport
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

// Task is the pure E0 durable domain record. Comis protocol DTOs do not appear
// here; managed-run binding is added through an adapter-owned join after handoff.
type Task struct {
	SchemaVersion     int
	Handle            string
	State             TaskState
	Shape             TaskShape
	RepositoryID      string
	BaseRevision      string
	BriefRevision     int64
	ValidationProfile string
	DeliveryMode      DeliveryMode
	WorkerProfileID   string
	ReportCursor      int64
	StateVersion      int64
	CreatedAt         time.Time
	UpdatedAt         time.Time
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
	if !task.State.valid() {
		return &ValidationError{Field: "state", Reason: "must be a known E0 task state"}
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
	if !task.DeliveryMode.valid() {
		return &ValidationError{Field: "deliveryMode", Reason: "must be an E0 delivery mode"}
	}
	if task.Shape == ShapeShip && task.DeliveryMode != DeliveryPullRequest {
		return &ValidationError{Field: "deliveryMode", Reason: "ship tasks require pull_request in E0"}
	}
	if task.Shape == ShapeScout && task.DeliveryMode != DeliveryReport {
		return &ValidationError{Field: "deliveryMode", Reason: "scout tasks require report in E0"}
	}
	if task.ReportCursor < 0 {
		return &ValidationError{Field: "reportCursor", Reason: "must not be negative"}
	}
	if task.StateVersion < 1 {
		return &ValidationError{Field: "stateVersion", Reason: "must be positive"}
	}
	return validateTimes(task.CreatedAt, task.UpdatedAt)
}
