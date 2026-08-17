package sqlite

import (
	"context"
	"fmt"
	"time"

	"github.com/comisai/comis-dev-crew/internal/application"
	"github.com/comisai/comis-dev-crew/internal/domain"
)

// CommitManagedRunCancel stops one activated run at the host's request.
//
// A run this service has already settled answers with that settled task instead
// of refusing: the host recorded its decision before sending, so treating an
// already-terminal run as a precondition failure would make a safe repeat look
// like a fault. Artifacts are preserved — cancellation stops work, it does not
// discard it, and removal has its own evidence-gated path.
func (store *Store) CommitManagedRunCancel(ctx context.Context, mutation application.ManagedRunCancelMutation) (application.MutationResult, error) {
	transaction, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return application.MutationResult{}, fmt.Errorf("begin managed-run cancel: %w", err)
	}
	defer func() { _ = transaction.Rollback() }()
	if replay, found, err := mutationReplay(ctx, transaction, mutation.OperationID, commandCancelManagedRun, mutation.SubjectDigest); err != nil {
		return application.MutationResult{}, commitReplayConflict(transaction, err)
	} else if found {
		return replayResult(ctx, transaction, replay)
	}
	task, err := getTaskByManagedRun(ctx, transaction, mutation.ManagedRunID)
	if err != nil {
		return application.MutationResult{}, err
	}
	if task.ServiceInstanceID != mutation.ServiceInstanceID || mutation.At.Location() != time.UTC {
		return application.MutationResult{}, fmt.Errorf("managed-run cancel join: %w", application.ErrPrecondition)
	}
	updated := task
	if task.State != domain.TaskCancelled {
		updated, err = task.ApplyTransition(domain.TransitionCancelRequested, mutation.At)
		if err != nil {
			return application.MutationResult{}, fmt.Errorf("apply managed-run cancel: %w", err)
		}
	}
	stateVersion, err := nextMutationStateVersion(ctx, transaction)
	if err != nil {
		return application.MutationResult{}, err
	}
	updated.StateVersion = stateVersion
	if err := updateTaskState(ctx, transaction, updated); err != nil {
		return application.MutationResult{}, err
	}
	operation := completedMutationOperation(
		mutation.OperationID, commandCancelManagedRun, mutation.SubjectDigest,
		updated.Handle, stateVersion, mutation.At,
	)
	if err := insertOperation(ctx, transaction, operation); err != nil {
		if isConstraintError(err) {
			return application.MutationResult{}, fmt.Errorf("insert cancel operation: %w", application.ErrConflict)
		}
		return application.MutationResult{}, fmt.Errorf("insert cancel operation: %w", err)
	}
	if err := transaction.Commit(); err != nil {
		return application.MutationResult{}, fmt.Errorf("commit managed-run cancel: %w", err)
	}
	return application.MutationResult{Task: updated, Operation: operation}, nil
}
