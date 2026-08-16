package service

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/comisai/comis-dev-crew/internal/store/sqlite"
)

func TestRuntimeAttachmentRegistrationDoesNotOwnCoordinatorStateWhileWaiting(t *testing.T) {
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
		NewCredential:           func() (string, error) { return "registration-state-0123456789abcdef", nil },
		NewAttentionOperationID: runtimeAttentionOperationID,
	})
	if err != nil {
		t.Fatal(err)
	}
	close(coordinator.recoveryReady)
	published := make(chan struct{})
	coordinator.afterRuntimeDirectoryPublish = func() error {
		close(published)
		return nil
	}
	request := runtimeAttachmentRequest(t, workspace, "task-registration-state-waiting")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	prepared := make(chan error, 1)
	go func() {
		_, err := coordinator.PrepareRuntimeAttachment(ctx, request)
		prepared <- err
	}()
	<-published
	configured := make(chan error, 1)
	go func() { configured <- coordinator.SetRecoveryAcknowledger(runtimeAttachmentAcknowledger{}) }()
	select {
	case err := <-configured:
		if err != nil {
			t.Fatalf("SetRecoveryAcknowledger() error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("coordinator state remained locked by pending registration")
	}
	cancel()
	if err := <-prepared; !errors.Is(err, context.Canceled) {
		t.Fatalf("PrepareRuntimeAttachment(cancelled registration) error = %v", err)
	}
}

func TestRuntimeAttachmentRegistrationJoinsAcceptedCancellation(t *testing.T) {
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
		NewCredential:           func() (string, error) { return "registration-cancel-0123456789abcdef", nil },
		NewAttentionOperationID: runtimeAttentionOperationID,
	})
	if err != nil {
		t.Fatal(err)
	}
	close(coordinator.recoveryReady)
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		registration := <-coordinator.registrations
		cancel()
		registration.ready <- ctx.Err()
	}()
	request := runtimeAttachmentRequest(t, workspace, "task-registration-cancelled")
	if _, err := coordinator.PrepareRuntimeAttachment(ctx, request); !errors.Is(err, context.Canceled) {
		t.Fatalf("PrepareRuntimeAttachment(accepted cancellation) error = %v", err)
	}
}
