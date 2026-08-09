package comiswire

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

const controlTestBearer = "control_test_bearer_0123456789abcdef"

type durableControlHandler struct {
	mu          sync.Mutex
	activations map[OperationID]ActivateRequestParams
	effects     int
}

func (handler *durableControlHandler) Activate(_ context.Context, params ActivateRequestParams) (ActivateResponseResult, error) {
	handler.mu.Lock()
	defer handler.mu.Unlock()
	if prior, found := handler.activations[params.OperationID]; found {
		if prior != params {
			return ActivateResponseResult{}, RPCError{Code: -32009, Kind: ErrorKindReplayConflict, Message: "altered activation replay", Retryable: false}
		}
		return activateResult(params), nil
	}
	handler.activations[params.OperationID] = params
	handler.effects++
	return activateResult(params), nil
}

func (handler *durableControlHandler) Abandon(_ context.Context, params AbandonRequestParams) (AbandonResponseResult, error) {
	return AbandonResponseResult{ExternalRunRef: params.ExternalRunRef, State: ManagedRunStateAbandoned}, nil
}

func (handler *durableControlHandler) effectCount() int {
	handler.mu.Lock()
	defer handler.mu.Unlock()
	return handler.effects
}

func TestControlConnectionPersistsAcrossBidirectionalTrafficAndReplaysAfterReconnect(t *testing.T) {
	socketPath, listener := controlTestListener(t)
	handler := &durableControlHandler{activations: make(map[OperationID]ActivateRequestParams)}
	connection, err := NewControlConnection(ControlConnectionConfig{
		SocketPath: socketPath, Credential: controlTestBearer,
		ServiceInstanceID: "service-instance_a", HandshakeOperationID: "operation_handshake_a",
		Handler: handler, RequestTimeout: time.Second,
		MinimumBackoff: time.Millisecond, MaximumBackoff: 2 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("NewControlConnection() error = %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	runDone := make(chan error, 1)
	go func() { runDone <- connection.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		if err := <-runDone; err != nil && !errors.Is(err, context.Canceled) {
			t.Errorf("Run() error = %v", err)
		}
	})

	activate := ActivateRequest{
		JSONRPC: JSONRPCVersion, ID: "operation_activate_a", Method: MethodManagedRunsActivate,
		Params: ActivateRequestParams{
			OperationID: "operation_activate_a", ManagedRunID: "managed-run_a",
			ExternalRunRef: "task-0001", RegistrationNonce: "registration-nonce_a",
		},
	}
	firstReady := make(chan struct{})
	firstComplete := make(chan struct{})
	secondComplete := make(chan struct{})
	serverDone := make(chan error, 1)
	go func() {
		first, err := listener.AcceptUnix()
		if err != nil {
			serverDone <- err
			return
		}
		if err := serveHandshake(first); err != nil {
			serverDone <- err
			return
		}
		if err := writeAuthenticatedFrame(first, activate, controlTestBearer); err != nil {
			serverDone <- err
			return
		}
		var activated ActivateResponse
		if err := readControlFrame(first, &activated); err != nil {
			serverDone <- err
			return
		}
		if activated.Result != activateResult(activate.Params) {
			serverDone <- fmt.Errorf("first activation acknowledgement differs: %#v", activated)
			return
		}
		close(firstReady)
		var report authenticatedReportRequest
		if err := readControlFrame(first, &report); err != nil {
			serverDone <- err
			return
		}
		if report.Bearer != controlTestBearer || report.Method != MethodManagedRunsReport {
			serverDone <- errors.New("report did not use authenticated persistent connection")
			return
		}
		if err := writeControlFrame(first, ReportResponse{
			JSONRPC: JSONRPCVersion, ID: report.ID,
			Result: ReportResponseResult{
				AcceptedSequence: 1, ManagedRunID: report.Params.ManagedRunID,
				ServiceReportID: report.Params.ServiceReportID, RetainedUntilMs: 1_800_000_000_000,
			},
		}); err != nil {
			serverDone <- err
			return
		}
		close(firstComplete)
		_ = first.Close()

		second, err := listener.AcceptUnix()
		if err != nil {
			serverDone <- err
			return
		}
		defer second.Close()
		if err := serveHandshake(second); err != nil {
			serverDone <- err
			return
		}
		if err := writeAuthenticatedFrame(second, activate, controlTestBearer); err != nil {
			serverDone <- err
			return
		}
		var replay ActivateResponse
		if err := readControlFrame(second, &replay); err != nil {
			serverDone <- err
			return
		}
		if replay.Result != activateResult(activate.Params) {
			serverDone <- errors.New("activation replay acknowledgement differs")
			return
		}
		close(secondComplete)
		<-ctx.Done()
		serverDone <- nil
	}()

	select {
	case <-firstReady:
	case <-time.After(time.Second):
		t.Fatal("first authenticated connection was not ready")
	}
	reportResult, err := connection.Report(context.Background(), ReportRequestParams{
		OperationID: "operation_report_a", ManagedRunID: "managed-run_a",
		ServiceReportID: "service-report_a", Kind: ReportKindProgress, Summary: "bounded progress",
	})
	if err != nil {
		select {
		case serverErr := <-serverDone:
			t.Fatalf("Report() error = %v; server error = %v", err, serverErr)
		default:
		}
		t.Fatalf("Report() error = %v", err)
	}
	if reportResult.AcceptedSequence != 1 || reportResult.ServiceReportID != "service-report_a" {
		t.Fatalf("Report() = %#v", reportResult)
	}
	select {
	case <-firstComplete:
	case <-time.After(time.Second):
		t.Fatal("first persistent session did not complete")
	}
	select {
	case <-secondComplete:
	case <-time.After(time.Second):
		t.Fatal("control connection did not reconnect and replay")
	}
	if effects := handler.effectCount(); effects != 1 {
		t.Fatalf("activation effects = %d, want 1", effects)
	}
	cancel()
	if err := <-serverDone; err != nil {
		t.Fatalf("control fixture server error = %v", err)
	}
}

func TestControlConnectionRejectsHostileBearerBeforeDispatch(t *testing.T) {
	socketPath, listener := controlTestListener(t)
	handler := &durableControlHandler{activations: make(map[OperationID]ActivateRequestParams)}
	connection, err := NewControlConnection(ControlConnectionConfig{
		SocketPath: socketPath, Credential: controlTestBearer,
		ServiceInstanceID: "service-instance_a", HandshakeOperationID: "operation_handshake_a",
		Handler: handler, RequestTimeout: time.Second,
		MinimumBackoff: time.Millisecond, MaximumBackoff: 2 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("NewControlConnection() error = %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- connection.Run(ctx) }()
	peer, err := listener.AcceptUnix()
	if err != nil {
		t.Fatal(err)
	}
	if err := serveHandshake(peer); err != nil {
		t.Fatal(err)
	}
	request := ActivateRequest{
		JSONRPC: JSONRPCVersion, ID: "operation_hostile_a", Method: MethodManagedRunsActivate,
		Params: ActivateRequestParams{
			OperationID: "operation_hostile_a", ManagedRunID: "managed-run_a",
			ExternalRunRef: "task-0001", RegistrationNonce: "registration-nonce_a",
		},
	}
	if err := writeAuthenticatedFrame(peer, request, "hostile_bearer_0123456789abcdefgh"); err != nil {
		t.Fatal(err)
	}
	var rejected ErrorResponse
	if err := readControlFrame(peer, &rejected); err != nil {
		t.Fatal(err)
	}
	if rejected.Error.Kind != ErrorKindUnauthorizedInstance || handler.effectCount() != 0 {
		t.Fatalf("hostile request response = %#v, effects = %d", rejected, handler.effectCount())
	}
	cancel()
	_ = peer.Close()
	if err := <-done; err != nil && !errors.Is(err, context.Canceled) {
		t.Fatalf("Run() error = %v", err)
	}
}

func activateResult(params ActivateRequestParams) ActivateResponseResult {
	return ActivateResponseResult{
		ActivatedAtMs: 1_700_000_000_000, ExternalRunRef: params.ExternalRunRef,
		ManagedRunID: params.ManagedRunID, State: ManagedRunStateActive,
	}
}

func controlTestListener(t *testing.T) (string, *net.UnixListener) {
	t.Helper()
	directory, err := os.MkdirTemp("/private/tmp", "dc-ctl-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(directory) })
	path := filepath.Join(directory, "control.sock")
	address, err := net.ResolveUnixAddr("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	listener, err := net.ListenUnix("unix", address)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	return path, listener
}

func serveHandshake(connection net.Conn) error {
	var request authenticatedHandshakeRequest
	if err := readControlFrame(connection, &request); err != nil {
		return err
	}
	if request.Bearer != controlTestBearer || request.Params.ProtocolID != ProtocolID || request.Params.BundleDigest != BundleDigest {
		return errors.New("handshake identity differs")
	}
	return writeControlFrame(connection, validHandshakeResponse(func(response *HandshakeResponse) {
		response.ID = request.ID
		response.Result.ServiceInstanceID = request.Params.ServiceInstanceID
	}))
}

func writeAuthenticatedFrame(connection net.Conn, payload any, bearer string) error {
	encoded, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &object); err != nil {
		return err
	}
	credential, _ := json.Marshal(bearer)
	object["bearer"] = credential
	return writeControlFrame(connection, object)
}

func writeControlFrame(connection net.Conn, payload any) error {
	return json.NewEncoder(connection).Encode(payload)
}

func readControlFrame(connection net.Conn, destination any) error {
	line := make([]byte, 0, 1024)
	for len(line) <= MaxLineBytes {
		var next [1]byte
		if _, err := connection.Read(next[:]); err != nil {
			return err
		}
		if next[0] == '\n' {
			return json.Unmarshal(line, destination)
		}
		line = append(line, next[0])
	}
	return errors.New("control test frame exceeds line limit")
}
