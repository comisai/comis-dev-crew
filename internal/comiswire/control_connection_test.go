package comiswire

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/comisai/comis-dev-crew/internal/application"
)

var _ interface {
	Release(context.Context, ReleaseRequestParams) (ReleaseResponseResult, error)
} = (*ControlConnection)(nil)

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
	return abandonResult(params), nil
}

func (handler *durableControlHandler) TerminalEvent(_ context.Context, params TerminalEventRequestParams) (TerminalEventResponseResult, error) {
	return TerminalEventResponseResult{
		ManagedRunID: params.ManagedRunID, TerminalSessionID: params.TerminalSessionID, Transition: params.Transition,
	}, nil
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

func TestControlConnectionSendsVerifiedEvidenceOnAuthenticatedPersistentSession(t *testing.T) {
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
	serverDone := make(chan error, 1)
	body := []byte("verified candidate evidence")
	contentHash := fmt.Sprintf("%x", sha256.Sum256(body))
	go func() {
		peer, acceptErr := listener.AcceptUnix()
		if acceptErr != nil {
			serverDone <- acceptErr
			return
		}
		defer peer.Close()
		if handshakeErr := serveHandshake(peer); handshakeErr != nil {
			serverDone <- handshakeErr
			return
		}
		var request authenticatedPutEvidenceRequest
		if readErr := readControlFrame(peer, &request); readErr != nil {
			serverDone <- readErr
			return
		}
		if request.Bearer != controlTestBearer || request.Method != MethodManagedRunsPutEvidence ||
			request.Params.BodyBase64 != base64.StdEncoding.EncodeToString(body) {
			serverDone <- errors.New("evidence did not use authenticated persistent connection")
			return
		}
		retainedUntil := int64(1_800_000_000_000)
		if writeErr := writeControlFrame(peer, PutEvidenceResponse{
			JSONRPC: JSONRPCVersion, ID: request.ID,
			Result: PutEvidenceResponseResult{
				ManagedRunID: request.Params.ManagedRunID, EvidenceRef: request.Params.EvidenceRef,
				ContentHash: request.Params.ContentHash, VerificationLevel: request.Params.VerificationLevel,
				RetainedUntilMs: &retainedUntil,
			},
		}); writeErr != nil {
			serverDone <- writeErr
			return
		}
		serverDone <- nil
		// Hold the peer open until the test cancels. Returning here would run
		// the deferred Close and can tear the session down before the client
		// has consumed its response, which surfaces as an uncertain EOF.
		<-ctx.Done()
	}()

	result, err := connection.PutEvidence(context.Background(), PutEvidenceRequestParams{
		OperationID: "operation_evidence_a", ManagedRunID: "managed-run_a", EvidenceRef: "evidence_a",
		Kind: "candidate_bundle", SubjectDigest: contentHash, ObservedAtMs: 1_700_000_000_000,
		ContentHash: contentHash, VerificationLevel: EvidenceVerificationLevelAdapterVerified,
		BodyBase64: base64.StdEncoding.EncodeToString(body),
	})
	if err != nil {
		t.Fatalf("PutEvidence() error = %v", err)
	}
	if result.EvidenceRef != "evidence_a" || result.ContentHash != contentHash || result.RetainedUntilMs == nil {
		t.Fatalf("PutEvidence() = %#v", result)
	}
	if serverErr := <-serverDone; serverErr != nil {
		t.Fatalf("control fixture server error = %v", serverErr)
	}
	cancel()
	if err := <-runDone; err != nil && !errors.Is(err, context.Canceled) {
		t.Fatalf("Run() error = %v", err)
	}
}

func TestControlConnectionSendsReleaseOnPersistentAuthenticatedSession(t *testing.T) {
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
	serverDone := make(chan error, 1)
	go func() {
		peer, acceptErr := listener.AcceptUnix()
		if acceptErr != nil {
			serverDone <- acceptErr
			return
		}
		defer peer.Close()
		if handshakeErr := serveHandshake(peer); handshakeErr != nil {
			serverDone <- handshakeErr
			return
		}
		var request authenticatedReleaseRequest
		if readErr := readControlFrame(peer, &request); readErr != nil {
			serverDone <- readErr
			return
		}
		if request.Bearer != controlTestBearer || request.Method != MethodManagedRunsRelease ||
			request.Params.WorkspaceLeaseID != "workspace-lease_a" {
			serverDone <- errors.New("release did not use authenticated persistent connection")
			return
		}
		if writeErr := writeControlFrame(peer, ReleaseResponse{
			JSONRPC: JSONRPCVersion, ID: request.ID,
			Result: ReleaseResponseResult{
				ManagedRunID: request.Params.ManagedRunID, WorkspaceLeaseID: request.Params.WorkspaceLeaseID,
				State: ManagedRunState("released"), Disposition: request.Params.Disposition,
				ReleasedAtMs: request.Params.ReleasedAtMs,
			},
		}); writeErr != nil {
			serverDone <- writeErr
			return
		}
		serverDone <- nil
		// Hold the peer open until the test cancels. Returning here would run
		// the deferred Close and can tear the session down before the client
		// has consumed its response, which surfaces as an uncertain EOF.
		<-ctx.Done()
	}()

	var releaser application.ManagedRunReleaser = connection
	result, err := releaser.ReleaseManagedRun(context.Background(), application.ManagedRunReleaseRequest{
		OperationID: "operation_release_a", ManagedRunID: "managed-run_a",
		WorkspaceLeaseID: "workspace-lease_a", Disposition: application.ManagedRunReleaseReapSafe,
		ReleasedAt: time.UnixMilli(1_800_000_000_000).UTC(),
	})
	if err != nil || result.State != application.ManagedRunReleased {
		t.Fatalf("ReleaseManagedRun() = %#v, %v", result, err)
	}
	if serverErr := <-serverDone; serverErr != nil {
		t.Fatalf("control fixture server error = %v", serverErr)
	}
	cancel()
	if err := <-runDone; err != nil && !errors.Is(err, context.Canceled) {
		t.Fatalf("Run() error = %v", err)
	}
}

func TestControlConnectionRejectsInvalidEvidenceAndReleaseBeforeDispatch(t *testing.T) {
	socketPath, _ := controlTestListener(t)
	connection, err := NewControlConnection(ControlConnectionConfig{
		SocketPath: socketPath, Credential: controlTestBearer,
		ServiceInstanceID: "service-instance_a", HandshakeOperationID: "operation_handshake_a",
		Handler:        &durableControlHandler{activations: make(map[OperationID]ActivateRequestParams)},
		RequestTimeout: time.Second, MinimumBackoff: time.Millisecond, MaximumBackoff: 2 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("NewControlConnection() error = %v", err)
	}
	body := []byte("bounded evidence")
	contentHash := fmt.Sprintf("%x", sha256.Sum256(body))
	params := PutEvidenceRequestParams{
		OperationID: "operation_evidence_validation", ManagedRunID: "managed-run_a", EvidenceRef: "evidence_a",
		Kind: "candidate_bundle", SubjectDigest: contentHash, ObservedAtMs: 1_800_000_000_000,
		ContentHash: contentHash, VerificationLevel: EvidenceVerificationLevelAdapterVerified,
		BodyBase64: base64.StdEncoding.EncodeToString(body),
	}
	if _, err := connection.PutEvidence(missingContext(), params); err == nil {
		t.Fatal("PutEvidence(nil context) error = nil")
	}
	invalidBody := params
	invalidBody.BodyBase64 = "not-base64"
	if _, err := connection.PutEvidence(context.Background(), invalidBody); err == nil {
		t.Fatal("PutEvidence(invalid body) error = nil")
	}
	wrongHash := params
	wrongHash.ContentHash = strings.Repeat("0", 64)
	if _, err := connection.PutEvidence(context.Background(), wrongHash); err == nil {
		t.Fatal("PutEvidence(wrong hash) error = nil")
	}
	hostVerified := params
	hostVerified.VerificationLevel = EvidenceVerificationLevelHostVerified
	if _, err := connection.PutEvidence(context.Background(), hostVerified); err == nil {
		t.Fatal("PutEvidence(host verified) error = nil")
	}
	invalidRequest := params
	invalidRequest.OperationID = "bad operation"
	if _, err := connection.PutEvidence(context.Background(), invalidRequest); err == nil {
		t.Fatal("PutEvidence(invalid request) error = nil")
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := connection.PutEvidence(canceled, params); !errors.Is(err, context.Canceled) {
		t.Fatalf("PutEvidence(canceled) error = %v, want context.Canceled", err)
	}
	if _, err := connection.Release(missingContext(), ReleaseRequestParams{
		OperationID: "operation_release_validation", ManagedRunID: "managed-run_a",
		WorkspaceLeaseID: "workspace-lease_a", Disposition: "reap_safe", ReleasedAtMs: 1_800_000_000_000,
	}); err == nil {
		t.Fatal("Release(nil context) error = nil")
	}
	if _, err := connection.Release(context.Background(), ReleaseRequestParams{}); err == nil {
		t.Fatal("Release(invalid request) error = nil")
	}
	if _, err := connection.ReleaseManagedRun(context.Background(), application.ManagedRunReleaseRequest{}); err == nil {
		t.Fatal("ReleaseManagedRun(invalid request) error = nil")
	}
	if _, err := connection.ReleaseManagedRun(canceled, application.ManagedRunReleaseRequest{
		OperationID: "operation_release_validation", ManagedRunID: "managed-run_a",
		WorkspaceLeaseID: "workspace-lease_a", Disposition: application.ManagedRunReleaseReapSafe,
		ReleasedAt: time.UnixMilli(1_800_000_000_000).UTC(),
	}); !errors.Is(err, context.Canceled) {
		t.Fatalf("ReleaseManagedRun(canceled) error = %v, want context.Canceled", err)
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

// socketTempDir creates a temporary directory short enough to hold a Unix
// socket path, which the kernel bounds near 104 bytes. The platform temporary
// directory is far longer than that on macOS, so this resolves /tmp instead and
// keeps the result canonical: macOS reports /tmp as a symlink to /private/tmp,
// and the connection paths compare canonical identities. Resolving rather than
// hard-coding either spelling keeps the helper correct on Linux and macOS.
func socketTempDir(t *testing.T, prefix string) string {
	t.Helper()
	root, err := filepath.EvalSymlinks("/tmp")
	if err != nil {
		t.Fatalf("resolve /tmp: %v", err)
	}
	directory, err := os.MkdirTemp(root, prefix)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(directory) })
	return directory
}

func controlTestListener(t *testing.T) (string, *net.UnixListener) {
	t.Helper()
	directory := socketTempDir(t, "dc-ctl-")
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
		response.Result.ActiveScopes = append([]ServiceScope(nil), request.Params.RequestedScopes...)
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
