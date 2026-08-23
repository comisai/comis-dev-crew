package application

import (
	"context"

	"github.com/comisai/comis-dev-crew/internal/domain"
)

// ResumeTask returns one paused task to the worker that was already running it.
//
// It refuses a worktree that is not exactly as that worker left it. This is the
// rule the command exists for: the paused worker still holds a brief, a base
// revision, and an evidence set describing the tree it stopped on, and none of
// those would notice a developer's edit. Resuming onto a changed tree would
// continue from a description of a tree that no longer exists, and the first
// sign of it would be a candidate built on assumptions nobody re-checked.
//
// A dirty tree is therefore not an error to work around but a routing decision:
// the operator wants handback, which captures the fresh head, invalidates the
// evidence the edit stales, and revalidates before anything continues.
func (interventions *Interventions) ResumeTask(
	ctx context.Context,
	command ResumeTaskCommand,
) (MutationResult, error) {
	if err := validMutationContext(ctx); err != nil {
		return MutationResult{}, err
	}
	if domain.ValidateOperationID(command.OperationID) != nil ||
		domain.ValidateTaskHandle(command.TaskHandle) != nil {
		return MutationResult{}, mutationValidationFailure("resume fields are invalid")
	}
	subjectDigest, err := digestMutationSubject(command)
	if err != nil {
		return MutationResult{}, mutationValidationFailure("resume subject cannot be encoded")
	}
	if replay, found, err := interventions.store.ReplayMutation(
		ctx, command.OperationID, commandResumeTaskName, subjectDigest,
	); err != nil {
		return MutationResult{}, mutationReplayFailure(err)
	} else if found {
		if err := interventions.rebindReadyWorkerLaunch(ctx, replay.Task); err != nil {
			return MutationResult{}, err
		}
		return replay, nil
	}
	task, err := interventions.store.GetTask(ctx, command.TaskHandle)
	if err != nil {
		return MutationResult{}, mutationCommitFailure(err)
	}
	if task.State != domain.TaskPaused {
		return MutationResult{}, mutationCommitFailure(ErrPrecondition)
	}
	snapshot, err := interventions.inspectPausedWorkspace(ctx, task)
	if err != nil {
		return MutationResult{}, err
	}
	if snapshot.Cleanliness != WorkspaceClean {
		if promoted, found := interventions.promotePausedWorkerCandidate(ctx, task); found {
			snapshot = promoted
		}
	}
	// Clean means the tree is what the worker left. A dirty tree carries a
	// developer's edit that only handback's revalidation can safely absorb.
	if snapshot.Cleanliness != WorkspaceClean {
		return MutationResult{}, dirtyWorktreeRefusal()
	}
	result, err := interventions.store.CommitTaskResume(ctx, TaskResumeMutation{
		TaskHandle: task.Handle, OperationID: command.OperationID,
		SubjectDigest: subjectDigest, ObservedHeadRevision: snapshot.HeadRevision,
		At: interventions.clock(),
	})
	if err != nil {
		return MutationResult{}, mutationCommitFailure(err)
	}
	if err := interventions.rebindReadyWorkerLaunch(ctx, result.Task); err != nil {
		return MutationResult{}, err
	}
	return result, nil
}

func (interventions *Interventions) rebindReadyWorkerLaunch(ctx context.Context, task domain.Task) error {
	if interventions.runtimeLaunches == nil {
		return nil
	}
	// A replay read returns the task's current state, not the historical ready
	// projection. Once launch advanced, the first rebind already succeeded.
	if task.State != domain.TaskReady {
		return nil
	}
	if task.StateVersion < 1 {
		return &dependencyFailure{message: "worker relaunch generation is unavailable"}
	}
	operationID, err := RuntimeRelaunchAcknowledgementOperationID(task.Handle, task.StateVersion)
	if err != nil {
		return &dependencyFailure{message: "worker relaunch identity is unavailable", cause: err}
	}
	brief, err := task.RenderWorkerBrief()
	if err != nil {
		return &dependencyFailure{message: "worker relaunch brief is unavailable", cause: err}
	}
	if err := interventions.runtimeLaunches.RebindRuntimeAttachmentLaunch(ctx, RuntimeAttachmentLaunchRebindRequest{
		TaskHandle: task.Handle, ReadyStateVersion: task.StateVersion,
		LaunchOperationID: operationID, Brief: brief,
	}); err != nil {
		return &dependencyFailure{message: "worker runtime attachment could not be rebound", cause: err}
	}
	return nil
}

type pausedCandidateAuthorityReader interface {
	ReadCandidateHandoffAuthority(context.Context, string) (CandidateHandoffAuthority, error)
}

// promotePausedWorkerCandidate recognizes the clean commit a confined worker
// left in lease-private Git administration and hands that exact commit to the
// shared task branch. Ordinary Git reports those committed files as dirty until
// this handoff, which must not be confused with a developer editing a pause.
func (interventions *Interventions) promotePausedWorkerCandidate(
	ctx context.Context,
	task domain.Task,
) (WorkspaceSnapshot, bool) {
	authorities, authorityOK := interventions.store.(pausedCandidateAuthorityReader)
	promoter, promoterOK := interventions.workspaces.(ReconciliationWorkspacePromoter)
	if !authorityOK || !promoterOK {
		return WorkspaceSnapshot{}, false
	}
	authority, err := authorities.ReadCandidateHandoffAuthority(ctx, task.Handle)
	if err != nil || authority.Task.Handle != task.Handle || authority.Task.State != domain.TaskPaused ||
		authority.Task.RepositoryID != task.RepositoryID ||
		authority.Preparation.ExternalRunRef != task.Handle ||
		authority.Preparation.RequestedWorkspaceRoot == "" ||
		domain.ValidateOperationID(authority.PreparationOperationID) != nil {
		return WorkspaceSnapshot{}, false
	}
	snapshot, err := promoter.PromoteReconciliationCandidate(ctx, ReconciliationWorkspaceRequest{
		PreparationOperationID: authority.PreparationOperationID,
		TaskHandle:             task.Handle,
		RepositoryID:           task.RepositoryID,
		WorktreePath:           authority.Preparation.RequestedWorkspaceRoot,
		BaseRevision:           task.BaseRevision,
	})
	if err != nil || snapshot.Validate() != nil || snapshot.TaskHandle != task.Handle ||
		snapshot.RepositoryID != task.RepositoryID ||
		snapshot.WorktreePath != authority.Preparation.RequestedWorkspaceRoot ||
		snapshot.Cleanliness != WorkspaceClean || snapshot.HeadRevision == task.BaseRevision {
		return WorkspaceSnapshot{}, false
	}
	return snapshot, true
}

// inspectPausedWorkspace reads independent Git truth for one paused task. Both
// resume and handback need exactly this, and neither may trust a snapshot that
// describes a different task, repository, or worktree than the one asked about.
func (interventions *Interventions) inspectPausedWorkspace(
	ctx context.Context,
	task domain.Task,
) (WorkspaceSnapshot, error) {
	preparation, err := interventions.store.GetManagedRunPreparation(ctx, task.Handle)
	if err != nil || preparation.RequestedWorkspaceRoot == "" {
		return WorkspaceSnapshot{}, &dependencyFailure{
			message: "task workspace is unavailable", cause: err,
		}
	}
	snapshot, err := interventions.workspaces.InspectWorkspace(ctx, WorkspaceSnapshotRequest{
		TaskHandle: task.Handle, RepositoryID: task.RepositoryID,
		WorktreePath: preparation.RequestedWorkspaceRoot,
	})
	if err != nil {
		return WorkspaceSnapshot{}, &dependencyFailure{
			message: "task workspace inspection failed", cause: err,
		}
	}
	if snapshot.Validate() != nil || snapshot.TaskHandle != task.Handle ||
		snapshot.RepositoryID != task.RepositoryID ||
		snapshot.WorktreePath != preparation.RequestedWorkspaceRoot {
		return WorkspaceSnapshot{}, &dependencyFailure{
			message: "task workspace inspection returned different authority",
		}
	}
	return snapshot, nil
}

// dirtyWorktreeRefusal names the command that can absorb the edit. A bare
// "precondition failed" would leave an operator with a paused task, a change
// they made deliberately, and no indication that the product already has the
// path they need.
func dirtyWorktreeRefusal() error {
	failure, err := domain.NewFailure(
		domain.ErrorPrecondition, false,
		"the worktree has uncommitted changes, so the paused worker cannot resume onto it",
		"hand the work back with action validate-developer-work to revalidate the edit",
		nil,
	)
	if err != nil {
		return mutationValidationFailure("resume refusal cannot be encoded")
	}
	return failure
}

// The command name is shared with the durable layer's replay index.
const commandResumeTaskName = "ResumeTask"
