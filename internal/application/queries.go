package application

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/comisai/comis-dev-crew/internal/domain"
)

// Clock supplies deterministic observation time to read projections.
type Clock func() time.Time

// Queries owns every canonical read-only application handler.
type Queries struct {
	repository Repository
	clock      Clock
}

// NewQueries validates and binds the read-side dependencies.
func NewQueries(repository Repository, clock Clock) (*Queries, error) {
	if repository == nil {
		return nil, errors.New("create queries: repository is required")
	}
	if clock == nil {
		return nil, errors.New("create queries: clock is required")
	}
	return &Queries{repository: repository, clock: clock}, nil
}

// Diagnose returns bounded readiness without treating absent host integration as healthy.
func (queries *Queries) Diagnose(ctx context.Context) (DiagnosticReport, error) {
	stateVersion, err := queries.repository.CurrentStateVersion(ctx)
	if err != nil {
		return DiagnosticReport{}, translateReadError(err, "diagnostic state")
	}
	now := queries.now()
	return DiagnosticReport{
		SchemaVersion: 1,
		CapturedAtMs:  now.UnixMilli(),
		StateVersion:  stateVersion,
		Completeness:  CompletenessPartial,
		ServiceHealth: HealthHealthy,
		ComisHealth:   HealthUnavailable,
		Checks: []DiagnosticCheck{
			{Name: "service", Status: CheckPass, Message: "service query handler is available", Hint: "none"},
			{Name: "store", Status: CheckPass, Message: "durable state is readable", Hint: "none"},
			{Name: "host_integration", Status: CheckUnknown, Message: "host integration is unavailable", Hint: "install and configure a compatible host integration"},
		},
	}, nil
}

// Fleet returns the canonical current E0 fleet snapshot.
func (queries *Queries) Fleet(ctx context.Context) (FleetSnapshot, error) {
	tasks, stateVersion, err := queries.taskSnapshot(ctx)
	if err != nil {
		return FleetSnapshot{}, err
	}
	now := queries.now()
	return FleetSnapshot{
		SchemaVersion: 1,
		CapturedAtMs:  now.UnixMilli(),
		StateVersion:  stateVersion,
		Completeness:  CompletenessPartial,
		ServiceHealth: HealthHealthy,
		ComisHealth:   HealthUnavailable,
		Tasks:         projectTasks(tasks, now),
	}, nil
}

// ListTasks returns the same task rows used by the fleet projection.
func (queries *Queries) ListTasks(ctx context.Context) (TaskList, error) {
	tasks, stateVersion, err := queries.taskSnapshot(ctx)
	if err != nil {
		return TaskList{}, err
	}
	now := queries.now()
	return TaskList{
		SchemaVersion: 1,
		CapturedAtMs:  now.UnixMilli(),
		StateVersion:  stateVersion,
		Completeness:  CompletenessPartial,
		Tasks:         projectTasks(tasks, now),
	}, nil
}

// ShowTask returns durable detail for one validated task handle.
func (queries *Queries) ShowTask(ctx context.Context, handle string) (TaskDetail, error) {
	if err := domain.ValidateTaskHandle(handle); err != nil {
		return TaskDetail{}, invalidReferenceFailure("task handle", err)
	}
	task, err := queries.repository.GetTask(ctx, handle)
	if err != nil {
		return TaskDetail{}, translateReadError(err, "task")
	}
	now := queries.now()
	return TaskDetail{
		SchemaVersion:     1,
		CapturedAtMs:      now.UnixMilli(),
		Completeness:      CompletenessPartial,
		Summary:           projectTask(task, now),
		Shape:             task.Shape,
		BaseRevision:      task.BaseRevision,
		BriefRevision:     task.BriefRevision,
		ValidationProfile: task.ValidationProfile,
		DeliveryMode:      task.DeliveryMode,
		ReportCursor:      task.ReportCursor,
		StateVersion:      task.StateVersion,
		CreatedAtMs:       task.CreatedAt.UnixMilli(),
		UpdatedAtMs:       task.UpdatedAt.UnixMilli(),
	}, nil
}

// ExplainTask returns a reasoned, bounded task posture from durable state.
func (queries *Queries) ExplainTask(ctx context.Context, handle string) (TaskExplanation, error) {
	if err := domain.ValidateTaskHandle(handle); err != nil {
		return TaskExplanation{}, invalidReferenceFailure("task handle", err)
	}
	task, err := queries.repository.GetTask(ctx, handle)
	if err != nil {
		return TaskExplanation{}, translateReadError(err, "task")
	}
	now := queries.now()
	reason, explanation, rootCause, actions := explainState(task.State)
	return TaskExplanation{
		SchemaVersion:   1,
		CapturedAtMs:    now.UnixMilli(),
		Completeness:    CompletenessPartial,
		Summary:         projectTask(task, now),
		ReasonCode:      reason,
		Explanation:     explanation,
		LikelyRootCause: rootCause,
		NextSafeActions: actions,
	}, nil
}

// Operation returns one durable reconciliation record by stable operation ID.
func (queries *Queries) Operation(ctx context.Context, id string) (OperationView, error) {
	if err := domain.ValidateOperationID(id); err != nil {
		return OperationView{}, invalidReferenceFailure("operation ID", err)
	}
	operation, err := queries.repository.GetOperation(ctx, id)
	if err != nil {
		return OperationView{}, translateReadError(err, "operation")
	}
	return OperationView{
		SchemaVersion: operation.SchemaVersion,
		CapturedAtMs:  queries.now().UnixMilli(),
		OperationID:   operation.ID,
		Command:       operation.Command,
		SubjectDigest: operation.SubjectDigest,
		Status:        operation.Status,
		ErrorCode:     operation.ErrorCode,
		StateVersion:  operation.StateVersion,
		CreatedAtMs:   operation.CreatedAt.UnixMilli(),
		UpdatedAtMs:   operation.UpdatedAt.UnixMilli(),
	}, nil
}

func (queries *Queries) taskSnapshot(ctx context.Context) ([]domain.Task, int64, error) {
	tasks, err := queries.repository.ListTasks(ctx)
	if err != nil {
		return nil, 0, translateReadError(err, "task list")
	}
	stateVersion, err := queries.repository.CurrentStateVersion(ctx)
	if err != nil {
		return nil, 0, translateReadError(err, "task state version")
	}
	return tasks, stateVersion, nil
}

func (queries *Queries) now() time.Time {
	return queries.clock().UTC()
}

func projectTasks(tasks []domain.Task, now time.Time) []TaskSummary {
	projected := make([]TaskSummary, 0, len(tasks))
	for _, task := range tasks {
		projected = append(projected, projectTask(task, now))
	}
	return projected
}

func projectTask(task domain.Task, now time.Time) TaskSummary {
	reason, _, _, actions := explainState(task.State)
	elapsed := now.Sub(task.CreatedAt).Milliseconds()
	if elapsed < 0 {
		elapsed = 0
	}
	return TaskSummary{
		TaskHandle:       task.Handle,
		State:            task.State,
		StateReason:      reason,
		StateSource:      StateSourceStore,
		StateConfidence:  ConfidenceVerified,
		Freshness:        FreshnessCurrent,
		Custody:          "unknown",
		WorkerProfileID:  task.WorkerProfileID,
		RepositoryID:     task.RepositoryID,
		Head:             "unknown",
		Activity:         "unknown",
		Processes:        "unknown",
		Validation:       "unknown",
		BlockedBy:        "unknown",
		Attention:        "unknown",
		ElapsedMs:        elapsed,
		LastActivityAtMs: task.UpdatedAt.UnixMilli(),
		NextSafeActions:  actions,
	}
}

func translateReadError(err error, subject string) error {
	switch {
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return newSafeFailure(domain.ErrorDeadlineExceeded, true, "query did not complete", "retry the read request", err)
	case errors.Is(err, ErrNotFound):
		return newSafeFailure(domain.ErrorNotFound, false, subject+" not found", "verify the opaque reference and retry", err)
	default:
		return newSafeFailure(domain.ErrorInternal, true, "durable state is unavailable", "inspect service health before retrying", err)
	}
}

func invalidReferenceFailure(subject string, cause error) error {
	return newSafeFailure(domain.ErrorInvalidArgument, false, "invalid "+subject, "use a bounded opaque identifier", cause)
}

func newSafeFailure(code domain.ErrorCode, retryable bool, message, hint string, cause error) error {
	failure, err := domain.NewFailure(code, retryable, message, hint, cause)
	if err != nil {
		return fmt.Errorf("construct safe application failure: %w", err)
	}
	return failure
}

func explainState(state domain.TaskState) (string, string, string, []NextAction) {
	switch state {
	case domain.TaskPrepared:
		return "task_prepared", "Task is prepared and has not started.", "No worker has been started for this task.", []NextAction{ActionInspectTask, ActionStartTask}
	case domain.TaskBlocked:
		return "task_blocked", "Task progress is blocked.", "A required input or external condition is unresolved.", []NextAction{ActionInspectTask, ActionResolveBlock}
	case domain.TaskUnknown:
		return "task_unknown", "Task posture cannot be proven from current evidence.", "One or more authoritative observations are missing or contradictory.", []NextAction{ActionInspectHealth, ActionReconcileTask}
	case domain.TaskFailed, domain.TaskCancelled, domain.TaskCleanupHeld:
		return "task_" + string(state), "Task requires operator review.", "The durable state records a terminal or safety-held posture.", []NextAction{ActionInspectTask}
	case domain.TaskDelivered, domain.TaskCleaned:
		return "task_" + string(state), "Task reached a completed durable posture.", "The durable state records successful completion of this stage.", []NextAction{ActionNone}
	default:
		return "task_" + string(state), "Task is progressing through its durable lifecycle.", "No durable blocking reason is recorded.", []NextAction{ActionInspectTask}
	}
}
