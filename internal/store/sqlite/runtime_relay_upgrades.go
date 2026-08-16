package sqlite

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"

	"github.com/comisai/comis-dev-crew/internal/application"
)

const runtimeRelayUpgradeMigration = `
CREATE TABLE IF NOT EXISTS runtime_relay_identity_upgrades (
    task_handle TEXT PRIMARY KEY REFERENCES task_preparations(task_handle) ON DELETE CASCADE,
    relay_identity TEXT NOT NULL,
    relay_seed BLOB NOT NULL
);
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
