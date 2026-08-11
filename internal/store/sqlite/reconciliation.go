package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/comisai/comis-dev-crew/internal/application"
	"github.com/comisai/comis-dev-crew/internal/domain"
)

// ReconcileStartup atomically converts worker-runtime-sensitive task states and
// accepted operation outcomes to unknown before the service becomes ready.
// Validating tasks remain durable so the candidate supervisor can recover them.
func (store *Store) ReconcileStartup(ctx context.Context, at time.Time) (application.StartupReconciliation, error) {
	if at.IsZero() || at.Location() != time.UTC {
		return application.StartupReconciliation{}, errors.New("reconcile startup: UTC service time is required")
	}
	transaction, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return application.StartupReconciliation{}, fmt.Errorf("begin startup reconciliation: %w", err)
	}
	defer func() { _ = transaction.Rollback() }()
	result := application.StartupReconciliation{}

	tasks, err := listTasks(ctx, transaction)
	if err != nil {
		return result, err
	}
	for _, task := range tasks {
		if !runtimeSensitiveState(task.State) {
			continue
		}
		unknown, err := reconcileTaskUnknown(task, at)
		if err != nil {
			return result, fmt.Errorf("reconcile task startup state: %w", err)
		}
		version, err := nextReconciliationVersion(ctx, transaction)
		if err != nil {
			return result, err
		}
		unknown.StateVersion = version
		if err := updateTaskState(ctx, transaction, unknown); err != nil {
			return result, err
		}
		result.TasksMarkedUnknown++
	}

	operationIDs, err := acceptedOperationIDs(ctx, transaction)
	if err != nil {
		return result, err
	}
	for _, operationID := range operationIDs {
		operation, err := getOperation(ctx, transaction, operationID)
		if err != nil {
			return result, err
		}
		version, err := nextReconciliationVersion(ctx, transaction)
		if err != nil {
			return result, err
		}
		operation.Status = domain.OperationUnknown
		operation.StateVersion = version
		operation.UpdatedAt = at
		if err := updateReconciledOperation(ctx, transaction, operation); err != nil {
			return result, err
		}
		result.OperationsMarkedUnknown++
	}
	result.StateVersion, err = currentStateVersion(ctx, transaction)
	if err != nil {
		return result, err
	}
	if err := transaction.Commit(); err != nil {
		return application.StartupReconciliation{}, fmt.Errorf("commit startup reconciliation: %w", err)
	}
	return result, nil
}

func runtimeSensitiveState(state domain.TaskState) bool {
	switch state {
	case domain.TaskLaunching, domain.TaskWorking, domain.TaskAwaitingDecision,
		domain.TaskBlocked, domain.TaskPaused, domain.TaskReconciling,
		domain.TaskCandidateComplete, domain.TaskDelivering:
		return true
	default:
		return false
	}
}

func reconcileTaskUnknown(task domain.Task, at time.Time) (domain.Task, error) {
	reconciling := task
	var err error
	if task.State != domain.TaskReconciling {
		reconciling, err = task.ApplyTransition(domain.TransitionReconcileRequired, at)
		if err != nil {
			return task, err
		}
	}
	unknown, err := reconciling.ApplyTransition(domain.TransitionReconciliationUnresolved, at)
	if err != nil {
		return task, err
	}
	return unknown, nil
}

func nextReconciliationVersion(ctx context.Context, transaction *sql.Tx) (int64, error) {
	current, err := currentStateVersion(ctx, transaction)
	if err != nil {
		return 0, err
	}
	if current == math.MaxInt64 {
		return 0, errors.New("startup reconciliation state version is exhausted")
	}
	return current + 1, nil
}

func acceptedOperationIDs(ctx context.Context, transaction *sql.Tx) (ids []string, resultErr error) {
	rows, err := transaction.QueryContext(ctx, "SELECT id FROM operations WHERE status = ? ORDER BY id", domain.OperationAccepted)
	if err != nil {
		return nil, fmt.Errorf("list accepted operations: %w", err)
	}
	defer func() { resultErr = errors.Join(resultErr, rows.Close()) }()
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scan accepted operation ID: %w", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list accepted operations: %w", err)
	}
	return ids, nil
}

func updateReconciledOperation(ctx context.Context, transaction *sql.Tx, operation domain.OperationRecord) error {
	if err := operation.Validate(); err != nil {
		return fmt.Errorf("validate reconciled operation: %w", err)
	}
	const update = `UPDATE operations SET status = ?, error_code = ?, state_version = ?, updated_at = ?
        WHERE id = ? AND status = ?`
	result, err := transaction.ExecContext(ctx, update,
		operation.Status, operation.ErrorCode, operation.StateVersion,
		formatTime(operation.UpdatedAt), operation.ID, domain.OperationAccepted,
	)
	if err != nil {
		return fmt.Errorf("update reconciled operation: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil || rows != 1 {
		return errors.New("update reconciled operation: exact accepted operation was not updated")
	}
	return nil
}
