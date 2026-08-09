// Package sqlite implements the service-owned durable store adapter.
package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	_ "modernc.org/sqlite"
)

const initialMigration = `
CREATE TABLE IF NOT EXISTS schema_migrations (
    version INTEGER PRIMARY KEY,
    applied_at TEXT NOT NULL
);
INSERT OR IGNORE INTO schema_migrations(version, applied_at)
VALUES (1, strftime('%Y-%m-%dT%H:%M:%fZ', 'now'));
`

const recordMigration = `
CREATE TABLE IF NOT EXISTS tasks (
    handle TEXT PRIMARY KEY,
    schema_version INTEGER NOT NULL,
    state TEXT NOT NULL,
    shape TEXT NOT NULL,
    repository_id TEXT NOT NULL,
    base_revision TEXT NOT NULL,
    brief_revision INTEGER NOT NULL,
    validation_profile TEXT NOT NULL,
    delivery_mode TEXT NOT NULL,
    worker_profile_id TEXT NOT NULL,
    report_cursor INTEGER NOT NULL,
    state_version INTEGER NOT NULL,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS tasks_state_handle_idx ON tasks(state, handle);
CREATE TABLE IF NOT EXISTS operations (
    id TEXT PRIMARY KEY,
    schema_version INTEGER NOT NULL,
    command TEXT NOT NULL,
    subject_digest TEXT NOT NULL,
    status TEXT NOT NULL,
    error_code TEXT NOT NULL,
    state_version INTEGER NOT NULL,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS operations_status_id_idx ON operations(status, id);
INSERT OR IGNORE INTO schema_migrations(version, applied_at)
VALUES (2, strftime('%Y-%m-%dT%H:%M:%fZ', 'now'));
`

const taskContractMigration = `
ALTER TABLE tasks ADD COLUMN brief_revision_hash TEXT NOT NULL DEFAULT '';
ALTER TABLE tasks ADD COLUMN acceptance_criteria_json TEXT NOT NULL DEFAULT '[]';
ALTER TABLE tasks ADD COLUMN constraints_json TEXT NOT NULL DEFAULT '[]';
INSERT OR IGNORE INTO schema_migrations(version, applied_at)
VALUES (3, strftime('%Y-%m-%dT%H:%M:%fZ', 'now'));
`

const taskBindingMigration = `
ALTER TABLE tasks ADD COLUMN service_instance_id TEXT NOT NULL DEFAULT '';
ALTER TABLE tasks ADD COLUMN managed_run_id TEXT NOT NULL DEFAULT '';
ALTER TABLE tasks ADD COLUMN workspace_lease_id TEXT NOT NULL DEFAULT '';
ALTER TABLE operations ADD COLUMN result_ref TEXT NOT NULL DEFAULT '';
INSERT OR IGNORE INTO schema_migrations(version, applied_at)
VALUES (4, strftime('%Y-%m-%dT%H:%M:%fZ', 'now'));
`

const reportMigration = `
CREATE TABLE reports (
    task_handle TEXT NOT NULL,
    local_report_id TEXT NOT NULL,
    subject_digest TEXT NOT NULL,
    schema_version INTEGER NOT NULL,
    brief_revision INTEGER NOT NULL,
    brief_revision_hash TEXT NOT NULL,
    kind TEXT NOT NULL,
    external_key TEXT NOT NULL,
    summary TEXT NOT NULL,
    details TEXT NOT NULL,
    worker_observed_at TEXT,
    state_version INTEGER NOT NULL,
    accepted_at TEXT NOT NULL,
    PRIMARY KEY(task_handle, local_report_id),
    FOREIGN KEY(task_handle) REFERENCES tasks(handle)
);
CREATE INDEX reports_task_version_idx ON reports(task_handle, state_version, local_report_id);
CREATE UNIQUE INDEX reports_decision_key_idx ON reports(task_handle, external_key)
WHERE kind = 'decision';
CREATE UNIQUE INDEX reports_resolution_key_idx ON reports(task_handle, external_key)
WHERE kind = 'resolution';
INSERT OR IGNORE INTO schema_migrations(version, applied_at)
VALUES (5, strftime('%Y-%m-%dT%H:%M:%fZ', 'now'));
`

const managedRunPreparationMigration = `
CREATE TABLE task_preparations (
    task_handle TEXT PRIMARY KEY,
    external_run_ref TEXT NOT NULL UNIQUE,
    registration_nonce TEXT NOT NULL,
    expires_at TEXT NOT NULL,
    created_at TEXT NOT NULL,
    FOREIGN KEY(task_handle) REFERENCES tasks(handle)
);
INSERT OR IGNORE INTO schema_migrations(version, applied_at)
VALUES (6, strftime('%Y-%m-%dT%H:%M:%fZ', 'now'));
`

const busyTimeoutMilliseconds = 500

// Store owns one SQLite connection pool. The service composition root is the
// only production caller allowed to open it for mutation.
type Store struct {
	db *sql.DB
}

type openDependencies struct {
	openDatabase func(string, string) (*sql.DB, error)
	chmod        func(string, os.FileMode) error
	migrate      func(context.Context, *Store) error
}

// Open validates a canonical owner-private path, configures the pure-Go SQLite
// driver, and applies migrations transactionally.
func Open(ctx context.Context, databasePath string) (*Store, error) {
	return openStore(ctx, databasePath, openDependencies{
		openDatabase: sql.Open,
		chmod:        os.Chmod,
		migrate: func(ctx context.Context, store *Store) error {
			return store.migrate(ctx)
		},
	})
}

func openStore(ctx context.Context, databasePath string, dependencies openDependencies) (*Store, error) {
	if ctx == nil {
		return nil, errors.New("open SQLite store: context is required")
	}
	cleanPath := filepath.Clean(databasePath)
	if !filepath.IsAbs(databasePath) || cleanPath != databasePath {
		return nil, errors.New("open SQLite store: database path must be absolute and canonical")
	}
	parent := filepath.Dir(cleanPath)
	if err := ensurePrivateDirectory(parent); err != nil {
		return nil, fmt.Errorf("open SQLite store directory: %w", err)
	}
	if err := validateDatabaseTarget(cleanPath); err != nil {
		return nil, err
	}

	databaseURL := url.URL{Scheme: "file", Path: cleanPath}
	query := databaseURL.Query()
	query.Add("_pragma", fmt.Sprintf("busy_timeout(%d)", busyTimeoutMilliseconds))
	query.Add("_pragma", "foreign_keys(ON)")
	query.Add("_pragma", "journal_mode(WAL)")
	query.Add("_pragma", "synchronous(NORMAL)")
	databaseURL.RawQuery = query.Encode()

	database, err := dependencies.openDatabase("sqlite", databaseURL.String())
	if err != nil {
		return nil, fmt.Errorf("open SQLite driver: %w", err)
	}
	database.SetMaxOpenConns(1)
	database.SetMaxIdleConns(1)
	store := &Store{db: database}
	if err := database.PingContext(ctx); err != nil {
		_ = database.Close()
		return nil, fmt.Errorf("ping SQLite store: %w", err)
	}
	if err := dependencies.chmod(cleanPath, 0o600); err != nil {
		_ = database.Close()
		return nil, fmt.Errorf("set SQLite database owner-only mode: %w", err)
	}
	if err := dependencies.migrate(ctx, store); err != nil {
		_ = database.Close()
		return nil, err
	}
	return store, nil
}

// Close releases the database connection and returns any close error.
func (store *Store) Close() error {
	if store == nil || store.db == nil {
		return nil
	}
	if err := store.db.Close(); err != nil {
		return fmt.Errorf("close SQLite store: %w", err)
	}
	return nil
}

func (store *Store) migrate(ctx context.Context) error {
	if err := store.applyMigration(ctx, initialMigration); err != nil {
		return err
	}
	if err := store.applyMigration(ctx, recordMigration); err != nil {
		return err
	}
	if err := store.applyVersionedMigration(ctx, 3, taskContractMigration); err != nil {
		return err
	}
	if err := store.applyVersionedMigration(ctx, 4, taskBindingMigration); err != nil {
		return err
	}
	if err := store.applyVersionedMigration(ctx, 5, reportMigration); err != nil {
		return err
	}
	return store.applyVersionedMigration(ctx, 6, managedRunPreparationMigration)
}

func (store *Store) applyVersionedMigration(ctx context.Context, version int, migration string) error {
	var applied int
	if err := store.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM schema_migrations WHERE version = ?", version).Scan(&applied); err != nil {
		return fmt.Errorf("inspect SQLite migration %d: %w", version, err)
	}
	if applied == 1 {
		return nil
	}
	return store.applyMigration(ctx, migration)
}

func (store *Store) applyMigration(ctx context.Context, migration string) error {
	transaction, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin SQLite migration: %w", err)
	}
	if _, err := transaction.ExecContext(ctx, migration); err != nil {
		_ = transaction.Rollback()
		return fmt.Errorf("apply SQLite migration: %w", err)
	}
	if err := transaction.Commit(); err != nil {
		return fmt.Errorf("commit SQLite migration: %w", err)
	}
	return nil
}

func ensurePrivateDirectory(directory string) error {
	volume := filepath.VolumeName(directory)
	root := volume + string(os.PathSeparator)
	if directory == root {
		return errors.New("database parent must not be a filesystem root")
	}
	remainder := strings.TrimPrefix(directory, root)
	current := root
	for _, component := range strings.Split(remainder, string(os.PathSeparator)) {
		if component == "" {
			continue
		}
		current = filepath.Join(current, component)
		info, err := os.Lstat(current)
		if os.IsNotExist(err) {
			if err := os.Mkdir(current, 0o700); err != nil {
				return fmt.Errorf("create directory component: %w", err)
			}
			continue
		}
		if err != nil {
			return fmt.Errorf("inspect directory component: %w", err)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return errors.New("directory path contains a symbolic link")
		}
		if !info.IsDir() {
			return errors.New("directory path contains a non-directory component")
		}
	}
	if err := os.Chmod(directory, 0o700); err != nil {
		return fmt.Errorf("set runtime directory owner-only mode: %w", err)
	}
	return nil
}

func validateDatabaseTarget(databasePath string) error {
	info, err := os.Lstat(databasePath)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect SQLite database target: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return errors.New("open SQLite store: database target must be a regular non-symlink file")
	}
	return nil
}
