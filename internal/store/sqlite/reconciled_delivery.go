package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/comisai/comis-dev-crew/internal/application"
	"github.com/comisai/comis-dev-crew/internal/domain"
)

// completeReconciledCandidateDelivery closes the server-owned recovery lane
// after both exact evidence publications are acknowledged. A worker report is
// neither required nor fabricated for this origin.
func completeReconciledCandidateDelivery(
	ctx context.Context,
	transaction *sql.Tx,
	evidenceOperationID string,
	deliveredAt time.Time,
) error {
	var taskHandle string
	if err := transaction.QueryRowContext(ctx, `SELECT task_handle
		FROM comis_evidence_outbox WHERE operation_id = ?`, evidenceOperationID).Scan(&taskHandle); err != nil {
		return fmt.Errorf("read reconciled candidate delivery task: %w", err)
	}
	task, err := getTask(ctx, transaction, taskHandle)
	if err != nil {
		return err
	}
	if task.State != domain.TaskCandidateComplete {
		return nil
	}
	var candidateReports int
	if err := transaction.QueryRowContext(ctx, `SELECT COUNT(*) FROM reports
		WHERE task_handle = ? AND kind = 'candidate_complete'`, taskHandle).Scan(&candidateReports); err != nil {
		return fmt.Errorf("inspect reconciled candidate reports: %w", err)
	}
	if candidateReports != 0 {
		return nil
	}
	reconciliationHead, found, err := exactDeliveryReconciliation(ctx, transaction, task)
	if err != nil {
		return err
	}
	if !found {
		return nil
	}
	sealed, judgment, err := latestCandidateEvidenceFrom(ctx, transaction, taskHandle)
	if err != nil {
		return err
	}
	bundle := sealed.Bundle()
	if judgment.Outcome != domain.CandidateAccepted || bundle.TaskHandle != task.Handle ||
		bundle.RepositoryIdentity != task.RepositoryID || bundle.BaseRevision != task.BaseRevision ||
		bundle.HeadRevision != reconciliationHead {
		return fmt.Errorf("reconciled candidate delivery evidence differs: %w", application.ErrPrecondition)
	}
	var total, delivered int
	if err := transaction.QueryRowContext(ctx, `SELECT COUNT(*), COUNT(delivered_at)
		FROM comis_evidence_outbox WHERE task_handle = ?`, taskHandle).Scan(&total, &delivered); err != nil {
		return fmt.Errorf("inspect reconciled candidate publications: %w", err)
	}
	if total != 2 || delivered != 2 {
		return nil
	}
	delivering, err := task.ApplyTransition(domain.TransitionDeliveryStarted, deliveredAt)
	if err != nil {
		return fmt.Errorf("start reconciled candidate delivery: %w", err)
	}
	completed, err := delivering.ApplyTransition(domain.TransitionDeliveryAccepted, deliveredAt)
	if err != nil {
		return fmt.Errorf("complete reconciled candidate delivery: %w", err)
	}
	stateVersion, err := nextMutationStateVersion(ctx, transaction)
	if err != nil {
		return err
	}
	completed.StateVersion = stateVersion
	if err := updateTaskState(ctx, transaction, completed); err != nil {
		return err
	}
	return nil
}

func exactDeliveryReconciliation(
	ctx context.Context,
	transaction *sql.Tx,
	task domain.Task,
) (string, bool, error) {
	const query = `SELECT reconciliation.head_revision
		FROM task_candidate_reconciliations reconciliation
		JOIN operations operation ON operation.id = reconciliation.operation_id
		WHERE reconciliation.task_handle = ? AND reconciliation.action = ?
		AND reconciliation.repository_id = ? AND reconciliation.base_revision = ?
		AND reconciliation.cleanliness = ?
		AND operation.command = ? AND operation.status = ?
		AND operation.result_ref = reconciliation.task_handle
		AND operation.state_version = reconciliation.completed_state_version
		AND reconciliation.started_state_version + 1 = reconciliation.completed_state_version
		AND reconciliation.completed_state_version <= ?
		ORDER BY reconciliation.completed_state_version DESC, reconciliation.operation_id`
	rows, err := transaction.QueryContext(ctx, query,
		task.Handle, application.ReconcileValidateCleanCandidate,
		task.RepositoryID, task.BaseRevision, application.WorkspaceClean,
		commandReconcileTask, domain.OperationCompleted, task.StateVersion,
	)
	if err != nil {
		return "", false, fmt.Errorf("read reconciled candidate delivery origin: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var head string
	count := 0
	for rows.Next() {
		if err := rows.Scan(&head); err != nil {
			return "", false, fmt.Errorf("scan reconciled candidate delivery origin: %w", err)
		}
		count++
	}
	if err := rows.Err(); err != nil {
		return "", false, fmt.Errorf("read reconciled candidate delivery origin: %w", err)
	}
	if count > 1 {
		return "", false, fmt.Errorf("reconciled candidate delivery origin is ambiguous: %w", application.ErrPrecondition)
	}
	return head, count == 1, nil
}

func latestCandidateEvidenceFrom(
	ctx context.Context,
	source queryer,
	taskHandle string,
) (*domain.SealedDeliveryEvidence, domain.CandidateJudgment, error) {
	const query = `SELECT task_handle, evidence_digest, canonical,
		required_local_checks_json, required_forge_checks_json,
		outcome, reason, judged_at, state_version
	FROM candidate_evidence WHERE task_handle = ?
	ORDER BY state_version DESC, evidence_digest LIMIT 1`
	row, err := scanCandidateEvidence(source.QueryRowContext(ctx, query, taskHandle))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, domain.CandidateJudgment{}, fmt.Errorf("read reconciled candidate evidence: %w", application.ErrNotFound)
	}
	if err != nil {
		return nil, domain.CandidateJudgment{}, fmt.Errorf("read reconciled candidate evidence: %w", err)
	}
	sealed, err := domain.ParseDeliveryEvidence(row.canonical, row.digest)
	if err != nil {
		return nil, domain.CandidateJudgment{}, errors.New("read reconciled candidate evidence: stored evidence is invalid")
	}
	return sealed, row.judgment, nil
}
