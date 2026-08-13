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

// HostIntegrationStatus reports whether the configured authenticated host
// control session is currently established.
type HostIntegrationStatus interface {
	Connected() bool
}

type candidateEvidenceReader interface {
	LatestCandidateEvidence(context.Context, string) (*domain.SealedDeliveryEvidence, domain.CandidateJudgment, error)
}

type taskRecoveryEvidenceReader interface {
	ReadTaskRecoveryEvidence(context.Context, string) (TaskRecoveryEvidence, error)
}

// Queries owns every canonical read-only application handler.
type Queries struct {
	repository               Repository
	harnesses                WorkerHarnessResolver
	host                     HostIntegrationStatus
	reconciliationWorkspaces ReconciliationWorkspaceInspector
	clock                    Clock
}

// QueryConfig supplies durable read authority and the reviewed worker adapter
// registry. Harnesses may be absent only for the legacy read-only service mode.
type QueryConfig struct {
	Repository               Repository
	Harnesses                WorkerHarnessResolver
	Host                     HostIntegrationStatus
	ReconciliationWorkspaces ReconciliationWorkspaceInspector
	Clock                    Clock
}

// NewQueries validates and binds the read-side dependencies.
func NewQueries(config QueryConfig) (*Queries, error) {
	if config.Repository == nil {
		return nil, errors.New("create queries: repository is required")
	}
	if config.Clock == nil {
		return nil, errors.New("create queries: clock is required")
	}
	return &Queries{
		repository: config.Repository, harnesses: config.Harnesses, host: config.Host,
		reconciliationWorkspaces: config.ReconciliationWorkspaces, clock: config.Clock,
	}, nil
}

// Diagnose returns bounded readiness without treating absent host integration as healthy.
func (queries *Queries) Diagnose(ctx context.Context) (DiagnosticReport, error) {
	stateVersion, err := queries.repository.CurrentStateVersion(ctx)
	if err != nil {
		return DiagnosticReport{}, translateReadError(err, "diagnostic state")
	}
	now := queries.now()
	completeness, serviceHealth, comisHealth, hostCheck := queries.hostHealth()
	return DiagnosticReport{
		SchemaVersion: 1,
		CapturedAtMs:  now.UnixMilli(),
		StateVersion:  stateVersion,
		Completeness:  completeness,
		ServiceHealth: serviceHealth,
		ComisHealth:   comisHealth,
		Checks: []DiagnosticCheck{
			{Name: "service", Status: CheckPass, Message: "service query handler is available", Hint: "none"},
			{Name: "store", Status: CheckPass, Message: "durable state is readable", Hint: "none"},
			hostCheck,
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
	completeness, serviceHealth, comisHealth, _ := queries.hostHealth()
	return FleetSnapshot{
		SchemaVersion: 1,
		CapturedAtMs:  now.UnixMilli(),
		StateVersion:  stateVersion,
		Completeness:  completeness,
		ServiceHealth: serviceHealth,
		ComisHealth:   comisHealth,
		Tasks:         projectTasks(tasks, now),
	}, nil
}

func (queries *Queries) hostHealth() (Completeness, HealthStatus, HealthStatus, DiagnosticCheck) {
	if queries.host == nil {
		return CompletenessPartial, HealthHealthy, HealthUnavailable, DiagnosticCheck{
			Name: "host_integration", Status: CheckUnknown, Message: "host integration is unavailable",
			Hint: "install and configure a compatible host integration",
		}
	}
	if !queries.host.Connected() {
		return CompletenessComplete, HealthDegraded, HealthUnavailable, DiagnosticCheck{
			Name: "host_integration", Status: CheckFail, Message: "authenticated host control is disconnected",
			Hint: "verify the host control socket and credential, then retry",
		}
	}
	return CompletenessComplete, HealthHealthy, HealthHealthy, DiagnosticCheck{
		Name: "host_integration", Status: CheckPass, Message: "authenticated host control is connected", Hint: "none",
	}
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
	if task.State == domain.TaskUnknown {
		reason, explanation, rootCause, actions, err = queries.explainUnknownRecovery(ctx, task)
		if err != nil {
			return TaskExplanation{}, err
		}
	}
	if task.State == domain.TaskFailed {
		candidateReason, candidateExplanation, candidateRootCause, found, candidateErr := queries.explainCandidateFailure(ctx, task.Handle)
		if candidateErr != nil {
			return TaskExplanation{}, translateReadError(candidateErr, "candidate evidence")
		}
		if found {
			reason = candidateReason
			explanation = candidateExplanation
			rootCause = candidateRootCause
			actions = []NextAction{ActionInspectTask}
		}
	}
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

func (queries *Queries) explainUnknownRecovery(
	ctx context.Context,
	task domain.Task,
) (string, string, string, []NextAction, error) {
	if queries.host == nil || !queries.host.Connected() {
		return "host_integration_unavailable",
			"Task recovery is waiting for the authenticated host integration.",
			"Current terminal authority cannot be refreshed while host control is unavailable.",
			[]NextAction{ActionInspectHealth}, nil
	}
	reader, ok := queries.repository.(taskRecoveryEvidenceReader)
	if !ok {
		return restartEvidenceUnresolvedExplanation()
	}
	evidence, err := reader.ReadTaskRecoveryEvidence(ctx, task.Handle)
	if err != nil {
		return "", "", "", nil, translateReadError(err, "task recovery evidence")
	}
	switch evidence.Kind {
	case RecoveryRestartEvidenceUnresolved:
		return restartEvidenceUnresolvedExplanation()
	case RecoveryTerminalSettledWithoutCandidate:
		if queries.reconciliationWorkspaces == nil {
			return workspaceNotRecoverableExplanation()
		}
		authority := evidence.Authority
		_, inspectErr := queries.reconciliationWorkspaces.InspectReconciliationCandidate(ctx, ReconciliationWorkspaceRequest{
			PreparationOperationID: authority.PreparationOperationID,
			TaskHandle:             task.Handle,
			RepositoryID:           task.RepositoryID,
			WorktreePath:           authority.Preparation.RequestedWorkspaceRoot,
			BaseRevision:           task.BaseRevision,
		})
		if inspectErr != nil {
			if ctx.Err() != nil {
				return "", "", "", nil, translateReadError(ctx.Err(), "task recovery workspace")
			}
			return workspaceNotRecoverableExplanation()
		}
		return "terminal_exited_without_candidate_evidence",
			"The worker terminal exited without an authoritative candidate report.",
			"The exact registered worktree still contains a clean non-base candidate.",
			[]NextAction{ActionInspectTask, ActionReconcileTask}, nil
	default:
		return restartEvidenceUnresolvedExplanation()
	}
}

func restartEvidenceUnresolvedExplanation() (string, string, string, []NextAction, error) {
	return "restart_evidence_unresolved",
		"Task recovery authority remains unresolved after a service restart or terminal interruption.",
		"A settled terminal and exact candidate origin cannot both be proven.",
		[]NextAction{ActionInspectHealth, ActionInspectTask}, nil
}

func workspaceNotRecoverableExplanation() (string, string, string, []NextAction, error) {
	return "workspace_not_recoverable",
		"The registered task workspace cannot be proven as a recoverable clean candidate.",
		"The worktree is missing, changed, dirty, divergent, or no longer matches its preparation authority.",
		[]NextAction{ActionInspectTask, ActionPrepareTask}, nil
}

func (queries *Queries) explainCandidateFailure(
	ctx context.Context,
	taskHandle string,
) (string, string, string, bool, error) {
	reader, ok := queries.repository.(candidateEvidenceReader)
	if !ok {
		return "", "", "", false, nil
	}
	_, judgment, err := reader.LatestCandidateEvidence(ctx, taskHandle)
	if errors.Is(err, ErrNotFound) {
		return "", "", "", false, nil
	}
	if err != nil {
		return "", "", "", false, err
	}
	if judgment.Outcome != domain.CandidateRejected {
		return "", "", "", false, nil
	}
	switch judgment.Reason {
	case domain.CandidateValidationFailed:
		return "candidate_validation_failed", "Candidate evidence was rejected by local validation.",
			"At least one required local validation check failed.", true, nil
	case domain.CandidateForgeFailed:
		return "candidate_forge_failed", "Candidate evidence was rejected by forge validation.",
			"At least one required forge check failed.", true, nil
	default:
		return "", "", "", false, nil
	}
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

// GetLaunchPlan builds the exact configured harness descriptor from durable
// activation authority, then returns only the reviewed non-executable fields.
func (queries *Queries) GetLaunchPlan(ctx context.Context, handle string) (LaunchPlan, error) {
	if err := domain.ValidateTaskHandle(handle); err != nil {
		return LaunchPlan{}, invalidReferenceFailure("task handle", err)
	}
	task, err := queries.repository.GetTask(ctx, handle)
	if err != nil {
		return LaunchPlan{}, translateReadError(err, "task")
	}
	if (task.State != domain.TaskReady && task.State != domain.TaskLaunching) ||
		task.ManagedRunID == "" || task.WorkspaceLeaseID == "" ||
		task.ExecutionAttachmentID == "" || task.AttachmentTargetName == "" {
		return LaunchPlan{}, newSafeFailure(
			domain.ErrorPrecondition, false, "task is not ready for launch",
			"wait for exact managed-run activation before requesting launch requirements", nil,
		)
	}
	if queries.harnesses == nil {
		return LaunchPlan{}, launchAdapterFailure(nil)
	}
	preparation, err := queries.repository.GetManagedRunPreparation(ctx, handle)
	if err != nil {
		return LaunchPlan{}, translateReadError(err, "task launch preparation")
	}
	descriptor, err := BuildWorkerLaunchDescriptor(ctx, task, preparation, queries.harnesses)
	if err != nil {
		if errors.Is(err, errLaunchAuthorityIncomplete) || errors.Is(err, errLaunchDescriptorInconsistent) {
			return LaunchPlan{}, newSafeFailure(
				domain.ErrorInternal, false, "reviewed launch descriptor is inconsistent",
				"inspect the configured worker profile before retrying", nil,
			)
		}
		return LaunchPlan{}, launchAdapterFailure(err)
	}
	return LaunchPlan{
		SchemaVersion: 1, CapturedAtMs: queries.now().UnixMilli(), StateVersion: task.StateVersion,
		Completeness: CompletenessComplete, TaskHandle: task.Handle, State: task.State,
		StateSource: StateSourceStore, StateConfidence: ConfidenceVerified, Freshness: FreshnessCurrent,
		WorkerProfileID: task.WorkerProfileID, TerminalAllowEntryID: descriptor.TerminalAllowEntry,
		ManagedRunID: task.ManagedRunID, WorkspaceLeaseID: task.WorkspaceLeaseID,
		BriefRevisionHash: task.BriefRevisionHash, AttachmentTargetName: task.AttachmentTargetName,
	}, nil
}

func launchAdapterFailure(cause error) error {
	if errors.Is(cause, context.Canceled) || errors.Is(cause, context.DeadlineExceeded) {
		return newSafeFailure(domain.ErrorDeadlineExceeded, true, "launch-plan query did not complete", "retry the read request", cause)
	}
	return newSafeFailure(
		domain.ErrorUnavailable, true, "reviewed worker profile is unavailable",
		"inspect the configured worker profile and exact harness version", cause,
	)
}

func (queries *Queries) taskSnapshot(ctx context.Context) ([]domain.Task, int64, error) {
	tasks, stateVersion, err := queries.repository.TaskSnapshot(ctx)
	if err != nil {
		return nil, 0, translateReadError(err, "task snapshot")
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
		StateVersion:     task.StateVersion,
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
	case domain.TaskReconciling:
		return "reconciliation_in_progress", "Task reconciliation is already in progress.", "A durable recovery operation owns the current transition.", []NextAction{ActionInspectTask}
	case domain.TaskFailed, domain.TaskCancelled, domain.TaskCleanupHeld:
		return "task_" + string(state), "Task requires operator review.", "The durable state records a terminal or safety-held posture.", []NextAction{ActionInspectTask}
	case domain.TaskDelivered, domain.TaskCleaned:
		return "task_" + string(state), "Task reached a completed durable posture.", "The durable state records successful completion of this stage.", []NextAction{ActionNone}
	default:
		return "task_" + string(state), "Task is progressing through its durable lifecycle.", "No durable blocking reason is recorded.", []NextAction{ActionInspectTask}
	}
}
