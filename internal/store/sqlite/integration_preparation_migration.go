package sqlite

import (
	"context"
	"errors"
	"fmt"
)

func (store *Store) applyIntegrationPreparationMigration(ctx context.Context) error {
	var applied int
	if err := store.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM schema_migrations WHERE version = 46").Scan(&applied); err != nil {
		return fmt.Errorf("inspect SQLite migration 46: %w", err)
	}
	if applied == 1 {
		return nil
	}
	transaction, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin SQLite migration 46: %w", err)
	}
	defer func() { _ = transaction.Rollback() }()
	if _, err := transaction.ExecContext(ctx, `ALTER TABLE integration_applications
		ADD COLUMN target_preparation_operation_id TEXT NOT NULL DEFAULT ''`); err != nil {
		return fmt.Errorf("apply SQLite migration 46: %w", err)
	}
	rows, err := transaction.QueryContext(ctx, `SELECT operation_id, integration_task_handle
		FROM integration_applications ORDER BY operation_id`)
	if err != nil {
		return fmt.Errorf("read migration 46 integrations: %w", err)
	}
	type integrationTarget struct {
		operationID string
		taskHandle  string
	}
	var targets []integrationTarget
	for rows.Next() {
		var target integrationTarget
		if err := rows.Scan(&target.operationID, &target.taskHandle); err != nil {
			_ = rows.Close()
			return fmt.Errorf("scan migration 46 integration: %w", err)
		}
		targets = append(targets, target)
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return fmt.Errorf("read migration 46 integrations: %w", err)
	}
	for _, target := range targets {
		preparationOperationID, err := taskPreparationOperationID(ctx, transaction, target.taskHandle)
		if err != nil {
			return fmt.Errorf("backfill migration 46 integration: %w", err)
		}
		result, err := transaction.ExecContext(ctx, `UPDATE integration_applications
			SET target_preparation_operation_id = ?
			WHERE operation_id = ? AND target_preparation_operation_id = ''`,
			preparationOperationID, target.operationID)
		if err != nil {
			return fmt.Errorf("backfill migration 46 integration: %w", err)
		}
		changed, err := result.RowsAffected()
		if err != nil || changed != 1 {
			return errors.New("backfill migration 46 integration: durable row differs")
		}
	}
	if _, err := transaction.ExecContext(ctx, `INSERT INTO schema_migrations(version, applied_at)
		VALUES (46, strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))`); err != nil {
		return fmt.Errorf("record SQLite migration 46: %w", err)
	}
	if err := transaction.Commit(); err != nil {
		return fmt.Errorf("commit SQLite migration 46: %w", err)
	}
	return nil
}
