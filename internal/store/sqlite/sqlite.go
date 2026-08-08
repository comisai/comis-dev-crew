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
	return store.applyMigration(ctx, initialMigration)
}

func (store *Store) applyMigration(ctx context.Context, migration string) error {
	transaction, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin SQLite migration: %w", err)
	}
	if _, err := transaction.ExecContext(ctx, migration); err != nil {
		_ = transaction.Rollback()
		return fmt.Errorf("apply SQLite migration 1: %w", err)
	}
	if err := transaction.Commit(); err != nil {
		return fmt.Errorf("commit SQLite migration 1: %w", err)
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
