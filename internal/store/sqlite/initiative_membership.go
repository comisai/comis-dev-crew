package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/comisai/comis-dev-crew/internal/application"
	"github.com/comisai/comis-dev-crew/internal/domain"
)

const initiativeMembershipMigration = `
CREATE TABLE initiative_members (
    task_handle TEXT NOT NULL,
	initiative_handle TEXT NOT NULL,
	PRIMARY KEY(task_handle, initiative_handle),
	FOREIGN KEY(initiative_handle) REFERENCES initiatives(handle) ON DELETE CASCADE
);
CREATE INDEX initiative_members_initiative_idx
ON initiative_members(initiative_handle, task_handle);
`

func (store *Store) applyInitiativeMembershipMigration(ctx context.Context) error {
	var applied int
	if err := store.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM schema_migrations WHERE version = 47").Scan(&applied); err != nil {
		return fmt.Errorf("inspect SQLite migration 47: %w", err)
	}
	if applied == 1 {
		return nil
	}
	transaction, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin SQLite migration 47: %w", err)
	}
	defer func() { _ = transaction.Rollback() }()
	if _, err := transaction.ExecContext(ctx, initiativeMembershipMigration); err != nil {
		return fmt.Errorf("apply SQLite migration 47: %w", err)
	}
	if err := backfillInitiativeMembership(ctx, transaction); err != nil {
		return fmt.Errorf("backfill migration 47 membership: %w", err)
	}
	if _, err := transaction.ExecContext(ctx, `INSERT INTO schema_migrations(version, applied_at)
		VALUES (47, strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))`); err != nil {
		return fmt.Errorf("record SQLite migration 47: %w", err)
	}
	if err := transaction.Commit(); err != nil {
		return fmt.Errorf("commit SQLite migration 47: %w", err)
	}
	return nil
}

const initiativeMembershipMigrationPageSize = 64

func backfillInitiativeMembership(ctx context.Context, transaction *sql.Tx) error {
	const query = `SELECT handle, schema_version, managed_run_group_id, title_ref, state,
		base_revision_set_json, components_json, edges_json, contract_artifacts_json,
		integration_policy_id, integration_owner_task, state_version, created_at, updated_at
		FROM initiatives WHERE handle > ? ORDER BY handle LIMIT ?`
	afterHandle := ""
	for {
		rows, err := transaction.QueryContext(ctx, query, afterHandle, initiativeMembershipMigrationPageSize)
		if err != nil {
			return fmt.Errorf("read initiative page: %w", err)
		}
		page := make([]domain.DevelopmentInitiative, 0, initiativeMembershipMigrationPageSize)
		for rows.Next() {
			initiative, scanErr := scanInitiative(rows)
			if scanErr != nil {
				_ = rows.Close()
				return fmt.Errorf("validate initiative page: %w", scanErr)
			}
			page = append(page, initiative)
		}
		if err := errors.Join(rows.Err(), rows.Close()); err != nil {
			return fmt.Errorf("read initiative page: %w", err)
		}
		for _, initiative := range page {
			if err := insertInitiativeMembership(ctx, transaction, initiative); err != nil {
				return err
			}
		}
		if len(page) < initiativeMembershipMigrationPageSize {
			return nil
		}
		afterHandle = page[len(page)-1].Handle
	}
}

func insertInitiativeMembership(ctx context.Context, target execer, initiative domain.DevelopmentInitiative) error {
	for _, taskHandle := range initiativeTaskHandles(initiative) {
		if _, err := target.ExecContext(ctx,
			`INSERT INTO initiative_members(task_handle, initiative_handle) VALUES (?, ?)`,
			taskHandle, initiative.Handle,
		); isConstraintError(err) {
			return application.ErrConflict
		} else if err != nil {
			return fmt.Errorf("insert initiative membership: %w", err)
		}
	}
	return nil
}

func initiativeForTask(
	ctx context.Context,
	source queryer,
	taskHandle string,
) (domain.DevelopmentInitiative, bool, error) {
	const query = `SELECT i.handle, i.schema_version, i.managed_run_group_id, i.title_ref, i.state,
		i.base_revision_set_json, i.components_json, i.edges_json, i.contract_artifacts_json,
		i.integration_policy_id, i.integration_owner_task, i.state_version, i.created_at, i.updated_at
		FROM initiative_members AS member
		JOIN initiatives AS i ON i.handle = member.initiative_handle
		WHERE member.task_handle = ? ORDER BY i.handle LIMIT 2`
	rows, err := source.QueryContext(ctx, query, taskHandle)
	if err != nil {
		return domain.DevelopmentInitiative{}, false, fmt.Errorf("read initiative membership: %w", err)
	}
	if !rows.Next() {
		err := errors.Join(rows.Err(), rows.Close())
		if err != nil {
			return domain.DevelopmentInitiative{}, false, fmt.Errorf("read initiative membership: %w", err)
		}
		return domain.DevelopmentInitiative{}, false, nil
	}
	initiative, err := scanInitiative(rows)
	if err != nil {
		_ = rows.Close()
		return domain.DevelopmentInitiative{}, false, fmt.Errorf("read initiative membership: %w", err)
	}
	if rows.Next() {
		_ = rows.Close()
		return domain.DevelopmentInitiative{}, false, errors.New("read initiative membership: task belongs to multiple initiatives")
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return domain.DevelopmentInitiative{}, false, fmt.Errorf("read initiative membership: %w", err)
	}
	return initiative, true, nil
}
