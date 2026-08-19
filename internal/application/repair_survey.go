package application

import (
	"context"
	"time"

	"github.com/comisai/comis-dev-crew/internal/domain"
)

// RepairPosture is the closed reason one task in an unknown state can or cannot
// be reconciled right now.
//
// It is a discriminator rather than prose because the operator's next move
// differs per value, and because a free-text explanation of a task's worktree
// would put private detail on a fleet-wide surface.
type RepairPosture string

const (
	// RepairReconcilable means the durable authority and the worktree agree and
	// the task carries a clean candidate commit ahead of its pinned base.
	RepairReconcilable RepairPosture = "reconcilable"
	// RepairAuthorityIncomplete means the task, its preparation, or its terminal
	// settlement is not proven, so no Git evidence is trustworthy yet.
	RepairAuthorityIncomplete RepairPosture = "authority_incomplete"
	// RepairWorkspaceUnverified means the registered worktree could not be
	// proven to be the one the task owns.
	RepairWorkspaceUnverified RepairPosture = "workspace_unverified"
	// RepairWorktreeDirty means uncommitted work would be swept into a
	// reconciled candidate.
	RepairWorktreeDirty RepairPosture = "worktree_dirty"
	// RepairNoCandidate means the head still equals the pinned base, so there is
	// no candidate commit to reconcile.
	RepairNoCandidate RepairPosture = "no_candidate"
)

// Valid reports whether the posture is one this survey can produce.
func (posture RepairPosture) Valid() bool {
	switch posture {
	case RepairReconcilable, RepairAuthorityIncomplete, RepairWorkspaceUnverified,
		RepairWorktreeDirty, RepairNoCandidate:
		return true
	default:
		return false
	}
}

// TaskRepair is one task whose durable posture needs an operator decision.
type TaskRepair struct {
	TaskHandle string           `json:"taskHandle"`
	State      domain.TaskState `json:"state"`
	Posture    RepairPosture    `json:"posture"`
}

// RepairSurvey is the bounded fleet view of what needs reconciling.
//
// It reports and never mutates. Reconciliation stays behind the explicit
// per-task command, because choosing an action from evidence is exactly the
// authority a survey must not take.
type RepairSurvey struct {
	SchemaVersion int          `json:"schemaVersion"`
	CapturedAt    time.Time    `json:"capturedAt"`
	StateVersion  int64        `json:"stateVersion"`
	Tasks         []TaskRepair `json:"tasks"`
}

// RepairSurveyStore reads the durable authority reconciliation depends on.
type RepairSurveyStore interface {
	ReadTaskReconciliationAuthority(context.Context, string) (TaskReconciliationAuthority, error)
}

// SurveyRepairs reports which unknown tasks can be reconciled and why the rest
// cannot, optionally scoped to one task.
//
// Only tasks in the unknown state are surveyed, because that is the only state
// the reconciliation command accepts; listing anything else would offer an
// action the service would refuse.
func (queries *Queries) SurveyRepairs(ctx context.Context, taskHandle string) (RepairSurvey, error) {
	if taskHandle != "" {
		if err := domain.ValidateTaskHandle(taskHandle); err != nil {
			return RepairSurvey{}, invalidReferenceFailure("task reference", err)
		}
	}
	if queries.repairs == nil || queries.reconciliationWorkspaces == nil {
		return RepairSurvey{}, translateReadError(nil, "repair survey")
	}
	tasks, stateVersion, err := queries.repository.TaskSnapshot(ctx)
	if err != nil {
		return RepairSurvey{}, translateReadError(err, "repair survey")
	}
	survey := RepairSurvey{
		SchemaVersion: 1, CapturedAt: queries.now(), StateVersion: stateVersion,
		Tasks: []TaskRepair{},
	}
	for _, task := range tasks {
		if task.State != domain.TaskUnknown {
			continue
		}
		if taskHandle != "" && task.Handle != taskHandle {
			continue
		}
		posture, err := queries.repairPosture(ctx, task)
		if err != nil {
			return RepairSurvey{}, err
		}
		survey.Tasks = append(survey.Tasks, TaskRepair{
			TaskHandle: task.Handle, State: task.State, Posture: posture,
		})
	}
	return survey, nil
}

// repairPosture classifies one unknown task against the same evidence the
// reconciliation command requires, using only its read-only inspection.
//
// Every way the evidence falls short becomes a named posture rather than an
// error, so one unreconcilable task never hides the rest of the fleet. Only a
// caller-cancelled read stops the survey.
func (queries *Queries) repairPosture(ctx context.Context, task domain.Task) (RepairPosture, error) {
	authority, err := queries.repairs.ReadTaskReconciliationAuthority(ctx, task.Handle)
	if err != nil {
		if ctx.Err() != nil {
			return "", translateReadError(ctx.Err(), "repair survey")
		}
		return RepairAuthorityIncomplete, nil
	}
	if validateTaskReconciliationAuthority(authority, task.Handle) != nil {
		return RepairAuthorityIncomplete, nil
	}
	snapshot, err := queries.reconciliationWorkspaces.InspectReconciliationCandidate(
		ctx, ReconciliationWorkspaceRequest{
			PreparationOperationID: authority.PreparationOperationID,
			TaskHandle:             authority.Task.Handle, RepositoryID: authority.Task.RepositoryID,
			WorktreePath: authority.Preparation.RequestedWorkspaceRoot,
			BaseRevision: authority.Task.BaseRevision,
		},
	)
	if err != nil {
		if ctx.Err() != nil {
			return "", translateReadError(ctx.Err(), "repair survey")
		}
		return RepairWorkspaceUnverified, nil
	}
	if snapshot.Validate() != nil || snapshot.TaskHandle != authority.Task.Handle ||
		snapshot.RepositoryID != authority.Task.RepositoryID ||
		snapshot.WorktreePath != authority.Preparation.RequestedWorkspaceRoot {
		return RepairWorkspaceUnverified, nil
	}
	if snapshot.Cleanliness != WorkspaceClean {
		return RepairWorktreeDirty, nil
	}
	if snapshot.HeadRevision == authority.Task.BaseRevision {
		return RepairNoCandidate, nil
	}
	return RepairReconcilable, nil
}
