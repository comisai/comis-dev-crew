package application

import (
	"context"

	"github.com/comisai/comis-dev-crew/internal/domain"
)

// ReplaceWorker preserves one paused task's work and readies it for a different
// worker.
//
// The worktree is untouched. Replacement exists for the case where the work is
// worth keeping and the worker is not — a wedged harness, a profile that turned
// out wrong for the task — so discarding the tree would destroy the very thing
// being preserved.
//
// The proposed profile is checked against the reviewed catalog for this task's
// shape before anything durable happens. A replacement that could name any
// profile string would be a way to launch an unreviewed executable through a
// command whose stated purpose is recovery.
func (interventions *Interventions) ReplaceWorker(
	ctx context.Context,
	command ReplaceWorkerCommand,
) (MutationResult, error) {
	if err := validMutationContext(ctx); err != nil {
		return MutationResult{}, err
	}
	if domain.ValidateOperationID(command.OperationID) != nil ||
		domain.ValidateTaskHandle(command.TaskHandle) != nil ||
		domain.ValidateAuthorityReference("workerProfileId", command.WorkerProfileID) != nil {
		return MutationResult{}, mutationValidationFailure("replacement fields are invalid")
	}
	if interventions.workerProfiles == nil {
		return MutationResult{}, &dependencyFailure{message: "reviewed worker profiles are unavailable"}
	}
	subjectDigest, err := digestMutationSubject(command)
	if err != nil {
		return MutationResult{}, mutationValidationFailure("replacement subject cannot be encoded")
	}
	if replay, found, err := interventions.store.ReplayMutation(
		ctx, command.OperationID, commandReplaceWorkerName, subjectDigest,
	); err != nil {
		return MutationResult{}, mutationReplayFailure(err)
	} else if found {
		return replay, nil
	}
	task, err := interventions.store.GetTask(ctx, command.TaskHandle)
	if err != nil {
		return MutationResult{}, mutationCommitFailure(err)
	}
	if task.State != domain.TaskPaused {
		return MutationResult{}, mutationCommitFailure(ErrPrecondition)
	}
	// The profile is checked for this task's own shape. A ship profile cannot
	// take over a scout, which would hand an investigation the delivery authority
	// the scout shape withholds.
	if err := interventions.workerProfiles(command.WorkerProfileID, task.Shape); err != nil {
		return MutationResult{}, mutationValidationFailure("replacement worker profile is not reviewed for this task shape")
	}
	snapshot, err := interventions.inspectPausedWorkspace(ctx, task)
	if err != nil {
		return MutationResult{}, err
	}
	result, err := interventions.store.CommitTaskReplace(ctx, TaskReplaceMutation{
		OperationID: command.OperationID, SubjectDigest: subjectDigest,
		TaskHandle: task.Handle, WorkerProfileID: command.WorkerProfileID,
		Snapshot: snapshot, At: interventions.clock(),
	})
	if err != nil {
		return MutationResult{}, mutationCommitFailure(err)
	}
	return result, nil
}

// The command name is shared with the durable layer's replay index.
const commandReplaceWorkerName = "ReplaceWorker"
