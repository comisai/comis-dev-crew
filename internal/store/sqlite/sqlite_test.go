package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestOpen_ConfiguresPureGoSQLiteForPrivateWALServiceStore(t *testing.T) {
	databasePath := filepath.Join(canonicalTempDir(t), "private", "devcrew.db")
	store, err := Open(context.Background(), databasePath)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Errorf("Close() error = %v", err)
		}
	})

	assertPragmaText(t, store, "journal_mode", "wal")
	assertPragmaInt(t, store, "busy_timeout", busyTimeoutMilliseconds)
	assertPragmaInt(t, store, "foreign_keys", 1)
	var migrationCount int
	if err := store.db.QueryRow("SELECT COUNT(*) FROM schema_migrations WHERE version = 1").Scan(&migrationCount); err != nil {
		t.Fatalf("read migration version: %v", err)
	}
	if migrationCount != 1 {
		t.Fatalf("migration count = %d, want 1", migrationCount)
	}

	assertMode(t, filepath.Dir(databasePath), 0o700)
	assertMode(t, databasePath, 0o600)
}

func TestOpen_RejectsRelativeSymlinkedAndNonRegularDatabasePaths(t *testing.T) {
	t.Run("relative path", func(t *testing.T) {
		if _, err := Open(context.Background(), "relative/devcrew.db"); err == nil {
			t.Fatal("Open() error = nil, want relative-path rejection")
		}
	})

	t.Run("non-canonical path", func(t *testing.T) {
		root := canonicalTempDir(t)
		nonCanonical := root + string(os.PathSeparator) + "nested" + string(os.PathSeparator) + ".." + string(os.PathSeparator) + "devcrew.db"
		if _, err := Open(context.Background(), nonCanonical); err == nil {
			t.Fatal("Open() error = nil, want non-canonical-path rejection")
		}
	})

	t.Run("nil context", func(t *testing.T) {
		//lint:ignore SA1012 The boundary test proves nil is rejected before any database work.
		if _, err := Open(nil, filepath.Join(canonicalTempDir(t), "devcrew.db")); err == nil {
			t.Fatal("Open() error = nil, want nil-context rejection")
		}
	})

	t.Run("filesystem root parent", func(t *testing.T) {
		if _, err := Open(context.Background(), filepath.Join(string(os.PathSeparator), "devcrew.db")); err == nil {
			t.Fatal("Open() error = nil, want broad-root rejection")
		}
	})

	t.Run("symlinked parent", func(t *testing.T) {
		root := canonicalTempDir(t)
		realParent := filepath.Join(root, "real")
		if err := os.Mkdir(realParent, 0o700); err != nil {
			t.Fatalf("create real parent: %v", err)
		}
		linkedParent := filepath.Join(root, "linked")
		if err := os.Symlink(realParent, linkedParent); err != nil {
			t.Fatalf("create parent symlink: %v", err)
		}
		if _, err := Open(context.Background(), filepath.Join(linkedParent, "devcrew.db")); err == nil {
			t.Fatal("Open() error = nil, want symlinked-parent rejection")
		}
	})

	t.Run("symlinked database target", func(t *testing.T) {
		root := canonicalTempDir(t)
		realDatabase := filepath.Join(root, "real.db")
		if err := os.WriteFile(realDatabase, nil, 0o600); err != nil {
			t.Fatalf("create real database target: %v", err)
		}
		linkedDatabase := filepath.Join(root, "linked.db")
		if err := os.Symlink(realDatabase, linkedDatabase); err != nil {
			t.Fatalf("create database symlink: %v", err)
		}
		if _, err := Open(context.Background(), linkedDatabase); err == nil {
			t.Fatal("Open() error = nil, want symlinked-database rejection")
		}
	})

	t.Run("non-regular target", func(t *testing.T) {
		databasePath := filepath.Join(canonicalTempDir(t), "devcrew.db")
		if err := os.Mkdir(databasePath, 0o700); err != nil {
			t.Fatalf("create directory at database path: %v", err)
		}
		if _, err := Open(context.Background(), databasePath); err == nil {
			t.Fatal("Open() error = nil, want non-regular-file rejection")
		}
	})

	t.Run("non-directory parent component", func(t *testing.T) {
		root := canonicalTempDir(t)
		component := filepath.Join(root, "not-a-directory")
		if err := os.WriteFile(component, nil, 0o600); err != nil {
			t.Fatalf("create file parent component: %v", err)
		}
		if _, err := Open(context.Background(), filepath.Join(component, "devcrew.db")); err == nil {
			t.Fatal("Open() error = nil, want non-directory-component rejection")
		}
	})

	t.Run("cancelled context", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if _, err := Open(ctx, filepath.Join(canonicalTempDir(t), "devcrew.db")); !errors.Is(err, context.Canceled) {
			t.Fatalf("Open() error = %v, want context cancellation", err)
		}
	})
}

func TestStore_CloseAndMigrationFailuresRemainExplicit(t *testing.T) {
	var nilStore *Store
	if err := nilStore.Close(); err != nil {
		t.Fatalf("nil Store.Close() error = %v, want nil", err)
	}
	if err := (&Store{}).Close(); err != nil {
		t.Fatalf("empty Store.Close() error = %v, want nil", err)
	}

	store, err := Open(context.Background(), filepath.Join(canonicalTempDir(t), "devcrew.db"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if err := store.migrate(context.Background()); err == nil {
		t.Fatal("migrate() error = nil after close, want explicit failure")
	}
}

func TestStore_InvalidMigrationRollsBackPartialSchema(t *testing.T) {
	store, err := Open(context.Background(), filepath.Join(canonicalTempDir(t), "devcrew.db"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	invalidMigration := "CREATE TABLE partial_migration (id INTEGER); INVALID SQL"
	if err := store.applyMigration(context.Background(), invalidMigration); err == nil {
		t.Fatal("applyMigration() error = nil, want invalid-SQL failure")
	}
	var count int
	query := "SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'partial_migration'"
	if err := store.db.QueryRow(query).Scan(&count); err != nil {
		t.Fatalf("inspect rolled-back migration: %v", err)
	}
	if count != 0 {
		t.Fatalf("partial migration table count = %d, want 0", count)
	}
}

func TestOpen_DependencyFailuresClosePartiallyOpenedDatabase(t *testing.T) {
	t.Run("driver open", func(t *testing.T) {
		dependencies := realOpenDependencies()
		dependencies.openDatabase = func(string, string) (*sql.DB, error) {
			return nil, errors.New("driver open failed")
		}
		if _, err := openStore(context.Background(), filepath.Join(canonicalTempDir(t), "devcrew.db"), dependencies); err == nil {
			t.Fatal("openStore() error = nil, want driver-open failure")
		}
	})

	for _, test := range []struct {
		name   string
		mutate func(*openDependencies)
	}{
		{name: "chmod", mutate: func(dependencies *openDependencies) {
			dependencies.chmod = func(string, os.FileMode) error { return errors.New("chmod failed") }
		}},
		{name: "migration", mutate: func(dependencies *openDependencies) {
			dependencies.migrate = func(context.Context, *Store) error { return errors.New("migration failed") }
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			dependencies := realOpenDependencies()
			var openedDatabase *sql.DB
			dependencies.openDatabase = func(driverName, dataSourceName string) (*sql.DB, error) {
				database, err := sql.Open(driverName, dataSourceName)
				openedDatabase = database
				return database, err
			}
			test.mutate(&dependencies)
			if _, err := openStore(context.Background(), filepath.Join(canonicalTempDir(t), "devcrew.db"), dependencies); err == nil {
				t.Fatalf("openStore() error = nil, want %s failure", test.name)
			}
			if openedDatabase == nil {
				t.Fatal("test did not capture opened database")
			}
			if err := openedDatabase.Ping(); err == nil || !strings.Contains(err.Error(), "closed") {
				t.Fatalf("partially opened database Ping() error = %v, want closed database", err)
			}
		})
	}
}

func realOpenDependencies() openDependencies {
	return openDependencies{
		openDatabase: sql.Open,
		chmod:        os.Chmod,
		migrate: func(ctx context.Context, store *Store) error {
			return store.migrate(ctx)
		},
	}
}

func TestFilesystemHelpers_ReportPermissionAndInspectionFailures(t *testing.T) {
	root := canonicalTempDir(t)
	locked := filepath.Join(root, "locked")
	if err := os.Mkdir(locked, 0o700); err != nil {
		t.Fatalf("create locked directory: %v", err)
	}
	if err := os.Chmod(locked, 0); err != nil {
		t.Fatalf("lock directory: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(locked, 0o700) })

	if err := ensurePrivateDirectory(filepath.Join(locked, "nested")); err == nil {
		t.Fatal("ensurePrivateDirectory() error = nil, want inaccessible-component failure")
	}
	if err := validateDatabaseTarget(filepath.Join(locked, "devcrew.db")); err == nil {
		t.Fatal("validateDatabaseTarget() error = nil, want inaccessible-target failure")
	}

	doubleSeparatorPath := root + string(os.PathSeparator) + string(os.PathSeparator) + "empty-component"
	if err := ensurePrivateDirectory(doubleSeparatorPath); err != nil {
		t.Fatalf("ensurePrivateDirectory() with empty path component error = %v", err)
	}
}

func TestOpen_LockContentionCancelsAndRollbackPreservesWriterProgress(t *testing.T) {
	databasePath := filepath.Join(canonicalTempDir(t), "devcrew.db")
	first, err := Open(context.Background(), databasePath)
	if err != nil {
		t.Fatalf("Open(first) error = %v", err)
	}
	t.Cleanup(func() { _ = first.Close() })
	second, err := Open(context.Background(), databasePath)
	if err != nil {
		t.Fatalf("Open(second) error = %v", err)
	}
	t.Cleanup(func() { _ = second.Close() })

	if _, err := first.db.Exec("CREATE TABLE lock_probe (id INTEGER PRIMARY KEY, value TEXT NOT NULL)"); err != nil {
		t.Fatalf("create lock probe: %v", err)
	}
	transaction, err := first.db.Begin()
	if err != nil {
		t.Fatalf("begin first transaction: %v", err)
	}
	if _, err := transaction.Exec("INSERT INTO lock_probe(id, value) VALUES (1, 'held')"); err != nil {
		t.Fatalf("acquire write lock: %v", err)
	}

	blockedContext, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	_, blockedErr := second.db.ExecContext(blockedContext, "INSERT INTO lock_probe(id, value) VALUES (2, 'blocked')")
	if blockedErr == nil {
		t.Fatal("contending write error = nil, want cancellation while first writer holds lock")
	}
	if !errors.Is(blockedContext.Err(), context.DeadlineExceeded) {
		t.Fatalf("blocked context error = %v, want deadline exceeded", blockedContext.Err())
	}
	if err := transaction.Rollback(); err != nil {
		t.Fatalf("rollback first transaction: %v", err)
	}
	if _, err := second.db.Exec("INSERT INTO lock_probe(id, value) VALUES (2, 'after-rollback')"); err != nil {
		t.Fatalf("write after rollback: %v", err)
	}
	var rows int
	if err := second.db.QueryRow("SELECT COUNT(*) FROM lock_probe").Scan(&rows); err != nil {
		t.Fatalf("count lock-probe rows: %v", err)
	}
	if rows != 1 {
		t.Fatalf("row count = %d, want only the post-rollback row", rows)
	}
}

func assertPragmaText(t *testing.T, store *Store, name, want string) {
	t.Helper()
	var got string
	if err := store.db.QueryRow("PRAGMA " + name).Scan(&got); err != nil {
		t.Fatalf("read PRAGMA %s: %v", name, err)
	}
	if !strings.EqualFold(got, want) {
		t.Fatalf("PRAGMA %s = %q, want %q", name, got, want)
	}
}

func assertPragmaInt(t *testing.T, store *Store, name string, want int) {
	t.Helper()
	var got int
	if err := store.db.QueryRow("PRAGMA " + name).Scan(&got); err != nil {
		t.Fatalf("read PRAGMA %s: %v", name, err)
	}
	if got != want {
		t.Fatalf("PRAGMA %s = %d, want %d", name, got, want)
	}
}

func assertMode(t *testing.T, path string, want os.FileMode) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
	if got := info.Mode().Perm(); got != want {
		t.Fatalf("mode for %s = %#o, want %#o", path, got, want)
	}
}

func canonicalTempDir(t *testing.T) string {
	t.Helper()
	realPath, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("resolve temporary directory: %v", err)
	}
	return realPath
}
