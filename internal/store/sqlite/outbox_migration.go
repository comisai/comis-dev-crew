package sqlite

import (
	"context"
	"errors"
	"fmt"
)

func (store *Store) applyComisReportOutboxMigration(ctx context.Context) error {
	var applied int
	if err := store.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM schema_migrations WHERE version = 7").Scan(&applied); err != nil {
		return fmt.Errorf("inspect SQLite migration 7: %w", err)
	}
	if applied == 1 {
		return nil
	}
	transaction, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin SQLite migration 7: %w", err)
	}
	defer func() { _ = transaction.Rollback() }()
	if _, err := transaction.ExecContext(ctx, comisReportOutboxMigration); err != nil {
		return fmt.Errorf("apply SQLite migration 7: %w", err)
	}
	type priorReport struct{ taskHandle, localReportID, subjectDigest, managedRunID, workspaceLeaseID string }
	rows, err := transaction.QueryContext(ctx, `SELECT
        r.task_handle, r.local_report_id, r.subject_digest, t.managed_run_id, t.workspace_lease_id
    FROM reports r JOIN tasks t ON t.handle = r.task_handle
    ORDER BY r.task_handle, r.local_report_id`)
	if err != nil {
		return fmt.Errorf("read migration 7 reports: %w", err)
	}
	var reports []priorReport
	for rows.Next() {
		var report priorReport
		if err := rows.Scan(&report.taskHandle, &report.localReportID, &report.subjectDigest, &report.managedRunID, &report.workspaceLeaseID); err != nil {
			_ = rows.Close()
			return fmt.Errorf("scan migration 7 report: %w", err)
		}
		reports = append(reports, report)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return fmt.Errorf("read migration 7 reports: %w", err)
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("close migration 7 reports: %w", err)
	}
	for _, report := range reports {
		if report.managedRunID == "" || report.workspaceLeaseID == "" {
			return errors.New("backfill migration 7 report: exact task binding is unavailable")
		}
		operationID, serviceReportID := comisReportIDs(report.taskHandle, report.localReportID, report.subjectDigest)
		if err := insertComisReportIdentity(ctx, transaction, operationID, report.taskHandle, report.localReportID, serviceReportID); err != nil {
			return fmt.Errorf("backfill migration 7 report: %w", err)
		}
	}
	if _, err := transaction.ExecContext(ctx, `INSERT INTO schema_migrations(version, applied_at)
        VALUES (7, strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))`); err != nil {
		return fmt.Errorf("record SQLite migration 7: %w", err)
	}
	if err := transaction.Commit(); err != nil {
		return fmt.Errorf("commit SQLite migration 7: %w", err)
	}
	return nil
}
