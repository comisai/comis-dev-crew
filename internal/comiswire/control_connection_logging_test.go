package comiswire

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/comisai/comis-dev-crew/internal/application"
	"github.com/comisai/comis-dev-crew/internal/domain"
)

type controlConnectionBoundaryLogger struct {
	records chan application.BoundaryRecord
}

func (logger *controlConnectionBoundaryLogger) Record(record application.BoundaryRecord) {
	logger.records <- record
}

func TestControlConnectionRecordsFirstHandshakeAuthorityFailure(t *testing.T) {
	socketPath, listener := controlTestListener(t)
	logger := &controlConnectionBoundaryLogger{records: make(chan application.BoundaryRecord, 1)}
	connection, err := NewControlConnection(ControlConnectionConfig{
		SocketPath: socketPath, Credential: controlTestBearer,
		ServiceInstanceID: "service-instance_logging", HandshakeOperationID: "operation_handshake_logging",
		Handler: controlHandlerStub{}, RequestTimeout: time.Second,
		MinimumBackoff: time.Millisecond, MaximumBackoff: 2 * time.Millisecond,
		Logger: logger,
	})
	if err != nil {
		t.Fatalf("NewControlConnection() error = %v", err)
	}

	serverDone := make(chan error, 1)
	go func() {
		peer, acceptErr := listener.AcceptUnix()
		if acceptErr != nil {
			serverDone <- acceptErr
			return
		}
		defer peer.Close()
		var request authenticatedHandshakeRequest
		if readErr := readControlFrame(peer, &request); readErr != nil {
			serverDone <- readErr
			return
		}
		serverDone <- writeControlFrame(peer, ErrorResponse{
			JSONRPC: JSONRPCVersion, ID: &request.ID,
			Error: RPCError{Code: -32018, Kind: ErrorKindPreconditionFailed, Message: "precondition failed"},
		})
	}()

	ctx, cancel := context.WithCancel(context.Background())
	runDone := make(chan error, 1)
	go func() { runDone <- connection.Run(ctx) }()

	var record application.BoundaryRecord
	select {
	case record = <-logger.records:
	case <-time.After(time.Second):
		cancel()
		t.Fatal("control connection recorded no handshake failure")
	}
	if record.Boundary != application.BoundaryControl || record.Operation != string(MethodCapabilityServicesHandshake) ||
		record.Outcome != application.BoundaryFailed || record.ErrorKind != domain.ErrorPrecondition ||
		string(record.FailureCause) != "control_handshake_precondition_failed" ||
		record.Hint != "verify the configured control scope set and pinned protocol identity" {
		t.Fatalf("handshake failure record = %#v", record)
	}
	cancel()
	if runErr := <-runDone; !errors.Is(runErr, context.Canceled) {
		t.Fatalf("Run() error = %v", runErr)
	}
	if serverErr := <-serverDone; serverErr != nil {
		t.Fatalf("control fixture server error = %v", serverErr)
	}
}
