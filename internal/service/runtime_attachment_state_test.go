package service

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/comisai/comis-dev-crew/internal/store/sqlite"
)

func TestRuntimeAttachmentPreparationReplayJoinsPendingRegistration(t *testing.T) {
	root := shortTempDir(t)
	workspace := filepath.Join(root, "workspace")
	if err := os.Mkdir(workspace, 0o700); err != nil {
		t.Fatal(err)
	}
	store, err := sqlite.Open(context.Background(), filepath.Join(root, "state", "devcrew.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	coordinator, err := newRuntimeAttachmentCoordinator(runtimeAttachmentCoordinatorConfig{
		RuntimeRoot: filepath.Join(root, "runtime"), Store: store, Clock: time.Now,
		NewCredential:           func() (string, error) { return "pending-registration-0123456789abcdef", nil },
		NewAttentionOperationID: runtimeAttentionOperationID,
	})
	if err != nil {
		t.Fatal(err)
	}
	close(coordinator.recoveryReady)
	request := runtimeAttachmentRequest(t, workspace, "task-pending-registration-replay")
	ctx, cancel := context.WithCancel(context.Background())
	first := make(chan error, 1)
	go func() {
		_, err := coordinator.PrepareRuntimeAttachment(ctx, request)
		first <- err
	}()
	waitForRuntimeAttachmentEntry(t, coordinator, request.TaskHandle)
	replayObserved := make(chan struct{}, 1)
	coordinator.runtimeAttachmentReplayObserved = func() { replayObserved <- struct{}{} }
	replay := make(chan error, 1)
	go func() {
		_, err := coordinator.PrepareRuntimeAttachment(context.Background(), request)
		replay <- err
	}()
	select {
	case <-replayObserved:
	case <-time.After(time.Second):
		t.Fatal("preparation replay did not observe pending registration")
	}
	cancel()
	if err := <-first; !errors.Is(err, context.Canceled) {
		t.Fatalf("first PrepareRuntimeAttachment() error = %v", err)
	}
	if err := <-replay; !errors.Is(err, context.Canceled) {
		t.Fatalf("replayed PrepareRuntimeAttachment() error = %v", err)
	}
}

func TestRuntimeAttachmentReleaseReplayJoinsExactRelease(t *testing.T) {
	root := shortTempDir(t)
	workspace := filepath.Join(root, "workspace")
	if err := os.Mkdir(workspace, 0o700); err != nil {
		t.Fatal(err)
	}
	store, err := sqlite.Open(context.Background(), filepath.Join(root, "state", "devcrew.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	coordinator, err := newRuntimeAttachmentCoordinator(runtimeAttachmentCoordinatorConfig{
		RuntimeRoot: filepath.Join(root, "runtime"), Store: store, Clock: time.Now,
		NewCredential:           func() (string, error) { return "joined-release-0123456789abcdef0", nil },
		NewAttentionOperationID: runtimeAttentionOperationID,
	})
	if err != nil {
		t.Fatal(err)
	}
	runCtx, stop := context.WithCancel(context.Background())
	runDone := make(chan error, 1)
	go func() { runDone <- coordinator.Run(runCtx) }()
	t.Cleanup(func() {
		stop()
		if err := <-runDone; err != nil {
			t.Errorf("runtime coordinator stop error = %v", err)
		}
	})
	request := runtimeAttachmentRequest(t, workspace, "task-release-replay-join")
	if _, err := coordinator.PrepareRuntimeAttachment(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	paused := make(chan struct{})
	resume := make(chan struct{})
	crash := errors.New("simulated release interruption")
	coordinator.afterRuntimeAttachmentClose = func() error {
		close(paused)
		<-resume
		return crash
	}
	first := make(chan error, 1)
	go func() { first <- coordinator.ReleaseRuntimeAttachment(context.Background(), request.TaskHandle) }()
	<-paused
	replayObserved := make(chan struct{}, 1)
	coordinator.runtimeAttachmentReleaseReplayObserved = func() { replayObserved <- struct{}{} }
	second := make(chan error, 1)
	go func() { second <- coordinator.ReleaseRuntimeAttachment(context.Background(), request.TaskHandle) }()
	select {
	case <-replayObserved:
		close(resume)
	case err := <-second:
		close(resume)
		<-first
		t.Fatalf("release replay bypassed the active release: %v", err)
	case <-time.After(time.Second):
		close(resume)
		<-first
		t.Fatal("release replay did not join the active release")
	}
	if err := <-first; !errors.Is(err, crash) {
		t.Fatalf("first ReleaseRuntimeAttachment() error = %v", err)
	}
	if err := <-second; !errors.Is(err, crash) {
		t.Fatalf("replayed ReleaseRuntimeAttachment() error = %v", err)
	}
}

func waitForRuntimeAttachmentEntry(
	t *testing.T,
	coordinator *runtimeAttachmentCoordinator,
	taskHandle string,
) *runtimeAttachmentEntry {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		coordinator.mu.Lock()
		entry := coordinator.entries[taskHandle]
		coordinator.mu.Unlock()
		if entry != nil {
			return entry
		}
		runtime.Gosched()
	}
	t.Fatal("runtime attachment entry was not published")
	return nil
}
