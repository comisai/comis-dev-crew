package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/comisai/comis-dev-crew/internal/application"
)

const taskPreparationIntentMigration = `
CREATE TABLE task_preparation_intents (
    operation_id TEXT PRIMARY KEY,
    task_handle TEXT NOT NULL UNIQUE,
    subject_digest TEXT NOT NULL,
    created_at TEXT NOT NULL
);
CREATE INDEX task_preparation_intents_task_idx
ON task_preparation_intents(task_handle, operation_id);
INSERT OR IGNORE INTO schema_migrations(version, applied_at)
VALUES (18, strftime('%Y-%m-%dT%H:%M:%fZ', 'now'));
`

const runtimeRelayIdentityMigration = `
ALTER TABLE task_preparations ADD COLUMN requested_attachment_relay_identity TEXT NOT NULL DEFAULT '';
INSERT OR IGNORE INTO schema_migrations(version, applied_at)
VALUES (19, strftime('%Y-%m-%dT%H:%M:%fZ', 'now'));
`

func (store *Store) applyTaskPreparationMigrations(ctx context.Context) error {
	if err := store.applyVersionedMigration(ctx, 18, taskPreparationIntentMigration); err != nil {
		return err
	}
	return store.applyVersionedMigration(ctx, 19, runtimeRelayIdentityMigration)
}

// RecordTaskPreparationIntent publishes or reuses one immutable task identity.
func (store *Store) RecordTaskPreparationIntent(
	ctx context.Context,
	intent application.TaskPreparationIntent,
) (application.TaskPreparationIntent, error) {
	if err := intent.Validate(); err != nil {
		return application.TaskPreparationIntent{}, err
	}
	transaction, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return application.TaskPreparationIntent{}, fmt.Errorf("begin task preparation intent: %w", err)
	}
	defer func() { _ = transaction.Rollback() }()
	operation, found, err := mutationReplay(
		ctx, transaction, intent.OperationID, commandPrepareTask, intent.SubjectDigest,
	)
	if err != nil {
		return application.TaskPreparationIntent{}, commitReplayConflict(transaction, err)
	}
	if found {
		task, err := getTask(ctx, transaction, operation.ResultRef)
		if err != nil {
			return application.TaskPreparationIntent{}, err
		}
		replayed := application.TaskPreparationIntent{
			OperationID: operation.ID, TaskHandle: task.Handle,
			SubjectDigest: operation.SubjectDigest, CreatedAt: task.CreatedAt,
		}
		if replayed.Validate() != nil {
			return application.TaskPreparationIntent{}, errors.New("replay task preparation intent is invalid")
		}
		if err := transaction.Commit(); err != nil {
			return application.TaskPreparationIntent{}, fmt.Errorf("commit task preparation operation replay: %w", err)
		}
		return replayed, nil
	}
	const insert = `INSERT INTO task_preparation_intents(
		operation_id, task_handle, subject_digest, created_at
	) VALUES (?, ?, ?, ?)`
	if _, err := transaction.ExecContext(ctx, insert,
		intent.OperationID, intent.TaskHandle, intent.SubjectDigest, formatTime(intent.CreatedAt),
	); err == nil {
		if err := transaction.Commit(); err != nil {
			return application.TaskPreparationIntent{}, fmt.Errorf("commit task preparation intent: %w", err)
		}
		return intent, nil
	} else if !isConstraintError(err) {
		return application.TaskPreparationIntent{}, fmt.Errorf("record task preparation intent: %w", err)
	}
	existing, err := readTaskPreparationIntent(ctx, transaction, intent.OperationID)
	if err != nil {
		if errors.Is(err, application.ErrNotFound) {
			return application.TaskPreparationIntent{}, fmt.Errorf("record task preparation intent: %w", application.ErrConflict)
		}
		return application.TaskPreparationIntent{}, err
	}
	if existing.SubjectDigest != intent.SubjectDigest {
		return application.TaskPreparationIntent{}, fmt.Errorf("record task preparation altered replay: %w", application.ErrConflict)
	}
	if err := transaction.Commit(); err != nil {
		return application.TaskPreparationIntent{}, fmt.Errorf("commit task preparation replay: %w", err)
	}
	return existing, nil
}

// ListTaskPreparationIntents returns incomplete preparation authority in task order.
func (store *Store) ListTaskPreparationIntents(ctx context.Context) ([]application.TaskPreparationIntent, error) {
	const query = `SELECT operation_id, task_handle, subject_digest, created_at
		FROM task_preparation_intents ORDER BY task_handle, operation_id`
	rows, err := store.db.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("list task preparation intents: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var intents []application.TaskPreparationIntent
	for rows.Next() {
		var intent application.TaskPreparationIntent
		var createdAt string
		if err := rows.Scan(&intent.OperationID, &intent.TaskHandle, &intent.SubjectDigest, &createdAt); err != nil {
			return nil, fmt.Errorf("scan task preparation intent: %w", err)
		}
		intent.CreatedAt, err = parseTime(createdAt)
		if err != nil || intent.Validate() != nil {
			return nil, errors.New("task preparation intent is invalid")
		}
		intents = append(intents, intent)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list task preparation intents: %w", err)
	}
	return intents, nil
}

func readTaskPreparationIntent(
	ctx context.Context,
	source queryer,
	operationID string,
) (application.TaskPreparationIntent, error) {
	const query = `SELECT operation_id, task_handle, subject_digest, created_at
		FROM task_preparation_intents WHERE operation_id = ?`
	var intent application.TaskPreparationIntent
	var createdAt string
	err := source.QueryRowContext(ctx, query, operationID).Scan(
		&intent.OperationID, &intent.TaskHandle, &intent.SubjectDigest, &createdAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return application.TaskPreparationIntent{}, application.ErrNotFound
	}
	if err != nil {
		return application.TaskPreparationIntent{}, fmt.Errorf("read task preparation intent: %w", err)
	}
	intent.CreatedAt, err = parseTime(createdAt)
	if err != nil || intent.Validate() != nil {
		return application.TaskPreparationIntent{}, errors.New("task preparation intent is invalid")
	}
	return intent, nil
}

func consumeTaskPreparationIntent(
	ctx context.Context,
	transaction *sql.Tx,
	mutation application.PreparedTaskMutation,
) error {
	intent, err := readTaskPreparationIntent(ctx, transaction, mutation.OperationID)
	if errors.Is(err, application.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if intent.TaskHandle != mutation.Task.Handle || intent.SubjectDigest != mutation.SubjectDigest ||
		intent.CreatedAt != mutation.At {
		return fmt.Errorf("consume task preparation intent: %w", application.ErrConflict)
	}
	result, err := transaction.ExecContext(ctx, `DELETE FROM task_preparation_intents WHERE operation_id = ?`, intent.OperationID)
	if err != nil {
		return fmt.Errorf("consume task preparation intent: %w", err)
	}
	removed, err := result.RowsAffected()
	if err != nil || removed != 1 {
		return errors.New("consume task preparation intent: durable intent changed")
	}
	return nil
}
