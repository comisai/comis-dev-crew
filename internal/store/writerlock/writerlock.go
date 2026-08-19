// Package writerlock grants one process exclusive authority to mutate a
// durable data directory.
//
// The durable store has exactly one writer. Endpoint binding cannot enforce
// that on its own: it proves only that one socket path is taken, while the
// state a second process would corrupt is the database beside it. A second
// service aimed at the same directory through any other endpoint would open
// the store, migrate it, and run startup recovery — which converts running
// work to unknown — before ever discovering the endpoint was occupied.
//
// The lock is therefore acquired against the directory, before the store is
// opened, and held for the owning process's lifetime.
package writerlock

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

// fileName is the advisory lock file kept beside the durable store. Its
// contents are never authority: ownership is the held kernel lock, so a stale
// file left by a killed process grants nothing and blocks nothing.
const fileName = ".writer.lock"

// ErrHeld reports that another live process owns the data directory. It is a
// distinct sentinel because refusing to start is the correct, expected outcome
// of contention, not a failure to investigate.
var ErrHeld = errors.New("data directory is owned by another running service instance")

// Lock is held exclusive authority over one data directory.
type Lock struct {
	file   *os.File
	unlock func(int) error
}

// dependencies isolates the two OS effects this package performs so their
// failure branches are provable without arranging a hostile kernel.
type dependencies struct {
	makeDirectory func(string, os.FileMode) error
	openFile      func(string, int, os.FileMode) (*os.File, error)
	lock          func(int) error
	unlock        func(int) error
}

func realDependencies() dependencies {
	return dependencies{
		makeDirectory: os.MkdirAll,
		openFile:      os.OpenFile,
		lock:          func(descriptor int) error { return syscall.Flock(descriptor, syscall.LOCK_EX|syscall.LOCK_NB) },
		unlock:        func(descriptor int) error { return syscall.Flock(descriptor, syscall.LOCK_UN) },
	}
}

// Acquire takes exclusive authority over the directory holding databasePath.
// It never blocks: a directory another instance owns returns ErrHeld so the
// caller can refuse cleanly rather than queue behind an instance that may run
// for weeks.
func Acquire(databasePath string) (*Lock, error) {
	return acquire(databasePath, realDependencies())
}

func acquire(databasePath string, deps dependencies) (*Lock, error) {
	if !filepath.IsAbs(databasePath) || filepath.Clean(databasePath) != databasePath {
		return nil, errors.New("acquire writer lock: database path must be absolute and canonical")
	}
	directory := filepath.Dir(databasePath)
	if err := deps.makeDirectory(directory, 0o700); err != nil {
		return nil, fmt.Errorf("acquire writer lock directory: %w", err)
	}
	path := filepath.Join(directory, fileName)
	file, err := deps.openFile(path, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open writer lock: %w", err)
	}
	if err := deps.lock(int(file.Fd())); err != nil {
		closeErr := file.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, errors.Join(fmt.Errorf("%s: %w", directory, ErrHeld), closeErr)
		}
		return nil, errors.Join(fmt.Errorf("lock writer lock: %w", err), closeErr)
	}
	return &Lock{file: file, unlock: deps.unlock}, nil
}

// Release drops exclusive authority. The lock file is left in place: removing
// it would let a second process create a fresh file and lock that instead,
// which is the exact concurrency this package exists to refuse.
func (lock *Lock) Release() error {
	if lock == nil || lock.file == nil {
		return nil
	}
	file := lock.file
	unlock := lock.unlock
	lock.file = nil
	unlockErr := unlock(int(file.Fd()))
	closeErr := file.Close()
	if unlockErr != nil {
		return errors.Join(fmt.Errorf("unlock writer lock: %w", unlockErr), closeErr)
	}
	if closeErr != nil {
		return fmt.Errorf("close writer lock: %w", closeErr)
	}
	return nil
}
