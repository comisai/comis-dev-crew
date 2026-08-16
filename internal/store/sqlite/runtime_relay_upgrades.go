package sqlite

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/comisai/comis-dev-crew/internal/application"
	"github.com/comisai/comis-dev-crew/internal/domain"
)

const runtimeRelayUpgradeMigration = `
CREATE TABLE IF NOT EXISTS runtime_relay_identity_upgrades (
    task_handle TEXT PRIMARY KEY REFERENCES task_preparations(task_handle) ON DELETE CASCADE,
    relay_identity TEXT NOT NULL,
    relay_seed BLOB NOT NULL
);
`

const runtimeRelayRefusalMigration = `
CREATE TABLE runtime_relay_identity_refusals (
    task_handle TEXT PRIMARY KEY REFERENCES tasks(handle) ON DELETE CASCADE,
    reason TEXT NOT NULL CHECK(reason = 'unproven_filesystem_authority')
);
INSERT OR IGNORE INTO schema_migrations(version, applied_at)
VALUES (21, strftime('%Y-%m-%dT%H:%M:%fZ', 'now'));
`

func (store *Store) applyRuntimeRelayUpgradeMigration(ctx context.Context) error {
	var applied int
	if err := store.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM schema_migrations WHERE version = 20").Scan(&applied); err != nil {
		return fmt.Errorf("inspect SQLite migration 20: %w", err)
	}
	if applied == 1 {
		return nil
	}
	transaction, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin SQLite migration 20: %w", err)
	}
	defer func() { _ = transaction.Rollback() }()
	if _, err := transaction.ExecContext(ctx, runtimeRelayUpgradeMigration); err != nil {
		return fmt.Errorf("apply SQLite migration 20: %w", err)
	}
	rows, err := transaction.QueryContext(ctx, `SELECT task_handle FROM task_preparations
		WHERE requested_attachment_relay_identity = '' ORDER BY task_handle`)
	if err != nil {
		return fmt.Errorf("read migration 20 preparations: %w", err)
	}
	var taskHandles []string
	for rows.Next() {
		var taskHandle string
		if err := rows.Scan(&taskHandle); err != nil {
			_ = rows.Close()
			return fmt.Errorf("scan migration 20 preparation: %w", err)
		}
		taskHandles = append(taskHandles, taskHandle)
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return fmt.Errorf("read migration 20 preparations: %w", err)
	}
	for _, taskHandle := range taskHandles {
		var seed [32]byte
		if _, err := rand.Read(seed[:]); err != nil {
			return errors.New("backfill migration 20 relay seed is unavailable")
		}
		privateKey := ed25519.NewKeyFromSeed(seed[:])
		identity := hex.EncodeToString(privateKey.Public().(ed25519.PublicKey))
		if _, err := transaction.ExecContext(ctx, `UPDATE task_preparations
			SET requested_attachment_relay_identity = ?
			WHERE task_handle = ? AND requested_attachment_relay_identity = ''`, identity, taskHandle); err != nil {
			return fmt.Errorf("backfill migration 20 preparation: %w", err)
		}
		if _, err := transaction.ExecContext(ctx, `INSERT INTO runtime_relay_identity_upgrades(
			task_handle, relay_identity, relay_seed) VALUES (?, ?, ?)`, taskHandle, identity, seed[:]); err != nil {
			return fmt.Errorf("record migration 20 relay upgrade: %w", err)
		}
	}
	if _, err := transaction.ExecContext(ctx, `INSERT INTO schema_migrations(version, applied_at)
		VALUES (20, strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))`); err != nil {
		return fmt.Errorf("record SQLite migration 20: %w", err)
	}
	if err := transaction.Commit(); err != nil {
		return fmt.Errorf("commit SQLite migration 20: %w", err)
	}
	return nil
}

// ListRuntimeRelayIdentityUpgrades returns strict incomplete upgrade authority.
func (store *Store) ListRuntimeRelayIdentityUpgrades(ctx context.Context) ([]application.RuntimeRelayIdentityUpgrade, error) {
	rows, err := store.db.QueryContext(ctx, `SELECT task_handle, relay_identity, relay_seed
		FROM runtime_relay_identity_upgrades ORDER BY task_handle`)
	if err != nil {
		return nil, fmt.Errorf("list runtime relay identity upgrades: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var upgrades []application.RuntimeRelayIdentityUpgrade
	for rows.Next() {
		var upgrade application.RuntimeRelayIdentityUpgrade
		var seed []byte
		if err := rows.Scan(&upgrade.TaskHandle, &upgrade.RelayIdentity, &seed); err != nil || len(seed) != len(upgrade.RelaySeed) {
			return nil, errors.New("stored runtime relay identity upgrade is invalid")
		}
		copy(upgrade.RelaySeed[:], seed)
		if upgrade.Validate() != nil {
			return nil, errors.New("stored runtime relay identity upgrade is invalid")
		}
		upgrades = append(upgrades, upgrade)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list runtime relay identity upgrades: %w", err)
	}
	return upgrades, nil
}

// CompleteRuntimeRelayIdentityUpgrade closes one exact durable upgrade transition.
func (store *Store) CompleteRuntimeRelayIdentityUpgrade(ctx context.Context, upgrade application.RuntimeRelayIdentityUpgrade) error {
	if upgrade.Validate() != nil {
		return errors.New("complete runtime relay identity upgrade: authority is invalid")
	}
	result, err := store.db.ExecContext(ctx, `DELETE FROM runtime_relay_identity_upgrades
		WHERE task_handle = ? AND relay_identity = ? AND relay_seed = ?`,
		upgrade.TaskHandle, upgrade.RelayIdentity, upgrade.RelaySeed[:])
	if err != nil {
		return fmt.Errorf("complete runtime relay identity upgrade: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil || affected != 1 {
		return errors.New("complete runtime relay identity upgrade: durable authority differs")
	}
	return nil
}

// ListRuntimeRelayIdentityRefusals returns the closed task-scoped upgrade refusals.
func (store *Store) ListRuntimeRelayIdentityRefusals(ctx context.Context) ([]application.RuntimeRelayIdentityRefusal, error) {
	rows, err := store.db.QueryContext(ctx, `SELECT task_handle, reason
		FROM runtime_relay_identity_refusals ORDER BY task_handle`)
	if err != nil {
		return nil, fmt.Errorf("list runtime relay identity refusals: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var refusals []application.RuntimeRelayIdentityRefusal
	for rows.Next() {
		var refusal application.RuntimeRelayIdentityRefusal
		if err := rows.Scan(&refusal.TaskHandle, &refusal.Reason); err != nil || refusal.Validate() != nil {
			return nil, errors.New("stored runtime relay identity refusal is invalid")
		}
		refusals = append(refusals, refusal)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list runtime relay identity refusals: %w", err)
	}
	return refusals, nil
}

// RefuseRuntimeRelayIdentityUpgrade atomically closes one task whose prior relay cannot be proven.
func (store *Store) RefuseRuntimeRelayIdentityUpgrade(
	ctx context.Context,
	upgrade application.RuntimeRelayIdentityUpgrade,
	at time.Time,
) error {
	if upgrade.Validate() != nil || at.IsZero() || at.Location() != time.UTC {
		return errors.New("refuse runtime relay identity upgrade: authority is invalid")
	}
	transaction, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin runtime relay identity refusal: %w", err)
	}
	defer func() { _ = transaction.Rollback() }()
	if err := refuseRuntimeAttachmentTask(ctx, transaction, upgrade.TaskHandle, at); err != nil {
		return fmt.Errorf("refuse runtime relay identity upgrade: %w", err)
	}
	result, err := transaction.ExecContext(ctx, `DELETE FROM runtime_relay_identity_upgrades
		WHERE task_handle = ? AND relay_identity = ? AND relay_seed = ?`,
		upgrade.TaskHandle, upgrade.RelayIdentity, upgrade.RelaySeed[:])
	if err != nil {
		return fmt.Errorf("close refused runtime relay identity upgrade: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil || affected != 1 {
		return errors.New("refuse runtime relay identity upgrade: durable authority differs")
	}
	if err := transaction.Commit(); err != nil {
		return fmt.Errorf("commit runtime relay identity refusal: %w", err)
	}
	return nil
}

// RefuseRuntimeAttachmentTaskRecovery durably closes only one task whose runtime directory ownership is unproven.
func (store *Store) RefuseRuntimeAttachmentTaskRecovery(
	ctx context.Context,
	taskHandle string,
	at time.Time,
) error {
	if domain.ValidateTaskHandle(taskHandle) != nil || at.IsZero() || at.Location() != time.UTC {
		return errors.New("refuse runtime attachment task recovery: authority is invalid")
	}
	transaction, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin runtime attachment task refusal: %w", err)
	}
	defer func() { _ = transaction.Rollback() }()
	if err := refuseRuntimeAttachmentTask(ctx, transaction, taskHandle, at); err != nil {
		return err
	}
	if err := transaction.Commit(); err != nil {
		return fmt.Errorf("commit runtime attachment task refusal: %w", err)
	}
	return nil
}

func refuseRuntimeAttachmentTask(ctx context.Context, transaction *sql.Tx, taskHandle string, at time.Time) error {
	refused, err := runtimeRelayIdentityRefusalExists(ctx, transaction, taskHandle)
	if err != nil || refused {
		return err
	}
	task, err := getTask(ctx, transaction, taskHandle)
	if err != nil {
		return err
	}
	if task.State != domain.TaskCleaned && task.State != domain.TaskUnknown {
		unknown, err := reconcileTaskUnknown(task, at)
		if err != nil {
			return fmt.Errorf("close runtime attachment task recovery: %w", err)
		}
		version, err := nextReconciliationVersion(ctx, transaction)
		if err != nil {
			return err
		}
		unknown.StateVersion = version
		if err := updateTaskState(ctx, transaction, unknown); err != nil {
			return err
		}
	}
	refusal := application.RuntimeRelayIdentityRefusal{
		TaskHandle: taskHandle,
		Reason:     application.RuntimeRelayIdentityUnproven,
	}
	if refusal.Validate() != nil {
		return errors.New("runtime attachment task refusal is invalid")
	}
	if _, err := transaction.ExecContext(ctx, `INSERT INTO runtime_relay_identity_refusals(task_handle, reason)
		VALUES (?, ?)`, refusal.TaskHandle, refusal.Reason); err != nil {
		return fmt.Errorf("record runtime attachment task refusal: %w", err)
	}
	return nil
}

func runtimeRelayIdentityRefusalExists(ctx context.Context, source queryer, taskHandle string) (bool, error) {
	var reason application.RuntimeRelayIdentityRefusalReason
	err := source.QueryRowContext(ctx, `SELECT reason FROM runtime_relay_identity_refusals
		WHERE task_handle = ?`, taskHandle).Scan(&reason)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("read runtime relay identity refusal: %w", err)
	}
	refusal := application.RuntimeRelayIdentityRefusal{TaskHandle: taskHandle, Reason: reason}
	if refusal.Validate() != nil {
		return false, errors.New("stored runtime relay identity refusal is invalid")
	}
	return true, nil
}
