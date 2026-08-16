package reporter

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/comisai/comis-dev-crew/internal/application"
)

func TestRuntimeServerCloseCancelsAndJoinsAcceptedConnection(t *testing.T) {
	receiver := &blockedRuntimeAttentionReceiver{
		entered: make(chan struct{}), canceled: make(chan struct{}), release: make(chan struct{}),
	}
	socketPath := filepath.Join(boundaryRuntimeDirectory(t), "attachment.sock")
	server, err := ListenRuntime(RuntimeServerConfig{
		SocketPath: socketPath, Brief: boundaryBrief("task-runtime-release-join"), Reporter: &Client{},
		AttentionResponses: receiver,
		NewAttentionOperationID: func() (string, error) {
			return "attention-runtime-release-join", nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	server.launch = &RuntimeLaunchConfig{Expected: application.LaunchAcknowledgement{ManagedRunID: "managed-run.release-join"}}
	ctx, cancel := context.WithCancel(context.Background())
	serveDone := make(chan error, 1)
	go func() { serveDone <- server.Serve(ctx) }()
	connection, err := net.DialUnix("unix", nil, &net.UnixAddr{Name: socketPath, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := connection.Write([]byte(`{"version":"devcrew.runtime.v1","kind":"attention_response","externalKey":"database-choice"}` + "\n")); err != nil {
		t.Fatal(err)
	}
	select {
	case <-receiver.entered:
	case <-time.After(time.Second):
		t.Fatal("accepted runtime request did not reach its dependency")
	}
	closeDone := make(chan error, 1)
	go func() { closeDone <- server.Close() }()
	premature := false
	select {
	case <-receiver.canceled:
	case err := <-closeDone:
		premature = true
		if err != nil {
			t.Errorf("Close() error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Close() neither canceled nor completed the accepted request")
	}
	cancel()
	close(receiver.release)
	if !premature {
		if err := <-closeDone; err != nil {
			t.Errorf("Close() error = %v", err)
		}
	}
	if err := <-serveDone; err != nil {
		t.Errorf("Serve() error = %v", err)
	}
	if err := connection.Close(); err != nil {
		t.Errorf("connection close error = %v", err)
	}
	if premature {
		t.Fatal("Close() returned while an accepted request remained active")
	}
}

func TestListenRuntimePreservesReplacementWhenIdentityCaptureFails(t *testing.T) {
	root := boundaryRuntimeDirectory(t)
	socketPath := filepath.Join(root, "attachment.sock")
	originalPath := filepath.Join(root, "original.sock")
	var replacement *net.UnixListener
	var hookErr error
	t.Cleanup(func() {
		if replacement != nil {
			_ = replacement.Close()
		}
		_ = os.Remove(socketPath)
		_ = os.Remove(originalPath)
	})
	_, err := listenRuntime(RuntimeServerConfig{
		SocketPath: socketPath, Brief: boundaryBrief("task-runtime-capture-race"), Reporter: &Client{},
	}, func() {
		hookErr = os.Rename(socketPath, originalPath)
		if hookErr != nil {
			return
		}
		address, resolveErr := net.ResolveUnixAddr("unix", socketPath)
		if resolveErr != nil {
			hookErr = resolveErr
			return
		}
		replacement, hookErr = net.ListenUnix("unix", address)
		if hookErr != nil {
			return
		}
		replacement.SetUnlinkOnClose(false)
		hookErr = os.Chmod(socketPath, 0o600)
	})
	if hookErr != nil {
		t.Fatal(hookErr)
	}
	if err == nil {
		t.Fatal("listenRuntime(replaced socket) error = nil")
	}
	info, statErr := os.Lstat(socketPath)
	if statErr != nil || info.Mode()&os.ModeSocket == 0 {
		t.Fatalf("unverified replacement was not preserved: %#v, %v", info, statErr)
	}
}

func TestRuntimeServerServePropagatesCloseIdentityFailure(t *testing.T) {
	root := boundaryRuntimeDirectory(t)
	socketPath := filepath.Join(root, "attachment.sock")
	server, err := ListenRuntime(RuntimeServerConfig{
		SocketPath: socketPath, Brief: boundaryBrief("task-runtime-close-error"), Reporter: &Client{},
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- server.Serve(ctx) }()
	if err := os.Remove(socketPath); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(socketPath, []byte("replacement"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cancel()
		_ = server.Close()
		_ = os.Remove(socketPath)
	})
	cancel()
	if err := <-done; err == nil {
		t.Fatal("Serve() identity failure error = nil")
	}
	contents, err := os.ReadFile(socketPath)
	if err != nil || string(contents) != "replacement" {
		t.Fatalf("Serve removed ambiguous replacement: %q, %v", contents, err)
	}
}

type blockedRuntimeAttentionReceiver struct {
	entered  chan struct{}
	canceled chan struct{}
	release  chan struct{}
}

func (receiver *blockedRuntimeAttentionReceiver) ReceiveRuntimeAttentionResponse(
	ctx context.Context,
	_ AttentionResponseRequest,
) (AttentionResponse, error) {
	close(receiver.entered)
	<-ctx.Done()
	close(receiver.canceled)
	<-receiver.release
	return AttentionResponse{}, ctx.Err()
}
