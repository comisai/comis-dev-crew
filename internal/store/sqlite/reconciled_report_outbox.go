package sqlite

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"

	"github.com/comisai/comis-dev-crew/internal/application"
	"github.com/comisai/comis-dev-crew/internal/domain"
)

const reconciledReportOutboxMigration = `
CREATE TABLE comis_reconciled_report_outbox (
    operation_id TEXT PRIMARY KEY,
    task_handle TEXT NOT NULL UNIQUE,
    local_report_id TEXT NOT NULL UNIQUE,
    service_report_id TEXT NOT NULL UNIQUE,
    summary TEXT NOT NULL,
    state_version INTEGER NOT NULL,
    accepted_sequence INTEGER,
    retained_until TEXT,
    delivered_at TEXT,
    FOREIGN KEY(task_handle) REFERENCES tasks(handle)
);
CREATE INDEX comis_reconciled_report_outbox_pending_idx
ON comis_reconciled_report_outbox(delivered_at, state_version, task_handle);
INSERT OR IGNORE INTO schema_migrations(version, applied_at)
VALUES (42, strftime('%Y-%m-%dT%H:%M:%fZ', 'now'));
`

const reconciledCandidateSummary = "Candidate validated from exact workspace reconciliation."

func insertReconciledComisReport(
	ctx context.Context,
	target execQueryer,
	task domain.Task,
	evidence *domain.SealedDeliveryEvidence,
	stateVersion int64,
) error {
	origin, found, err := readReconciledCandidateOrigin(ctx, target, task)
	if err != nil || !found {
		return err
	}
	if evidence == nil || stateVersion < 1 || !reconciledCandidateBundleMatches(task, origin, evidence.Bundle()) {
		return fmt.Errorf("enqueue reconciled Comis report: authority differs: %w", application.ErrPrecondition)
	}
	operationID, localReportID, serviceReportID := reconciledReportIDs(task.Handle, evidence.Digest())
	const insert = `INSERT INTO comis_reconciled_report_outbox (
        operation_id, task_handle, local_report_id, service_report_id, summary, state_version
    ) VALUES (?, ?, ?, ?, ?, ?)`
	if _, err := target.ExecContext(ctx, insert, operationID, task.Handle, localReportID,
		serviceReportID, reconciledCandidateSummary, stateVersion); isConstraintError(err) {
		return fmt.Errorf("enqueue reconciled Comis report identity: %w", application.ErrConflict)
	} else if err != nil {
		return fmt.Errorf("enqueue reconciled Comis report: %w", err)
	}
	return nil
}

func reconciledReportIDs(taskHandle, evidenceDigest string) (string, string, string) {
	digest := sha256.Sum256([]byte(taskHandle + "\x00" + evidenceDigest))
	identity := fmt.Sprintf("%x", digest[:16])
	return "reconciled-report-" + identity,
		"reconciled-candidate-" + identity,
		"service-report-" + identity
}

// backfillReconciledComisReports closes projection gaps for exact reconciled
// candidates that completed before the service-owned terminal outbox existed.
func (store *Store) backfillReconciledComisReports(ctx context.Context) error {
	rows, err := store.db.QueryContext(ctx, `SELECT DISTINCT evidence.task_handle
		FROM candidate_evidence evidence
		JOIN tasks task ON task.handle = evidence.task_handle
		JOIN task_candidate_reconciliations reconciliation
			ON reconciliation.task_handle = evidence.task_handle
		WHERE evidence.outcome = 'accepted'
		AND task.state IN ('candidate_complete', 'delivering', 'delivered', 'cleanup_held', 'cleaned')
		AND NOT EXISTS (
			SELECT 1 FROM reports report
			WHERE report.task_handle = evidence.task_handle AND report.kind = 'candidate_complete'
		)
		AND NOT EXISTS (
			SELECT 1 FROM comis_reconciled_report_outbox outbox
			WHERE outbox.task_handle = evidence.task_handle
		)
		ORDER BY evidence.task_handle`)
	if err != nil {
		return fmt.Errorf("read reconciled Comis report backfill: %w", err)
	}
	var taskHandles []string
	for rows.Next() {
		var taskHandle string
		if err := rows.Scan(&taskHandle); err != nil {
			_ = rows.Close()
			return fmt.Errorf("scan reconciled Comis report backfill: %w", err)
		}
		taskHandles = append(taskHandles, taskHandle)
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return fmt.Errorf("read reconciled Comis report backfill: %w", err)
	}
	for _, taskHandle := range taskHandles {
		if err := store.backfillReconciledComisReport(ctx, taskHandle); err != nil {
			return err
		}
	}
	return nil
}

func (store *Store) backfillReconciledComisReport(ctx context.Context, taskHandle string) error {
	transaction, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin reconciled Comis report backfill: %w", err)
	}
	defer func() { _ = transaction.Rollback() }()
	task, err := getTask(ctx, transaction, taskHandle)
	if err != nil {
		return err
	}
	evidence, judgment, err := latestCandidateEvidenceFrom(ctx, transaction, taskHandle)
	if err != nil {
		return err
	}
	if judgment.Outcome != domain.CandidateAccepted {
		return errors.New("backfill reconciled Comis report: candidate is not accepted")
	}
	var total, exact int
	if err := transaction.QueryRowContext(ctx, `SELECT COUNT(*),
		COALESCE(SUM(CASE WHEN subject_digest = ? THEN 1 ELSE 0 END), 0)
		FROM comis_evidence_outbox WHERE task_handle = ?`, evidence.Digest(), taskHandle).Scan(&total, &exact); err != nil {
		return fmt.Errorf("inspect reconciled Comis report publications: %w", err)
	}
	if total != 2 || exact != 2 {
		return fmt.Errorf("backfill reconciled Comis report: exact publications are unavailable: %w", application.ErrPrecondition)
	}
	if err := insertReconciledComisReport(ctx, transaction, task, evidence, task.StateVersion); err != nil {
		return err
	}
	if err := transaction.Commit(); err != nil {
		return fmt.Errorf("commit reconciled Comis report backfill: %w", err)
	}
	return nil
}

type execQueryer interface {
	execer
	queryer
}

var _ execQueryer = (*sql.Tx)(nil)
