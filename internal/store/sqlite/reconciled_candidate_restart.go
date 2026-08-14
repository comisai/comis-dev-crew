package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/comisai/comis-dev-crew/internal/domain"
)

func resumableReconciledCandidateDelivery(
	ctx context.Context,
	transaction *sql.Tx,
	task domain.Task,
	at time.Time,
) (bool, error) {
	if task.State != domain.TaskCandidateComplete {
		return false, nil
	}
	var candidateReports int
	if err := transaction.QueryRowContext(ctx, `SELECT COUNT(*) FROM reports
		WHERE task_handle = ? AND kind = 'candidate_complete'`, task.Handle).Scan(&candidateReports); err != nil {
		return false, fmt.Errorf("inspect restart candidate reports: %w", err)
	}
	if candidateReports != 0 {
		return false, nil
	}
	origin, found, err := readReconciledCandidateOrigin(ctx, transaction, task)
	if err != nil || !found {
		return false, err
	}
	const evidenceQuery = `SELECT task_handle, evidence_digest, canonical,
		required_local_checks_json, required_forge_checks_json,
		outcome, reason, judged_at, state_version
	FROM candidate_evidence WHERE task_handle = ?
	ORDER BY state_version DESC, evidence_digest LIMIT 1`
	row, err := scanCandidateEvidence(transaction.QueryRowContext(ctx, evidenceQuery, task.Handle))
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("read restart candidate evidence: %w", err)
	}
	sealed, err := domain.ParseDeliveryEvidence(row.canonical, row.digest)
	if err != nil {
		return false, errors.New("read restart candidate evidence: stored evidence is invalid")
	}
	bundle := sealed.Bundle()
	if row.judgment.Outcome != domain.CandidateAccepted || row.stateVersion != task.StateVersion ||
		bundle.ProducedAt.After(at) || !bundle.ExpiresAt.After(at) ||
		!reconciledCandidateBundleMatches(task, origin, bundle) {
		return false, nil
	}
	var total, delivered, exact int
	if err := transaction.QueryRowContext(ctx, `SELECT COUNT(*), COUNT(delivered_at),
		COALESCE(SUM(CASE WHEN subject_digest = ? AND state_version = ? THEN 1 ELSE 0 END), 0)
		FROM comis_evidence_outbox WHERE task_handle = ?`,
		row.digest, row.stateVersion, task.Handle,
	).Scan(&total, &delivered, &exact); err != nil {
		return false, fmt.Errorf("inspect restart candidate publications: %w", err)
	}
	return total == 2 && exact == 2 && delivered < total, nil
}
