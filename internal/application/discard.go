package application

import (
	"context"
	"errors"
	"time"

	"github.com/comisai/comis-dev-crew/internal/domain"
)

// DiscardTaskCommand removes the worktree of one task that stopped without
// delivering anything.
//
// It requires an explicit acknowledgement. Cleanup can prove removal is safe by
// pointing at delivered work; a discard has nothing to point at, so the only
// thing standing between an operator and irreversible deletion of uncommitted
// work is their own statement that they meant it. A flag that defaulted to true,
// or that could be set by anything other than a person typing it, would remove
// the only gate this command has.
type DiscardTaskCommand struct {
	OperationID string
	TaskHandle  string
	// Acknowledged must be set by the operator. It is not a retry hint and never
	// carries over from a previous command.
	Acknowledged bool
}

// TaskDiscardMutation is the durable half of one discard's entry gate.
type TaskDiscardMutation struct {
	OperationID        string
	SubjectDigest      string
	TaskHandle         string
	ReleaseOperationID string
	ReleasedAt         time.Time
	At                 time.Time
}

// TaskDiscardStore begins the durable removal of undelivered work.
type TaskDiscardStore interface {
	BeginTaskDiscard(context.Context, TaskDiscardMutation) (TaskCleanupRecord, error)
}

// DiscardTask removes the worktree of a task that stopped without delivering.
//
// Cancellation preserves work on purpose, which leaves a settled task holding a
// worktree, a lease and a run binding that nothing else can release: cleanup
// requires delivery evidence a cancelled task will never have. Discard is the
// path out, and it releases host authority before removing anything, exactly as
// cleanup does.
func (coordinator *CleanupCoordinator) DiscardTask(
	ctx context.Context,
	command DiscardTaskCommand,
) (MutationResult, error) {
	if coordinator == nil || ctx == nil {
		return MutationResult{}, errors.New("discard task: coordinator and context are required")
	}
	if err := ctx.Err(); err != nil {
		return MutationResult{}, err
	}
	if domain.ValidateOperationID(command.OperationID) != nil ||
		domain.ValidateTaskHandle(command.TaskHandle) != nil {
		return MutationResult{}, mutationValidationFailure("discard command identity is invalid")
	}
	if !command.Acknowledged {
		return MutationResult{}, discardUnacknowledgedFailure()
	}
	discards, ok := coordinator.config.Store.(TaskDiscardStore)
	if !ok {
		return MutationResult{}, &dependencyFailure{message: "discard authority is unavailable"}
	}
	digest, err := digestMutationSubject(command)
	if err != nil {
		return MutationResult{}, mutationValidationFailure("discard subject cannot be encoded")
	}
	observed := coordinator.config.Clock()
	now := time.UnixMilli(observed.UnixMilli()).UTC()
	record, err := discards.BeginTaskDiscard(ctx, TaskDiscardMutation{
		OperationID: command.OperationID, SubjectDigest: digest, TaskHandle: command.TaskHandle,
		ReleaseOperationID: cleanupReleaseOperationID(command.OperationID, command.TaskHandle),
		ReleasedAt:         now, At: now,
	})
	if err != nil {
		return MutationResult{}, cleanupCommitFailure(err)
	}
	return coordinator.runRemovalStages(ctx, record, command.OperationID, command.TaskHandle, digest)
}

// discardUnacknowledgedFailure names what the operator must confirm. A bare
// refusal would leave them guessing at a flag whose whole purpose is to be
// typed deliberately.
func discardUnacknowledgedFailure() error {
	failure, err := domain.NewFailure(
		domain.ErrorPrecondition, false,
		"discarding removes work that was never delivered and cannot be undone",
		"re-run with the explicit acknowledgement once you have confirmed nothing in the worktree is needed",
		nil,
	)
	if err != nil {
		return mutationValidationFailure("discard refusal cannot be encoded")
	}
	return failure
}
