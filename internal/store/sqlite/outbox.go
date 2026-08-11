package sqlite

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/comisai/comis-dev-crew/internal/application"
	"github.com/comisai/comis-dev-crew/internal/domain"
)

func insertComisReport(ctx context.Context, transaction *sql.Tx, task domain.Task, accepted domain.AcceptedReport) error {
	if task.Handle != accepted.TaskHandle || task.ManagedRunID == "" || task.WorkspaceLeaseID == "" {
		return errors.New("enqueue Comis report: exact task binding is unavailable")
	}
	operationID, serviceReportID := comisReportIDs(task.Handle, accepted.Report.LocalReportID, accepted.SubjectDigest)
	return insertComisReportIdentity(ctx, transaction, operationID, task.Handle, accepted.Report.LocalReportID, serviceReportID)
}

func comisReportIDs(taskHandle, localReportID, subjectDigest string) (string, string) {
	digest := sha256.Sum256([]byte(taskHandle + "\x00" + localReportID + "\x00" + subjectDigest))
	identity := fmt.Sprintf("%x", digest[:16])
	return "report-" + identity, "service-report-" + identity
}

func insertComisReportIdentity(ctx context.Context, transaction *sql.Tx, operationID, taskHandle, localReportID, serviceReportID string) error {
	const insert = `INSERT INTO comis_report_outbox (
        operation_id, task_handle, local_report_id, service_report_id
    ) VALUES (?, ?, ?, ?)`
	if _, err := transaction.ExecContext(ctx, insert, operationID, taskHandle, localReportID, serviceReportID); err != nil {
		if isConstraintError(err) {
			return fmt.Errorf("enqueue Comis report identity: %w", application.ErrConflict)
		}
		return fmt.Errorf("enqueue Comis report: %w", err)
	}
	return nil
}

// NextComisReport returns the oldest accepted report without durable host
// acknowledgement. It never leases or deletes the item, so process loss is a
// safe same-identity resend.
func (store *Store) NextComisReport(ctx context.Context) (application.ComisReportDelivery, bool, error) {
	const query = `SELECT
        o.operation_id, o.service_report_id, t.managed_run_id,
        r.task_handle, r.local_report_id, r.kind, r.external_key,
        r.summary, r.details, r.worker_observed_at, r.state_version
    FROM comis_report_outbox o
    JOIN reports r ON r.task_handle = o.task_handle AND r.local_report_id = o.local_report_id
    JOIN tasks t ON t.handle = r.task_handle
    WHERE o.delivered_at IS NULL
      AND (r.kind != 'candidate_complete' OR (
        t.state IN ('candidate_complete', 'delivering', 'delivered')
        AND (SELECT COUNT(*) FROM comis_evidence_outbox e WHERE e.task_handle = t.handle) = 2
        AND NOT EXISTS (
          SELECT 1 FROM comis_evidence_outbox e
          WHERE e.task_handle = t.handle AND e.delivered_at IS NULL
        )
      ))
    ORDER BY r.state_version, r.task_handle, r.local_report_id
    LIMIT 1`
	var delivery application.ComisReportDelivery
	var observedAt sql.NullString
	err := store.db.QueryRowContext(ctx, query).Scan(
		&delivery.OperationID, &delivery.ServiceReportID, &delivery.ManagedRunID,
		&delivery.TaskHandle, &delivery.LocalReportID, &delivery.Kind, &delivery.ExternalKey,
		&delivery.Summary, &delivery.Details, &observedAt, &delivery.StateVersion,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return application.ComisReportDelivery{}, false, nil
	}
	if err != nil {
		return application.ComisReportDelivery{}, false, fmt.Errorf("read Comis report outbox: %w", err)
	}
	if observedAt.Valid {
		observed, parseErr := parseTime(observedAt.String)
		if parseErr != nil {
			return application.ComisReportDelivery{}, false, fmt.Errorf("parse Comis report observation: %w", parseErr)
		}
		delivery.WorkerObservedAt = &observed
	}
	if err := validateComisDelivery(delivery); err != nil {
		return application.ComisReportDelivery{}, false, err
	}
	return delivery, true, nil
}

// MarkComisReportDelivered durably records only an exact acknowledgement and
// treats an identical repeated mark as success.
func (store *Store) MarkComisReportDelivered(
	ctx context.Context,
	operationID string,
	ack application.ComisReportAcknowledgement,
	deliveredAt time.Time,
) error {
	if err := validateComisAcknowledgement(operationID, ack, deliveredAt); err != nil {
		return err
	}
	transaction, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin Comis report acknowledgement: %w", err)
	}
	defer func() { _ = transaction.Rollback() }()
	const query = `SELECT
        t.managed_run_id, o.service_report_id, o.accepted_sequence,
        o.retained_until, o.delivered_at
    FROM comis_report_outbox o JOIN tasks t ON t.handle = o.task_handle
    WHERE o.operation_id = ?`
	var managedRunID string
	var serviceReportID string
	var acceptedSequence sql.NullInt64
	var retainedUntil sql.NullString
	var priorDeliveredAt sql.NullString
	if err := transaction.QueryRowContext(ctx, query, operationID).Scan(
		&managedRunID, &serviceReportID, &acceptedSequence, &retainedUntil, &priorDeliveredAt,
	); errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("mark Comis report delivered: %w", application.ErrNotFound)
	} else if err != nil {
		return fmt.Errorf("read Comis report acknowledgement: %w", err)
	}
	if managedRunID != ack.ManagedRunID || serviceReportID != ack.ServiceReportID {
		return fmt.Errorf("comis report acknowledgement identity: %w", application.ErrConflict)
	}
	if priorDeliveredAt.Valid {
		if !acceptedSequence.Valid || acceptedSequence.Int64 != ack.AcceptedSequence || !retainedUntil.Valid || retainedUntil.String != formatTime(ack.RetainedUntil) {
			return fmt.Errorf("comis report acknowledgement replay: %w", application.ErrConflict)
		}
		return nil
	}
	const update = `UPDATE comis_report_outbox SET
        accepted_sequence = ?, retained_until = ?, delivered_at = ?
    WHERE operation_id = ? AND delivered_at IS NULL`
	result, err := transaction.ExecContext(ctx, update, ack.AcceptedSequence, formatTime(ack.RetainedUntil), formatTime(deliveredAt), operationID)
	if err != nil {
		return fmt.Errorf("write Comis report acknowledgement: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil || rows != 1 {
		return errors.New("write Comis report acknowledgement: exact item was not updated")
	}
	if err := transaction.Commit(); err != nil {
		return fmt.Errorf("commit Comis report acknowledgement: %w", err)
	}
	return nil
}

func validateComisDelivery(delivery application.ComisReportDelivery) error {
	if err := domain.ValidateOperationID(delivery.OperationID); err != nil {
		return errors.New("read Comis report outbox: invalid operation identity")
	}
	if err := domain.ValidateTaskHandle(delivery.TaskHandle); err != nil {
		return errors.New("read Comis report outbox: invalid task identity")
	}
	if err := domain.ValidateLocalReportID(delivery.LocalReportID); err != nil {
		return errors.New("read Comis report outbox: invalid local report identity")
	}
	if err := domain.ValidateLocalReportID(delivery.ServiceReportID); err != nil {
		return errors.New("read Comis report outbox: invalid service report identity")
	}
	if err := domain.ValidateAuthorityReference("managedRunId", delivery.ManagedRunID); err != nil || delivery.StateVersion < 1 {
		return errors.New("read Comis report outbox: invalid durable binding")
	}
	report := domain.WorkerReport{
		SchemaVersion: 1, LocalReportID: delivery.LocalReportID, BriefRevision: 1,
		BriefRevisionHash: fmt.Sprintf("%064x", 0), Kind: delivery.Kind,
		ExternalKey: delivery.ExternalKey, Summary: delivery.Summary, Details: delivery.Details,
		WorkerObservedAt: delivery.WorkerObservedAt,
	}
	if err := report.Validate(); err != nil {
		return errors.New("read Comis report outbox: invalid sparse report")
	}
	return nil
}

func validateComisAcknowledgement(operationID string, ack application.ComisReportAcknowledgement, deliveredAt time.Time) error {
	if err := domain.ValidateOperationID(operationID); err != nil {
		return errors.New("mark Comis report delivered: invalid operation identity")
	}
	if err := domain.ValidateAuthorityReference("managedRunId", ack.ManagedRunID); err != nil {
		return errors.New("mark Comis report delivered: invalid managed run identity")
	}
	if err := domain.ValidateLocalReportID(ack.ServiceReportID); err != nil || ack.AcceptedSequence < 1 {
		return errors.New("mark Comis report delivered: invalid acknowledgement identity")
	}
	if deliveredAt.IsZero() || deliveredAt.Location() != time.UTC || ack.RetainedUntil.Location() != time.UTC || !ack.RetainedUntil.After(deliveredAt) {
		return errors.New("mark Comis report delivered: invalid acknowledgement times")
	}
	return nil
}
