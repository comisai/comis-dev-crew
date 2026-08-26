package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/comisai/comis-dev-crew/internal/domain"
)

const initiativeLaunchResourceHeadsMigration = `
CREATE INDEX initiative_launch_facts_resource_priority_idx
ON initiative_launch_facts(scheduling_round, repository_id, worker_profile_id,
    initiative_created_at, initiative_handle, task_handle);
CREATE TABLE initiative_launch_resource_heads (
    scheduling_round INTEGER NOT NULL,
    repository_id TEXT NOT NULL,
    worker_profile_id TEXT NOT NULL,
    task_handle TEXT NOT NULL,
    initiative_handle TEXT NOT NULL,
    initiative_created_at TEXT NOT NULL,
    PRIMARY KEY(scheduling_round, repository_id, worker_profile_id),
    FOREIGN KEY(task_handle) REFERENCES tasks(handle) ON DELETE CASCADE,
    FOREIGN KEY(initiative_handle) REFERENCES initiatives(handle) ON DELETE CASCADE
);
CREATE INDEX initiative_launch_resource_heads_priority_idx
ON initiative_launch_resource_heads(scheduling_round, initiative_created_at, initiative_handle, task_handle);
`

type initiativeLaunchResourceKey struct {
	round           int
	repositoryID    string
	workerProfileID string
}

func (store *Store) applyInitiativeLaunchResourceHeadsMigration(ctx context.Context) error {
	var applied int
	if err := store.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM schema_migrations WHERE version = 50").Scan(&applied); err != nil {
		return fmt.Errorf("inspect SQLite migration 50: %w", err)
	}
	if applied == 1 {
		return nil
	}
	transaction, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin SQLite migration 50: %w", err)
	}
	defer func() { _ = transaction.Rollback() }()
	if _, err := transaction.ExecContext(ctx, initiativeLaunchResourceHeadsMigration); err != nil {
		return fmt.Errorf("apply SQLite migration 50: %w", err)
	}
	if err := backfillInitiativeLaunchResourceHeads(ctx, transaction); err != nil {
		return fmt.Errorf("backfill migration 50 resource heads: %w", err)
	}
	if _, err := transaction.ExecContext(ctx, `INSERT INTO schema_migrations(version, applied_at)
		VALUES (50, strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))`); err != nil {
		return fmt.Errorf("record SQLite migration 50: %w", err)
	}
	if err := transaction.Commit(); err != nil {
		return fmt.Errorf("commit SQLite migration 50: %w", err)
	}
	return nil
}

func backfillInitiativeLaunchResourceHeads(ctx context.Context, transaction *sql.Tx) error {
	after := initiativeLaunchResourceKey{round: -1}
	for {
		rows, err := transaction.QueryContext(ctx, `SELECT DISTINCT
			scheduling_round, repository_id, worker_profile_id
			FROM initiative_launch_facts
			WHERE (scheduling_round, repository_id, worker_profile_id) > (?, ?, ?)
			ORDER BY scheduling_round, repository_id, worker_profile_id LIMIT ?`,
			after.round, after.repositoryID, after.workerProfileID, initiativeSchedulingPageSize)
		if err != nil {
			return err
		}
		page := make([]initiativeLaunchResourceKey, 0, initiativeSchedulingPageSize)
		for rows.Next() {
			var key initiativeLaunchResourceKey
			if err := rows.Scan(&key.round, &key.repositoryID, &key.workerProfileID); err != nil {
				_ = rows.Close()
				return err
			}
			if !validInitiativeLaunchResourceKey(key) {
				_ = rows.Close()
				return errors.New("stored initiative launch resource key is invalid")
			}
			page = append(page, key)
		}
		if err := errors.Join(rows.Err(), rows.Close()); err != nil {
			return err
		}
		for _, key := range page {
			if err := refreshInitiativeLaunchResourceHead(ctx, transaction, key); err != nil {
				return err
			}
		}
		if len(page) < initiativeSchedulingPageSize {
			return nil
		}
		after = page[len(page)-1]
	}
}

func initiativeLaunchResourceKeys(
	ctx context.Context,
	source queryer,
	initiativeHandle string,
) ([]initiativeLaunchResourceKey, error) {
	rows, err := source.QueryContext(ctx, `SELECT DISTINCT
		scheduling_round, repository_id, worker_profile_id
		FROM initiative_launch_facts WHERE initiative_handle = ?
		ORDER BY scheduling_round, repository_id, worker_profile_id`, initiativeHandle)
	if err != nil {
		return nil, err
	}
	keys := make([]initiativeLaunchResourceKey, 0, domain.MaximumInitiativeMembers)
	for rows.Next() {
		var key initiativeLaunchResourceKey
		if err := rows.Scan(&key.round, &key.repositoryID, &key.workerProfileID); err != nil {
			_ = rows.Close()
			return nil, err
		}
		if !validInitiativeLaunchResourceKey(key) {
			_ = rows.Close()
			return nil, errors.New("stored initiative launch resource key is invalid")
		}
		keys = append(keys, key)
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return nil, err
	}
	return keys, nil
}

func refreshInitiativeLaunchResourceKeys(
	ctx context.Context,
	target queryExecer,
	keys map[initiativeLaunchResourceKey]struct{},
) error {
	for key := range keys {
		if err := refreshInitiativeLaunchResourceHead(ctx, target, key); err != nil {
			return err
		}
	}
	return nil
}

func refreshInitiativeLaunchResourceHead(
	ctx context.Context,
	target queryExecer,
	key initiativeLaunchResourceKey,
) error {
	if !validInitiativeLaunchResourceKey(key) {
		return errors.New("initiative launch resource key is invalid")
	}
	if _, err := target.ExecContext(ctx, `DELETE FROM initiative_launch_resource_heads
		WHERE scheduling_round = ? AND repository_id = ? AND worker_profile_id = ?`,
		key.round, key.repositoryID, key.workerProfileID); err != nil {
		return err
	}
	_, err := target.ExecContext(ctx, `INSERT INTO initiative_launch_resource_heads (
		scheduling_round, repository_id, worker_profile_id,
		task_handle, initiative_handle, initiative_created_at
	) SELECT scheduling_round, repository_id, worker_profile_id,
		task_handle, initiative_handle, initiative_created_at
		FROM initiative_launch_facts
		WHERE scheduling_round = ? AND repository_id = ? AND worker_profile_id = ?
		ORDER BY initiative_created_at, initiative_handle, task_handle LIMIT 1`,
		key.round, key.repositoryID, key.workerProfileID)
	return err
}

func validInitiativeLaunchResourceKey(key initiativeLaunchResourceKey) bool {
	return key.round >= 0 && key.round < domain.MaximumInitiativeMembers &&
		domain.ValidateRepositoryID(key.repositoryID) == nil &&
		domain.ValidateAuthorityReference("workerProfileId", key.workerProfileID) == nil
}
