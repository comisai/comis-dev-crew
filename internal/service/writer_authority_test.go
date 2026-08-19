package service

import (
	"context"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/comisai/comis-dev-crew/internal/domain"
	"github.com/comisai/comis-dev-crew/internal/localapi"
)

// TestRun_RefusesASecondMutatingCoordinatorOnOneDataDirectory pins the sole
// writer to the data directory rather than to the socket path.
//
// The endpoint already refuses to replace a live service, but that guard is
// reached only after the store has been opened, migrated, and driven through
// startup recovery. A second process aimed at the same state with any other
// endpoint therefore becomes a second mutating coordinator, and its recovery
// pass marks the live instance's running work unknown on the way in.
func TestRun_RefusesASecondMutatingCoordinatorOnOneDataDirectory(t *testing.T) {
	root := shortTempDir(t)
	databasePath := filepath.Join(root, "state", "devcrew.db")
	firstSocket := filepath.Join(root, "run", "devcrew.sock")
	secondSocket := filepath.Join(root, "run", "second.sock")

	ready := make(chan struct{})
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	first := make(chan error, 1)
	go func() {
		first <- Run(ctx, Config{
			DatabasePath: databasePath, SocketPath: firstSocket,
			Clock: time.Now, Ready: func() { close(ready) },
		})
	}()
	select {
	case <-ready:
	case err := <-first:
		t.Fatalf("first Run() before ready error = %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("first Run() did not advertise ready")
	}

	var secondReady atomic.Bool
	secondCtx, cancelSecond := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancelSecond)
	secondErr := Run(secondCtx, Config{
		DatabasePath: databasePath, SocketPath: secondSocket,
		Clock: time.Now, Ready: func() { secondReady.Store(true) },
	})
	if secondErr == nil {
		t.Fatal("second Run() on a live data directory succeeded, want a refusal")
	}
	if secondReady.Load() {
		t.Error("second Run() advertised ready on a data directory another instance owns")
	}
	if !strings.Contains(secondErr.Error(), "data directory") {
		t.Errorf("second Run() error = %v, want a refusal naming the contended data directory", secondErr)
	}

	client, err := localapi.NewClient(firstSocket, time.Second)
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	if _, err := client.Fleet(context.Background(), "read-after-refusal"); err != nil {
		t.Fatalf("first instance stopped serving after the refusal: %v", err)
	}

	cancel()
	if err := <-first; err != nil {
		t.Fatalf("first Run() error = %v", err)
	}
}

// TestRun_LeavesRunningWorkAloneWhenItCannotOwnTheStore proves the refusal
// happens before recovery, not after it. Startup reconciliation converts every
// runtime-sensitive task to unknown; running it from a process that does not
// own the store is the concrete damage the lock exists to prevent.
func TestRun_LeavesRunningWorkAloneWhenItCannotOwnTheStore(t *testing.T) {
	root := shortTempDir(t)
	databasePath := filepath.Join(root, "state", "devcrew.db")
	firstSocket := filepath.Join(root, "run", "devcrew.sock")
	secondSocket := filepath.Join(root, "run", "second.sock")

	ready := make(chan struct{})
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	first := make(chan error, 1)
	go func() {
		first <- Run(ctx, Config{
			DatabasePath: databasePath, SocketPath: firstSocket,
			Clock: time.Now, Ready: func() { close(ready) },
		})
	}()
	select {
	case <-ready:
	case err := <-first:
		t.Fatalf("first Run() before ready error = %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("first Run() did not advertise ready")
	}

	client, err := localapi.NewClient(firstSocket, time.Second)
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	before, err := client.Fleet(context.Background(), "read-state-version-before")
	if err != nil {
		t.Fatalf("Fleet() before contention error = %v", err)
	}

	secondCtx, cancelSecond := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancelSecond)
	if err := Run(secondCtx, Config{
		DatabasePath: databasePath, SocketPath: secondSocket, Clock: time.Now,
	}); err == nil {
		t.Fatal("second Run() succeeded on a live data directory, want a refusal")
	}

	after, err := client.Fleet(context.Background(), "read-state-version-after")
	if err != nil {
		t.Fatalf("Fleet() after contention error = %v", err)
	}
	if after.StateVersion != before.StateVersion {
		t.Errorf("state version moved from %d to %d while another process was refused",
			before.StateVersion, after.StateVersion)
	}
	for _, task := range after.Tasks {
		if task.State == domain.TaskUnknown {
			t.Errorf("task %s was marked unknown by a process that does not own the store", task.TaskHandle)
		}
	}

	cancel()
	if err := <-first; err != nil {
		t.Fatalf("first Run() error = %v", err)
	}
}
