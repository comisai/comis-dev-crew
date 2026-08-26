package writerlock

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

func databaseIn(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "state", "devcrew.db")
}

func TestAcquire_GrantsExactlyOneHolder(t *testing.T) {
	databasePath := databaseIn(t)
	first, err := Acquire(databasePath)
	if err != nil {
		t.Fatalf("Acquire() error = %v", err)
	}
	t.Cleanup(func() { _ = first.Release() })

	second, err := Acquire(databasePath)
	if !errors.Is(err, ErrHeld) {
		t.Fatalf("second Acquire() = %v, %v, want ErrHeld", second, err)
	}
	if second != nil {
		t.Error("second Acquire() returned a lock alongside its refusal")
	}
}

func TestAcquire_ReclaimsAfterRelease(t *testing.T) {
	databasePath := databaseIn(t)
	first, err := Acquire(databasePath)
	if err != nil {
		t.Fatalf("Acquire() error = %v", err)
	}
	if err := first.Release(); err != nil {
		t.Fatalf("Release() error = %v", err)
	}
	second, err := Acquire(databasePath)
	if err != nil {
		t.Fatalf("Acquire() after release error = %v", err)
	}
	if err := second.Release(); err != nil {
		t.Fatalf("second Release() error = %v", err)
	}
}

// TestAcquire_TreatsAnUnheldLockFileAsAvailable is the crash case: a service
// killed without unwinding leaves the file behind. The file is not authority —
// the kernel lock is — so a restart must not be blocked by its own debris.
func TestAcquire_TreatsAnUnheldLockFileAsAvailable(t *testing.T) {
	databasePath := databaseIn(t)
	directory := filepath.Dir(databasePath)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		t.Fatalf("create state directory: %v", err)
	}
	if err := os.WriteFile(filepath.Join(directory, fileName), nil, 0o600); err != nil {
		t.Fatalf("write stale lock file: %v", err)
	}
	lock, err := Acquire(databasePath)
	if err != nil {
		t.Fatalf("Acquire() over a stale lock file error = %v", err)
	}
	if err := lock.Release(); err != nil {
		t.Fatalf("Release() error = %v", err)
	}
}

func TestRelease_IsSafeWhenUnheld(t *testing.T) {
	var absent *Lock
	if err := absent.Release(); err != nil {
		t.Errorf("Release() on a nil lock = %v, want nil", err)
	}
	lock, err := Acquire(databaseIn(t))
	if err != nil {
		t.Fatalf("Acquire() error = %v", err)
	}
	if err := lock.Release(); err != nil {
		t.Fatalf("Release() error = %v", err)
	}
	if err := lock.Release(); err != nil {
		t.Errorf("second Release() = %v, want nil", err)
	}
}

func TestAcquire_RefusesPathsThatAreNotCanonicalAbsolute(t *testing.T) {
	for name, path := range map[string]string{
		"relative":     "state/devcrew.db",
		"uncleaned":    "/tmp/state/../state/devcrew.db",
		"empty":        "",
		"trailingDots": "/tmp/state/./devcrew.db",
	} {
		t.Run(name, func(t *testing.T) {
			if lock, err := Acquire(path); err == nil {
				_ = lock.Release()
				t.Fatalf("Acquire(%q) succeeded, want a refusal", path)
			}
		})
	}
}

func TestAcquire_KeepsTheLockFileOwnerOnly(t *testing.T) {
	databasePath := databaseIn(t)
	lock, err := Acquire(databasePath)
	if err != nil {
		t.Fatalf("Acquire() error = %v", err)
	}
	t.Cleanup(func() { _ = lock.Release() })
	info, err := os.Stat(filepath.Join(filepath.Dir(databasePath), fileName))
	if err != nil {
		t.Fatalf("stat lock file: %v", err)
	}
	if mode := info.Mode().Perm(); mode != 0o600 {
		t.Errorf("lock file mode = %o, want 600", mode)
	}
}

func TestAcquire_RefusesADirectoryItCannotCreate(t *testing.T) {
	root := t.TempDir()
	blocker := filepath.Join(root, "state")
	if err := os.WriteFile(blocker, nil, 0o600); err != nil {
		t.Fatalf("write blocking file: %v", err)
	}
	if lock, err := Acquire(filepath.Join(blocker, "devcrew.db")); err == nil {
		_ = lock.Release()
		t.Fatal("Acquire() succeeded where the state directory cannot exist, want a refusal")
	}
}

// TestAcquire_ReportsAKernelRefusalItCannotInterpret keeps an unexpected lock
// failure distinguishable from contention. Reading every refusal as "another
// instance owns this" would tell an operator to hunt a process that does not
// exist.
func TestAcquire_ReportsAKernelRefusalItCannotInterpret(t *testing.T) {
	deps := realDependencies()
	deps.lock = func(int) error { return syscall.EIO }
	lock, err := acquire(databaseIn(t), deps)
	if err == nil {
		_ = lock.Release()
		t.Fatal("acquire() succeeded despite a refused lock, want a failure")
	}
	if errors.Is(err, ErrHeld) {
		t.Errorf("acquire() = %v, want a failure distinct from contention", err)
	}
}

func TestAcquire_ReportsAnUnopenableLockFile(t *testing.T) {
	deps := realDependencies()
	deps.openFile = func(string, int, os.FileMode) (*os.File, error) { return nil, syscall.EACCES }
	if lock, err := acquire(databaseIn(t), deps); err == nil {
		_ = lock.Release()
		t.Fatal("acquire() succeeded without an open lock file, want a failure")
	}
}

func TestRelease_ReportsAFailedUnlock(t *testing.T) {
	deps := realDependencies()
	deps.unlock = func(int) error { return syscall.EIO }
	lock, err := acquire(databaseIn(t), deps)
	if err != nil {
		t.Fatalf("acquire() error = %v", err)
	}
	if err := lock.Release(); err == nil {
		t.Fatal("Release() hid a failed unlock, want the failure reported")
	}
}
