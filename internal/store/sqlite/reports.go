package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/comisai/comis-dev-crew/internal/application"
	"github.com/comisai/comis-dev-crew/internal/domain"
)

// CommitReport atomically deduplicates one authenticated report and advances
// its task cursor/state at the same global state version.
func (store *Store) CommitReport(ctx context.Context, mutation application.ReportMutation) (domain.ReportReceipt, error) {
	if err := validateReportMutation(mutation); err != nil {
		return domain.ReportReceipt{}, err
	}
	transaction, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.ReportReceipt{}, fmt.Errorf("begin report mutation: %w", err)
	}
	defer func() { _ = transaction.Rollback() }()

	accepted, found, err := findAcceptedReport(ctx, transaction, mutation.Report.TaskHandle, mutation.Report.Report.LocalReportID)
	if err != nil {
		return domain.ReportReceipt{}, err
	}
	if found {
		if accepted.SubjectDigest != mutation.SubjectDigest {
			return domain.ReportReceipt{}, fmt.Errorf("report altered replay: %w", application.ErrConflict)
		}
		return reportReceipt(accepted), nil
	}
	task, err := getTask(ctx, transaction, mutation.Report.TaskHandle)
	if err != nil {
		return domain.ReportReceipt{}, err
	}
	if err := validateDecisionReport(ctx, transaction, mutation.Report); err != nil {
		return domain.ReportReceipt{}, err
	}
	updated, err := task.AcceptWorkerReport(mutation.Report.Report, mutation.AcceptedAt)
	if err != nil {
		return domain.ReportReceipt{}, fmt.Errorf("apply worker report: %w", err)
	}
	stateVersion, err := nextMutationStateVersion(ctx, transaction)
	if err != nil {
		return domain.ReportReceipt{}, err
	}
	updated.StateVersion = stateVersion
	if err := updateReportedTask(ctx, transaction, updated); err != nil {
		return domain.ReportReceipt{}, err
	}
	accepted = domain.AcceptedReport{
		TaskHandle: mutation.Report.TaskHandle, Report: mutation.Report.Report,
		SubjectDigest: mutation.SubjectDigest, StateVersion: stateVersion, AcceptedAt: mutation.AcceptedAt,
	}
	if err := insertAcceptedReport(ctx, transaction, accepted); err != nil {
		return domain.ReportReceipt{}, err
	}
	if err := insertComisReport(ctx, transaction, task, accepted); err != nil {
		return domain.ReportReceipt{}, err
	}
	if err := transaction.Commit(); err != nil {
		return domain.ReportReceipt{}, fmt.Errorf("commit report mutation: %w", err)
	}
	return reportReceipt(accepted), nil
}

func validateReportMutation(mutation application.ReportMutation) error {
	if err := domain.ValidateTaskHandle(mutation.Report.TaskHandle); err != nil {
		return errors.New("commit report: invalid task scope")
	}
	accepted := domain.AcceptedReport{
		TaskHandle: mutation.Report.TaskHandle, Report: mutation.Report.Report,
		SubjectDigest: mutation.SubjectDigest, StateVersion: 1, AcceptedAt: mutation.AcceptedAt,
	}
	if err := accepted.Validate(); err != nil {
		return errors.New("commit report: invalid report mutation")
	}
	return nil
}

func validateDecisionReport(ctx context.Context, transaction *sql.Tx, report domain.AuthenticatedReport) error {
	if report.Report.Kind != domain.ReportDecision && report.Report.Kind != domain.ReportResolution {
		return nil
	}
	const query = `SELECT
        COALESCE(SUM(CASE WHEN kind = 'decision' THEN 1 ELSE 0 END), 0),
        COALESCE(SUM(CASE WHEN kind = 'resolution' THEN 1 ELSE 0 END), 0)
    FROM reports WHERE task_handle = ? AND external_key = ?`
	var decisions int
	var resolutions int
	if err := transaction.QueryRowContext(ctx, query, report.TaskHandle, report.Report.ExternalKey).Scan(&decisions, &resolutions); err != nil {
		return fmt.Errorf("inspect report decision key: %w", err)
	}
	if report.Report.Kind == domain.ReportDecision && decisions != 0 {
		return fmt.Errorf("decision key replay uses a new report identity: %w", application.ErrConflict)
	}
	if report.Report.Kind == domain.ReportResolution && (decisions != 1 || resolutions != 0) {
		return fmt.Errorf("resolution does not match one unresolved decision: %w", application.ErrConflict)
	}
	return nil
}

func updateReportedTask(ctx context.Context, transaction *sql.Tx, task domain.Task) error {
	if err := task.Validate(); err != nil {
		return fmt.Errorf("validate reported task: %w", err)
	}
	const update = `UPDATE tasks SET state = ?, report_cursor = ?, state_version = ?, updated_at = ? WHERE handle = ?`
	result, err := transaction.ExecContext(ctx, update, task.State, task.ReportCursor, task.StateVersion, formatTime(task.UpdatedAt), task.Handle)
	if err != nil {
		return fmt.Errorf("update reported task: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil || rows != 1 {
		return errors.New("update reported task: exact task was not updated")
	}
	return nil
}

func insertAcceptedReport(ctx context.Context, transaction *sql.Tx, accepted domain.AcceptedReport) error {
	if err := accepted.Validate(); err != nil {
		return err
	}
	var observedAt any
	if accepted.Report.WorkerObservedAt != nil {
		observedAt = formatTime(*accepted.Report.WorkerObservedAt)
	}
	const insert = `INSERT INTO reports (
        task_handle, local_report_id, subject_digest, schema_version,
        brief_revision, brief_revision_hash, kind, external_key, summary,
        details, worker_observed_at, state_version, accepted_at
    ) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`
	_, err := transaction.ExecContext(ctx, insert,
		accepted.TaskHandle, accepted.Report.LocalReportID, accepted.SubjectDigest,
		accepted.Report.SchemaVersion, accepted.Report.BriefRevision, accepted.Report.BriefRevisionHash,
		accepted.Report.Kind, accepted.Report.ExternalKey, accepted.Report.Summary, accepted.Report.Details,
		observedAt, accepted.StateVersion, formatTime(accepted.AcceptedAt),
	)
	if isConstraintError(err) {
		return fmt.Errorf("insert accepted report: %w", application.ErrConflict)
	}
	if err != nil {
		return fmt.Errorf("insert accepted report: %w", err)
	}
	return nil
}

// ListAcceptedReports returns exact durable report evidence ordered by its
// joined task state version.
func (store *Store) ListAcceptedReports(ctx context.Context, taskHandle string) (reports []domain.AcceptedReport, resultErr error) {
	if err := domain.ValidateTaskHandle(taskHandle); err != nil {
		return nil, errors.New("list accepted reports: invalid task handle")
	}
	const query = `SELECT
        task_handle, local_report_id, subject_digest, schema_version,
        brief_revision, brief_revision_hash, kind, external_key, summary,
        details, worker_observed_at, state_version, accepted_at
    FROM reports WHERE task_handle = ? ORDER BY state_version, local_report_id`
	rows, err := store.db.QueryContext(ctx, query, taskHandle)
	if err != nil {
		return nil, fmt.Errorf("list accepted reports: %w", err)
	}
	defer func() { resultErr = errors.Join(resultErr, rows.Close()) }()
	for rows.Next() {
		accepted, err := scanAcceptedReport(rows)
		if err != nil {
			return nil, fmt.Errorf("list accepted reports: %w", err)
		}
		reports = append(reports, accepted)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list accepted reports: %w", err)
	}
	return reports, nil
}

func findAcceptedReport(ctx context.Context, source queryer, taskHandle, localReportID string) (domain.AcceptedReport, bool, error) {
	const query = `SELECT
        task_handle, local_report_id, subject_digest, schema_version,
        brief_revision, brief_revision_hash, kind, external_key, summary,
        details, worker_observed_at, state_version, accepted_at
    FROM reports WHERE task_handle = ? AND local_report_id = ?`
	accepted, err := scanAcceptedReport(source.QueryRowContext(ctx, query, taskHandle, localReportID))
	if errors.Is(err, sql.ErrNoRows) {
		return domain.AcceptedReport{}, false, nil
	}
	if err != nil {
		return domain.AcceptedReport{}, false, fmt.Errorf("find accepted report: %w", err)
	}
	return accepted, true, nil
}

func scanAcceptedReport(row rowScanner) (domain.AcceptedReport, error) {
	var accepted domain.AcceptedReport
	var observedAt sql.NullString
	var acceptedAt string
	if err := row.Scan(
		&accepted.TaskHandle, &accepted.Report.LocalReportID, &accepted.SubjectDigest,
		&accepted.Report.SchemaVersion, &accepted.Report.BriefRevision, &accepted.Report.BriefRevisionHash,
		&accepted.Report.Kind, &accepted.Report.ExternalKey, &accepted.Report.Summary, &accepted.Report.Details,
		&observedAt, &accepted.StateVersion, &acceptedAt,
	); err != nil {
		return domain.AcceptedReport{}, err
	}
	var err error
	if observedAt.Valid {
		observed, parseErr := parseTime(observedAt.String)
		if parseErr != nil {
			return domain.AcceptedReport{}, fmt.Errorf("parse worker observation time: %w", parseErr)
		}
		accepted.Report.WorkerObservedAt = &observed
	}
	accepted.AcceptedAt, err = parseTime(acceptedAt)
	if err != nil {
		return domain.AcceptedReport{}, fmt.Errorf("parse report acceptance time: %w", err)
	}
	if err := accepted.Validate(); err != nil {
		return domain.AcceptedReport{}, fmt.Errorf("validate accepted report: %w", err)
	}
	return accepted, nil
}

func reportReceipt(accepted domain.AcceptedReport) domain.ReportReceipt {
	return domain.ReportReceipt{
		TaskHandle: accepted.TaskHandle, LocalReportID: accepted.Report.LocalReportID,
		StateVersion: accepted.StateVersion, AcceptedAt: accepted.AcceptedAt,
	}
}
