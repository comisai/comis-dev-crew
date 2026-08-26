package sqlite

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/comisai/comis-dev-crew/internal/application"
	"github.com/comisai/comis-dev-crew/internal/domain"
)

const commandAddBacklog = "AddBacklog"

var _ application.BacklogAdditionStore = (*Store)(nil)

// ReplayBacklogAddition returns the exact durable item created by one operation.
func (store *Store) ReplayBacklogAddition(
	ctx context.Context,
	operationID, subjectDigest string,
) (application.BacklogAdditionResult, bool, error) {
	transaction, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return application.BacklogAdditionResult{}, false, fmt.Errorf("begin backlog addition replay: %w", err)
	}
	defer func() { _ = transaction.Rollback() }()
	operation, found, err := mutationReplay(ctx, transaction, operationID, commandAddBacklog, subjectDigest)
	if err != nil {
		return application.BacklogAdditionResult{}, false, commitReplayConflict(transaction, err)
	}
	if !found {
		return application.BacklogAdditionResult{}, false, nil
	}
	result, err := readBacklogAdditionResult(ctx, transaction, operation)
	if err != nil {
		return application.BacklogAdditionResult{}, false, err
	}
	if err := transaction.Commit(); err != nil {
		return application.BacklogAdditionResult{}, false, fmt.Errorf("commit backlog addition replay: %w", err)
	}
	return result, true, nil
}

// CommitBacklogAddition atomically records one item and its completed operation.
func (store *Store) CommitBacklogAddition(
	ctx context.Context,
	mutation application.BacklogAdditionMutation,
) (application.BacklogAdditionResult, error) {
	if err := validateBacklogAdditionMutation(mutation); err != nil {
		return application.BacklogAdditionResult{}, err
	}
	transaction, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return application.BacklogAdditionResult{}, fmt.Errorf("begin backlog addition: %w", err)
	}
	defer func() { _ = transaction.Rollback() }()
	if operation, found, err := mutationReplay(
		ctx, transaction, mutation.OperationID, commandAddBacklog, mutation.SubjectDigest,
	); err != nil {
		return application.BacklogAdditionResult{}, commitReplayConflict(transaction, err)
	} else if found {
		return readBacklogAdditionResult(ctx, transaction, operation)
	}
	for _, dependency := range mutation.Item.DependsOn {
		if _, err := getBacklogItem(ctx, transaction, dependency); err != nil {
			if errors.Is(err, application.ErrNotFound) {
				return application.BacklogAdditionResult{}, fmt.Errorf("backlog dependency is missing: %w", application.ErrPrecondition)
			}
			return application.BacklogAdditionResult{}, err
		}
	}
	if err := insertBacklogItem(ctx, transaction, mutation.Item); err != nil {
		return application.BacklogAdditionResult{}, fmt.Errorf("insert backlog addition: %w", err)
	}
	stateVersion, err := nextMutationStateVersion(ctx, transaction)
	if err != nil {
		return application.BacklogAdditionResult{}, err
	}
	operation := completedMutationOperation(
		mutation.OperationID, commandAddBacklog, mutation.SubjectDigest,
		mutation.Item.Handle, stateVersion, mutation.At,
	)
	if err := insertOperation(ctx, transaction, operation); err != nil {
		if isConstraintError(err) {
			return application.BacklogAdditionResult{}, fmt.Errorf("insert backlog addition operation: %w", application.ErrConflict)
		}
		return application.BacklogAdditionResult{}, fmt.Errorf("insert backlog addition operation: %w", err)
	}
	if err := transaction.Commit(); err != nil {
		return application.BacklogAdditionResult{}, fmt.Errorf("commit backlog addition: %w", err)
	}
	return application.BacklogAdditionResult{Item: mutation.Item, Operation: operation}, nil
}

func readBacklogAdditionResult(
	ctx context.Context,
	source queryer,
	operation domain.OperationRecord,
) (application.BacklogAdditionResult, error) {
	item, err := getBacklogItem(ctx, source, operation.ResultRef)
	if err != nil {
		return application.BacklogAdditionResult{}, fmt.Errorf("read backlog addition result: %w", err)
	}
	return application.BacklogAdditionResult{Item: item, Operation: operation}, nil
}

func validateBacklogAdditionMutation(mutation application.BacklogAdditionMutation) error {
	if domain.ValidateOperationID(mutation.OperationID) != nil || len(mutation.SubjectDigest) != 64 ||
		mutation.At.Location() != time.UTC || mutation.Item.Validate() != nil ||
		!mutation.Item.CreatedAt.Equal(mutation.At) || !mutation.Item.UpdatedAt.Equal(mutation.At) ||
		(mutation.Item.Readiness != domain.BacklogReady && mutation.Item.Readiness != domain.BacklogNeedsRefinement) {
		return errors.New("backlog addition mutation is invalid")
	}
	return nil
}
