package sqlite

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/comisai/comis-dev-crew/internal/application"
)

const runtimeAttachmentRecoveryRefusalMigration = `
CREATE TABLE runtime_attachment_recovery_refusals (
    operation_id TEXT PRIMARY KEY REFERENCES task_preparation_intents(operation_id) ON DELETE CASCADE,
    reason TEXT NOT NULL CHECK(reason = 'unproven_filesystem_authority'),
    refused_at TEXT NOT NULL
);
INSERT OR IGNORE INTO schema_migrations(version, applied_at)
VALUES (22, strftime('%Y-%m-%dT%H:%M:%fZ', 'now'));
`

// ListRuntimeAttachmentRecoveryRefusals returns closed preparation-scoped recovery refusals.
func (store *Store) ListRuntimeAttachmentRecoveryRefusals(
	ctx context.Context,
) ([]application.RuntimeAttachmentRecoveryRefusal, error) {
	rows, err := store.db.QueryContext(ctx, `SELECT i.operation_id, i.task_handle, i.subject_digest,
		r.reason, r.refused_at
		FROM runtime_attachment_recovery_refusals r
		JOIN task_preparation_intents i ON i.operation_id = r.operation_id
		ORDER BY i.task_handle, i.operation_id`)
	if err != nil {
		return nil, fmt.Errorf("list runtime attachment recovery refusals: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var refusals []application.RuntimeAttachmentRecoveryRefusal
	for rows.Next() {
		var refusal application.RuntimeAttachmentRecoveryRefusal
		var refusedAt string
		if err := rows.Scan(
			&refusal.OperationID, &refusal.TaskHandle, &refusal.SubjectDigest, &refusal.Reason, &refusedAt,
		); err != nil {
			return nil, fmt.Errorf("scan runtime attachment recovery refusal: %w", err)
		}
		refusal.RefusedAt, err = parseTime(refusedAt)
		if err != nil || refusal.Validate() != nil {
			return nil, errors.New("stored runtime attachment recovery refusal is invalid")
		}
		refusals = append(refusals, refusal)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list runtime attachment recovery refusals: %w", err)
	}
	return refusals, nil
}

// RefuseRuntimeAttachmentRecovery atomically closes one exact preparation recovery.
func (store *Store) RefuseRuntimeAttachmentRecovery(
	ctx context.Context,
	intent application.TaskPreparationIntent,
	at time.Time,
) error {
	if intent.Validate() != nil || at.IsZero() || at.Location() != time.UTC {
		return errors.New("refuse runtime attachment recovery: authority is invalid")
	}
	transaction, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin runtime attachment recovery refusal: %w", err)
	}
	defer func() { _ = transaction.Rollback() }()
	current, err := readTaskPreparationIntent(ctx, transaction, intent.OperationID)
	if err != nil {
		return err
	}
	if current != intent {
		return errors.New("refuse runtime attachment recovery: durable authority differs")
	}
	const insert = `INSERT INTO runtime_attachment_recovery_refusals(operation_id, reason, refused_at)
		VALUES (?, ?, ?) ON CONFLICT(operation_id) DO NOTHING`
	if _, err := transaction.ExecContext(
		ctx, insert, intent.OperationID, application.RuntimeAttachmentPreparationUnproven, formatTime(at),
	); err != nil {
		return fmt.Errorf("record runtime attachment recovery refusal: %w", err)
	}
	var reason application.RuntimeAttachmentRecoveryRefusalReason
	var refusedAt string
	if err := transaction.QueryRowContext(ctx, `SELECT reason, refused_at
		FROM runtime_attachment_recovery_refusals WHERE operation_id = ?`, intent.OperationID).Scan(
		&reason, &refusedAt,
	); err != nil {
		return fmt.Errorf("read runtime attachment recovery refusal: %w", err)
	}
	storedAt, err := parseTime(refusedAt)
	if err != nil || reason != application.RuntimeAttachmentPreparationUnproven || !storedAt.Equal(at) {
		return errors.New("refuse runtime attachment recovery: durable outcome differs")
	}
	if err := transaction.Commit(); err != nil {
		return fmt.Errorf("commit runtime attachment recovery refusal: %w", err)
	}
	return nil
}
