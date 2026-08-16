package application

import (
	"context"
	"errors"

	"github.com/comisai/comis-dev-crew/internal/domain"
)

// ExplainTask returns a reasoned, bounded task posture from durable state.
func (queries *Queries) ExplainTask(ctx context.Context, handle string) (TaskExplanation, error) {
	if err := domain.ValidateTaskHandle(handle); err != nil {
		return TaskExplanation{}, invalidReferenceFailure("task handle", err)
	}
	observation, err := queries.taskObservation(ctx, handle)
	if err != nil {
		return TaskExplanation{}, translateReadError(err, "task")
	}
	task := observation.Task
	now := queries.now()
	summary, evidence, err := queries.projectTask(ctx, observation, now)
	if err != nil {
		return TaskExplanation{}, err
	}
	reason, explanation, rootCause, actions := explainState(task.State)
	if task.State == domain.TaskUnknown {
		reason, explanation, rootCause, actions, err = queries.explainUnknownRecovery(ctx, task)
		if err != nil {
			return TaskExplanation{}, err
		}
	}
	if task.State == domain.TaskFailed || task.State == domain.TaskValidating {
		candidateReason, candidateExplanation, candidateRootCause, found, candidateErr := queries.explainCandidatePosture(ctx, task)
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
		Summary:         summary,
		Evidence:        evidence,
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

func (queries *Queries) explainCandidatePosture(
	ctx context.Context,
	task domain.Task,
) (string, string, string, bool, error) {
	reader, ok := queries.repository.(candidateEvidenceReader)
	if !ok {
		return "", "", "", false, nil
	}
	sealed, judgment, err := reader.LatestCandidateEvidence(ctx, task.Handle)
	if errors.Is(err, ErrNotFound) {
		return "", "", "", false, nil
	}
	if err != nil {
		return "", "", "", false, err
	}
	if judgment.Outcome == domain.CandidateUnknown && judgment.Reason == domain.CandidateWorktreeUnverified && sealed != nil {
		return "candidate_worktree_unverified",
			"Candidate validation is waiting because current Git truth is unverified.",
			unverifiedCandidateRootCause(task, sealed.Bundle()), true, nil
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

func unverifiedCandidateRootCause(task domain.Task, bundle domain.DeliveryEvidenceBundle) string {
	if bundle.WorktreeCleanliness == domain.WorktreeUnknown {
		return "The exact registered worktree Git posture is unknown; no clean candidate head was observed."
	}
	dirty := bundle.WorktreeCleanliness != domain.WorktreeClean
	baseEqual := bundle.HeadRevision == task.BaseRevision
	switch {
	case dirty && baseEqual:
		return "The exact registered worktree is not clean and its candidate head still equals the pinned base revision."
	case dirty:
		return "The exact registered worktree is not clean."
	case baseEqual:
		return "The candidate head still equals the pinned base revision."
	default:
		return "The exact registered worktree does not satisfy clean non-base candidate authority."
	}
}
