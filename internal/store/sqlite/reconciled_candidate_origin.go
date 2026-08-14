package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/comisai/comis-dev-crew/internal/application"
	"github.com/comisai/comis-dev-crew/internal/domain"
)

type reconciledCandidateOrigin struct {
	snapshot              application.WorkspaceSnapshot
	completedStateVersion int64
}

// ReadReconciledCandidateSnapshot returns the immutable recovery snapshot that
// owns validation, when the validating task came from candidate reconciliation.
func (store *Store) ReadReconciledCandidateSnapshot(
	ctx context.Context,
	taskHandle string,
) (application.WorkspaceSnapshot, bool, error) {
	if store == nil || store.db == nil || ctx == nil || domain.ValidateTaskHandle(taskHandle) != nil {
		return application.WorkspaceSnapshot{}, false, errors.New("read reconciled candidate snapshot: input is invalid")
	}
	transaction, err := store.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return application.WorkspaceSnapshot{}, false, fmt.Errorf("begin reconciled candidate snapshot read: %w", err)
	}
	defer func() { _ = transaction.Rollback() }()
	task, err := getTask(ctx, transaction, taskHandle)
	if err != nil {
		return application.WorkspaceSnapshot{}, false, err
	}
	if task.State != domain.TaskValidating {
		return application.WorkspaceSnapshot{}, false, fmt.Errorf("reconciled candidate snapshot posture: %w", application.ErrPrecondition)
	}
	origin, found, err := readReconciledCandidateOrigin(ctx, transaction, task)
	if err != nil {
		return application.WorkspaceSnapshot{}, false, err
	}
	if err := transaction.Commit(); err != nil {
		return application.WorkspaceSnapshot{}, false, fmt.Errorf("commit reconciled candidate snapshot read: %w", err)
	}
	return origin.snapshot, found, nil
}

func readReconciledCandidateOrigin(
	ctx context.Context,
	source queryer,
	task domain.Task,
) (reconciledCandidateOrigin, bool, error) {
	const query = `SELECT
		reconciliation.task_handle, reconciliation.repository_id,
		reconciliation.worktree_path, reconciliation.branch,
		reconciliation.head_revision, reconciliation.cleanliness,
		reconciliation.completed_state_version
	FROM task_candidate_reconciliations reconciliation
	JOIN operations operation ON operation.id = reconciliation.operation_id
	JOIN operations preparation_operation
		ON preparation_operation.id = reconciliation.preparation_operation_id
	JOIN task_preparations preparation
		ON preparation.task_handle = reconciliation.task_handle
	WHERE reconciliation.task_handle = ? AND reconciliation.action = ?
	AND reconciliation.repository_id = ? AND reconciliation.base_revision = ?
	AND reconciliation.cleanliness = ?
	AND preparation.requested_workspace_root = reconciliation.worktree_path
	AND preparation.state = ?
	AND operation.command = ? AND operation.status = ?
	AND operation.result_ref = reconciliation.task_handle
	AND operation.state_version = reconciliation.completed_state_version
	AND preparation_operation.command = ? AND preparation_operation.status = ?
	AND preparation_operation.result_ref = reconciliation.task_handle
	AND reconciliation.started_state_version + 1 = reconciliation.completed_state_version
	AND reconciliation.completed_state_version <= ?
	ORDER BY reconciliation.completed_state_version DESC, reconciliation.operation_id`
	rows, err := source.QueryContext(ctx, query,
		task.Handle, application.ReconcileValidateCleanCandidate,
		task.RepositoryID, task.BaseRevision, application.WorkspaceClean,
		application.PreparationOpen, commandReconcileTask, domain.OperationCompleted,
		commandPrepareTask, domain.OperationCompleted, task.StateVersion,
	)
	if err != nil {
		return reconciledCandidateOrigin{}, false, fmt.Errorf("read reconciled candidate origin: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var origin reconciledCandidateOrigin
	count := 0
	for rows.Next() {
		if err := rows.Scan(
			&origin.snapshot.TaskHandle, &origin.snapshot.RepositoryID,
			&origin.snapshot.WorktreePath, &origin.snapshot.Branch,
			&origin.snapshot.HeadRevision, &origin.snapshot.Cleanliness,
			&origin.completedStateVersion,
		); err != nil {
			return reconciledCandidateOrigin{}, false, fmt.Errorf("scan reconciled candidate origin: %w", err)
		}
		count++
	}
	if err := rows.Err(); err != nil {
		return reconciledCandidateOrigin{}, false, fmt.Errorf("read reconciled candidate origin: %w", err)
	}
	var durableCount int
	if err := source.QueryRowContext(ctx, `SELECT COUNT(*) FROM task_candidate_reconciliations
		WHERE task_handle = ?`, task.Handle).Scan(&durableCount); err != nil {
		return reconciledCandidateOrigin{}, false, fmt.Errorf("count reconciled candidate origins: %w", err)
	}
	if durableCount > 1 || count > 1 {
		return reconciledCandidateOrigin{}, false, fmt.Errorf("reconciled candidate origin is ambiguous: %w", application.ErrPrecondition)
	}
	if durableCount != count {
		return reconciledCandidateOrigin{}, false, fmt.Errorf("reconciled candidate origin is incomplete: %w", application.ErrPrecondition)
	}
	if count == 0 {
		return reconciledCandidateOrigin{}, false, nil
	}
	if origin.snapshot.Validate() != nil || origin.snapshot.Cleanliness != application.WorkspaceClean ||
		origin.completedStateVersion < 1 {
		return reconciledCandidateOrigin{}, false, errors.New("reconciled candidate origin is invalid")
	}
	return origin, true, nil
}

func candidateRecoveryHistoryExists(ctx context.Context, source queryer, taskHandle string) (bool, error) {
	var found int
	if err := source.QueryRowContext(ctx, `SELECT
		EXISTS(SELECT 1 FROM task_candidate_reconciliations WHERE task_handle = ?) OR
		EXISTS(SELECT 1 FROM candidate_evidence WHERE task_handle = ?) OR
		EXISTS(SELECT 1 FROM comis_evidence_outbox WHERE task_handle = ?)`,
		taskHandle, taskHandle, taskHandle,
	).Scan(&found); err != nil {
		return false, fmt.Errorf("inspect task candidate recovery history: %w", err)
	}
	return found == 1, nil
}

func validateReconciledCandidateEvidenceAuthority(
	ctx context.Context,
	source queryer,
	task domain.Task,
	evidence *domain.SealedDeliveryEvidence,
) error {
	origin, found, err := readReconciledCandidateOrigin(ctx, source, task)
	if err != nil {
		return err
	}
	if !found {
		return nil
	}
	if !reconciledCandidateBundleMatches(task, origin, evidence.Bundle()) {
		return fmt.Errorf("candidate evidence differs from reconciliation authority: %w", application.ErrPrecondition)
	}
	return nil
}

func reconciledCandidateBundleMatches(
	task domain.Task,
	origin reconciledCandidateOrigin,
	bundle domain.DeliveryEvidenceBundle,
) bool {
	return bundle.TaskHandle == task.Handle && bundle.RepositoryIdentity == task.RepositoryID &&
		bundle.BaseRevision == task.BaseRevision && bundle.HeadRevision == origin.snapshot.HeadRevision &&
		bundle.WorktreeCleanliness == domain.WorktreeClean
}
