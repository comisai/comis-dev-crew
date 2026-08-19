package sqlite

import (
	"context"
	"errors"
	"fmt"

	"github.com/comisai/comis-dev-crew/internal/application"
)

const auditMigration = `
CREATE TABLE audit_events (
    sequence INTEGER PRIMARY KEY AUTOINCREMENT,
    occurred_at TEXT NOT NULL,
    kind TEXT NOT NULL,
    task_handle TEXT,
    reason TEXT NOT NULL
);
CREATE INDEX audit_events_task_idx ON audit_events(task_handle, sequence);
INSERT OR IGNORE INTO schema_migrations(version, applied_at)
VALUES (33, strftime('%Y-%m-%dT%H:%M:%fZ', 'now'));
`

// maximumAuditPage bounds one read so a reader cannot ask the service to
// materialize the whole trail.
const maximumAuditPage = 500

// RecordAuditEvent appends one security record on its own transaction.
//
// It deliberately does not join a caller's transaction. Every fact recorded
// here describes work that was refused or rejected, and that work's transaction
// rolls back — sharing it would erase the record along with the attempt.
func (store *Store) RecordAuditEvent(ctx context.Context, event application.AuditEvent) error {
	if store == nil || store.db == nil {
		return errors.New("record audit event: store is unavailable")
	}
	if ctx == nil {
		return errors.New("record audit event: context is required")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := event.Validate(); err != nil {
		return err
	}
	const insert = `INSERT INTO audit_events(occurred_at, kind, task_handle, reason)
        VALUES (?, ?, ?, ?)`
	if _, err := store.db.ExecContext(ctx, insert,
		formatTime(event.OccurredAt), string(event.Kind), event.TaskHandle, string(event.Reason),
	); err != nil {
		return fmt.Errorf("record audit event: %w", err)
	}
	return nil
}

// ReadAuditEvents returns the bounded page of records after the given cursor.
func (store *Store) ReadAuditEvents(
	ctx context.Context,
	afterSequence int64,
	limit int,
) ([]application.AuditEvent, error) {
	if store == nil || store.db == nil {
		return nil, errors.New("read audit events: store is unavailable")
	}
	if ctx == nil {
		return nil, errors.New("read audit events: context is required")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if afterSequence < 0 {
		return nil, errors.New("read audit events: cursor is invalid")
	}
	if limit <= 0 || limit > maximumAuditPage {
		return nil, errors.New("read audit events: page size is invalid")
	}
	const query = `SELECT sequence, occurred_at, kind, task_handle, reason
        FROM audit_events WHERE sequence > ? ORDER BY sequence LIMIT ?`
	rows, err := store.db.QueryContext(ctx, query, afterSequence, limit)
	if err != nil {
		return nil, fmt.Errorf("read audit events: %w", err)
	}
	defer func() { _ = rows.Close() }()
	events := make([]application.AuditEvent, 0, limit)
	for rows.Next() {
		var event application.AuditEvent
		var occurredAt, taskHandle string
		if err := rows.Scan(&event.Sequence, &occurredAt, &event.Kind, &taskHandle, &event.Reason); err != nil {
			return nil, fmt.Errorf("scan audit event: %w", err)
		}
		event.OccurredAt, err = parseTime(occurredAt)
		if err != nil {
			return nil, errors.New("read audit events: stored observation time is invalid")
		}
		event.TaskHandle = taskHandle
		if err := event.Validate(); err != nil {
			return nil, errors.New("read audit events: stored record is invalid")
		}
		events = append(events, event)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read audit events: %w", err)
	}
	return events, nil
}

// auditCleanupReason maps one classified cleanup blocker onto its closed audit
// ground. An unmapped blocker records the weakest honest reason rather than
// inventing a specific one.
func auditCleanupReason(cause error) application.AuditReason {
	switch {
	case errors.Is(cause, application.ErrCleanupOpenHold):
		return application.AuditCleanupOpenHold
	case errors.Is(cause, application.ErrCleanupOpenDecision):
		return application.AuditCleanupOpenDecision
	case errors.Is(cause, application.ErrCleanupUnattestedScout):
		return application.AuditCleanupUnattestedScout
	case errors.Is(cause, application.ErrCleanupActiveExecution):
		return application.AuditCleanupActiveExecution
	case errors.Is(cause, application.ErrCleanupUnknownExecution):
		return application.AuditCleanupUnknownExecution
	default:
		return application.AuditCleanupEvidenceMissing
	}
}

var _ application.AuditRecorder = (*Store)(nil)
var _ application.AuditReader = (*Store)(nil)
