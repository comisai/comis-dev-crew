package localapi

import (
	"context"

	"github.com/comisai/comis-dev-crew/internal/application"
	"github.com/comisai/comis-dev-crew/internal/domain"
)

// ReadQueries is the narrow application surface consumed by the local boundary.
type ReadQueries interface {
	ReadEvents(context.Context, int64, int, string) (application.EventPage, error)
	ReadAudit(context.Context, int64, int) (application.AuditPage, error)
	ReadTaskLogs(context.Context, string, application.TaskLogSource, int64, int) (application.TaskLogPage, error)
	DiffTask(context.Context, string) (application.TaskDiffView, error)
	SurveyRepairs(context.Context, string) (application.RepairSurvey, error)
	ListDecisions(context.Context, string) (application.DecisionList, error)
	ShowDecision(context.Context, string, string) (application.TaskDecision, error)
	Diagnose(context.Context) (application.DiagnosticReport, error)
	Fleet(context.Context) (application.FleetSnapshot, error)
	ListTasks(context.Context, domain.TaskState) (application.TaskList, error)
	ListWorkerProfiles(context.Context) (application.WorkerProfileList, error)
	ShowTask(context.Context, string) (application.TaskDetail, error)
	ExplainTask(context.Context, string) (application.TaskExplanation, error)
	GetLaunchPlan(context.Context, string) (application.LaunchPlan, error)
	Operation(context.Context, string) (application.OperationView, error)
}

// TaskMutations is the sole canonical mutation surface used by the local API.
type TaskMutations interface {
	PrepareTask(context.Context, application.PrepareTaskCommand) (application.MutationResult, error)
	PauseTask(context.Context, application.PauseTaskCommand) (application.MutationResult, error)
	VerifyTask(context.Context, application.VerifyTaskCommand) (application.MutationResult, error)
	SteerTask(context.Context, application.SteerTaskCommand) (application.MutationResult, error)
	PromoteScout(context.Context, application.PromoteScoutCommand) (application.MutationResult, error)
	CancelTask(context.Context, application.CancelTaskCommand) (application.MutationResult, error)
}

// InitiativeMutations is the canonical all-or-none group preparation surface.
type InitiativeMutations interface {
	PrepareInitiative(context.Context, application.PrepareInitiativeCommand) (application.InitiativePreparationResult, error)
}

// InitiativeControls is the operator-only non-atomic group control surface.
type InitiativeControls interface {
	PauseInitiative(context.Context, application.InitiativeControlCommand) (application.InitiativeControlResult, error)
	ResumeInitiative(context.Context, application.InitiativeControlCommand) (application.InitiativeControlResult, error)
	CancelInitiative(context.Context, application.InitiativeControlCommand) (application.InitiativeControlResult, error)
}

// InitiativeReadQueries is the narrow initiative and backlog read surface.
type InitiativeReadQueries interface {
	ListInitiatives(context.Context, domain.InitiativeState) (application.InitiativeList, error)
	GetInitiative(context.Context, string) (application.InitiativeDetail, error)
	ListBacklog(context.Context, application.BacklogFilter) (application.BacklogList, error)
}

// BacklogAdditions records bounded requests without granting run authority.
type BacklogAdditions interface {
	AddBacklog(context.Context, application.BacklogAdditionCommand) (application.BacklogAdditionResult, error)
}

// BacklogPromotions converts a ready request through normal task preparation.
type BacklogPromotions interface {
	PromoteBacklog(context.Context, application.BacklogPromotionCommand) (application.BacklogPromotionResult, error)
}

// TaskInterventions is the canonical paused-worktree handback surface.
type TaskInterventions interface {
	ResumeTask(context.Context, application.ResumeTaskCommand) (application.MutationResult, error)
	ReplaceWorker(context.Context, application.ReplaceWorkerCommand) (application.MutationResult, error)
	HandbackTask(context.Context, application.HandbackTaskCommand) (application.MutationResult, error)
}

// TaskReconciliation is the canonical unknown-task recovery surface.
type TaskReconciliation interface {
	ReconcileTask(context.Context, application.ReconcileTaskCommand) (application.MutationResult, error)
}

// TaskCleanup is the canonical release-before-removal mutation surface.
type TaskCleanup interface {
	CleanupTask(context.Context, application.CleanupTaskCommand) (application.MutationResult, error)
	DiscardTask(context.Context, application.DiscardTaskCommand) (application.MutationResult, error)
}

// DecisionAuthority owns operator decisions over worker questions.
type DecisionAuthority interface {
	CancelDecision(context.Context, application.CancelDecisionCommand) (application.MutationResult, error)
	RespondDecision(context.Context, application.RespondDecisionCommand) (application.MutationResult, error)
}

// ScoutReviewAttestation is the canonical review-completion surface.
type ScoutReviewAttestation interface {
	AttestScoutDecisions(context.Context, application.AttestScoutDecisionsCommand) (application.MutationResult, error)
}

// PrimaryCheckoutSync advances only an operator-configured primary checkout.
type PrimaryCheckoutSync interface {
	SyncPrimary(context.Context, application.PrimarySyncCommand) (application.PrimarySyncReport, error)
}

// HandlerConfig binds local endpoint authority to canonical application seams.
type HandlerConfig struct {
	Queries             ReadQueries
	InitiativeQueries   InitiativeReadQueries
	Mutations           TaskMutations
	InitiativeMutations InitiativeMutations
	InitiativeControls  InitiativeControls
	BacklogAdditions    BacklogAdditions
	BacklogPromotions   BacklogPromotions
	Reconciliation      TaskReconciliation
	Interventions       TaskInterventions
	Cleanup             TaskCleanup
	PrimaryCheckouts    PrimaryCheckoutSync
	ScoutReviews        ScoutReviewAttestation
	Decisions           DecisionAuthority
	ServiceInstanceID   string
	Clock               application.Clock
	// Logger is optional. A deployment without one records nothing and serves
	// exactly as before.
	Logger application.BoundaryLogger
}
