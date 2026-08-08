package sqlite

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestOpen_ConfiguresPureGoSQLiteForPrivateWALServiceStore(t *testing.T) {
	databasePath := filepath.Join(t.TempDir(), "private", "devcrew.db")
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
	assertPragmaInt(t, store, "busy_timeout", 5000)
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

	t.Run("symlinked parent", func(t *testing.T) {
		root := t.TempDir()
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

	t.Run("non-regular target", func(t *testing.T) {
		databasePath := filepath.Join(t.TempDir(), "devcrew.db")
		if err := os.Mkdir(databasePath, 0o700); err != nil {
			t.Fatalf("create directory at database path: %v", err)
		}
		if _, err := Open(context.Background(), databasePath); err == nil {
			t.Fatal("Open() error = nil, want non-regular-file rejection")
		}
	})
}

func TestOpen_LockContentionCancelsAndRollbackPreservesWriterProgress(t *testing.T) {
	databasePath := filepath.Join(t.TempDir(), "devcrew.db")
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
