package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/comisai/comis-dev-crew/internal/application"
	"github.com/comisai/comis-dev-crew/internal/domain"
)

var _ application.InitiativeQueryStore = (*Store)(nil)

// InitiativeSnapshot reads the initiative list and advertised version from one snapshot.
func (store *Store) InitiativeSnapshot(
	ctx context.Context,
) ([]domain.DevelopmentInitiative, int64, error) {
	transaction, err := store.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, 0, fmt.Errorf("begin initiative snapshot: %w", err)
	}
	initiatives, err := listInitiatives(ctx, transaction)
	if err != nil {
		return nil, 0, errors.Join(err, transaction.Rollback())
	}
	stateVersion, err := currentStateVersion(ctx, transaction)
	if err != nil {
		return nil, 0, errors.Join(err, transaction.Rollback())
	}
	if err := transaction.Commit(); err != nil {
		return nil, 0, fmt.Errorf("commit initiative snapshot: %w", err)
	}
	return initiatives, stateVersion, nil
}

// InitiativeObservation reads one initiative, every member task, and its version atomically.
func (store *Store) InitiativeObservation(
	ctx context.Context,
	handle string,
) (domain.DevelopmentInitiative, []domain.Task, int64, error) {
	transaction, err := store.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return domain.DevelopmentInitiative{}, nil, 0, fmt.Errorf("begin initiative observation: %w", err)
	}
	initiative, err := getInitiative(ctx, transaction, handle)
	if err != nil {
		return domain.DevelopmentInitiative{}, nil, 0, errors.Join(err, transaction.Rollback())
	}
	handles := initiativeTaskHandles(initiative)
	tasks := make([]domain.Task, 0, len(handles))
	for _, taskHandle := range handles {
		task, taskErr := getTask(ctx, transaction, taskHandle)
		if taskErr != nil {
			if errors.Is(taskErr, application.ErrNotFound) {
				taskErr = errors.New("initiative member durable state is missing")
			}
			return domain.DevelopmentInitiative{}, nil, 0, errors.Join(taskErr, transaction.Rollback())
		}
		tasks = append(tasks, task)
	}
	stateVersion, err := currentStateVersion(ctx, transaction)
	if err != nil {
		return domain.DevelopmentInitiative{}, nil, 0, errors.Join(err, transaction.Rollback())
	}
	if err := transaction.Commit(); err != nil {
		return domain.DevelopmentInitiative{}, nil, 0, fmt.Errorf("commit initiative observation: %w", err)
	}
	return initiative, tasks, stateVersion, nil
}

// BacklogSnapshot reads one filtered page and its advertised version from one snapshot.
func (store *Store) BacklogSnapshot(
	ctx context.Context,
	filter application.BacklogFilter,
) ([]domain.BacklogItem, string, int64, error) {
	if err := validateBacklogSnapshotFilter(filter); err != nil {
		return nil, "", 0, err
	}
	transaction, err := store.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, "", 0, fmt.Errorf("begin backlog snapshot: %w", err)
	}
	items, nextCursor, err := listBacklogPage(ctx, transaction, filter)
	if err != nil {
		return nil, "", 0, errors.Join(err, transaction.Rollback())
	}
	stateVersion, err := currentStateVersion(ctx, transaction)
	if err != nil {
		return nil, "", 0, errors.Join(err, transaction.Rollback())
	}
	if err := transaction.Commit(); err != nil {
		return nil, "", 0, fmt.Errorf("commit backlog snapshot: %w", err)
	}
	return items, nextCursor, stateVersion, nil
}

func validateBacklogSnapshotFilter(filter application.BacklogFilter) error {
	if filter.RepositoryID != "" && domain.ValidateRepositoryID(filter.RepositoryID) != nil {
		return errors.New("validate backlog snapshot: repository is invalid")
	}
	if filter.Readiness != "" && domain.ValidateBacklogReadiness(filter.Readiness) != nil {
		return errors.New("validate backlog snapshot: readiness is invalid")
	}
	if filter.AfterHandle != "" && domain.ValidateBacklogHandle(filter.AfterHandle) != nil {
		return errors.New("validate backlog snapshot: cursor is invalid")
	}
	if filter.Limit < 1 || filter.Limit > application.MaximumBacklogPage {
		return errors.New("validate backlog snapshot: limit is invalid")
	}
	return nil
}
