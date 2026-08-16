package reporter

import (
	"context"
	"net"
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
