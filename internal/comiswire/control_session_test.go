package comiswire

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

type controlHandlerStub struct {
	activate func(context.Context, ActivateRequestParams) (ActivateResponseResult, error)
	abandon  func(context.Context, AbandonRequestParams) (AbandonResponseResult, error)
	terminal func(context.Context, TerminalEventRequestParams) (TerminalEventResponseResult, error)
}

func (stub controlHandlerStub) Activate(ctx context.Context, params ActivateRequestParams) (ActivateResponseResult, error) {
	if stub.activate == nil {
		return activateResult(params), nil
	}
	return stub.activate(ctx, params)
}

func (stub controlHandlerStub) Abandon(ctx context.Context, params AbandonRequestParams) (AbandonResponseResult, error) {
	if stub.abandon == nil {
		return abandonResult(params), nil
	}
	return stub.abandon(ctx, params)
}

func (stub controlHandlerStub) TerminalEvent(ctx context.Context, params TerminalEventRequestParams) (TerminalEventResponseResult, error) {
	if stub.terminal == nil {
		return TerminalEventResponseResult{
			ManagedRunID: params.ManagedRunID, TerminalSessionID: params.TerminalSessionID, Transition: params.Transition,
		}, nil
	}
	return stub.terminal(ctx, params)
}

func TestControlConnectionRejectsInvalidConfiguration(t *testing.T) {
	valid := ControlConnectionConfig{
		SocketPath: filepath.Join("/private/tmp", "control-config.sock"), Credential: controlTestBearer,
		ServiceInstanceID: "service-instance_a", HandshakeOperationID: "operation_handshake_a",
		Handler: controlHandlerStub{}, RequestTimeout: time.Second,
		MinimumBackoff: time.Millisecond, MaximumBackoff: time.Second,
	}
	tests := []struct {
		name   string
		mutate func(*ControlConnectionConfig)
	}{
		{name: "handler", mutate: func(config *ControlConnectionConfig) { config.Handler = nil }},
		{name: "timeout", mutate: func(config *ControlConnectionConfig) { config.RequestTimeout = 0 }},
		{name: "minimum backoff", mutate: func(config *ControlConnectionConfig) { config.MinimumBackoff = 0 }},
		{name: "maximum backoff", mutate: func(config *ControlConnectionConfig) { config.MaximumBackoff = time.Nanosecond }},
		{name: "credential", mutate: func(config *ControlConnectionConfig) { config.Credential = "short" }},
		{name: "service instance", mutate: func(config *ControlConnectionConfig) { config.ServiceInstanceID = "bad instance" }},
		{name: "operation", mutate: func(config *ControlConnectionConfig) { config.HandshakeOperationID = "bad operation" }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			config := valid
			test.mutate(&config)
			if _, err := NewControlConnection(config); err == nil {
				t.Fatal("NewControlConnection() error = nil")
			}
		})
	}
	if _, err := NewControlConnection(valid); err != nil {
		t.Fatalf("NewControlConnection(valid) error = %v", err)
	}
}

func TestControlHandshakeRequestsCompleteRequiredScopeSet(t *testing.T) {
	request := controlHandshake(ControlConnectionConfig{
		ServiceInstanceID: "service-instance_scope", HandshakeOperationID: "operation_handshake_scope",
	})
	want := []ServiceScope{
		ServiceScopeHealth,
		ServiceScopeReport,
		ServiceScopeWorkspaceLease,
		ServiceScopeTerminalEvents,
		ServiceScopeExecutionAttachment,
	}
	if !slices.Equal(request.Params.RequestedScopes, want) {
		t.Fatalf("requested scopes = %v, want %v", request.Params.RequestedScopes, want)
	}
	if !sameControlScopes(want) {
		t.Fatal("complete required scope set was rejected")
	}
	if sameControlScopes(want[:len(want)-1]) {
		t.Fatal("missing execution attachment scope was accepted")
	}
}

func TestControlHandshakeAcceptsPinnedScopesAsAnOrderIndependentSet(t *testing.T) {
	canonical := pinnedHandshakeResponseScopes(t)
	negotiated := append(append([]ServiceScope(nil), canonical[2:]...), canonical[:2]...)
	if slices.Equal(negotiated, requiredControlScopes()) {
		t.Fatal("test fixture did not differ from the service request order")
	}
	clientConnection, serverConnection := net.Pipe()
	t.Cleanup(func() { _ = clientConnection.Close(); _ = serverConnection.Close() })
	serverResult := make(chan error, 1)
	go func() {
		var request authenticatedHandshakeRequest
		if err := readControlFrame(serverConnection, &request); err != nil {
			serverResult <- err
			return
		}
		serverResult <- writeControlFrame(serverConnection, validHandshakeResponse(func(response *HandshakeResponse) {
			response.ID = request.ID
			response.Result.ServiceInstanceID = request.Params.ServiceInstanceID
			response.Result.ActiveScopes = negotiated
		}))
	}()
	request := controlHandshake(ControlConnectionConfig{
		ServiceInstanceID: "service-instance_scope_set", HandshakeOperationID: "operation_handshake_scope_set",
	})
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := newControlSession(clientConnection, controlTestBearer, controlHandlerStub{}, time.Second).handshake(ctx, request); err != nil {
		t.Fatalf("handshake(reordered pinned scopes) error = %v", err)
	}
	if err := <-serverResult; err != nil {
		t.Fatalf("serve reordered handshake response: %v", err)
	}
	withDuplicate := append(append([]ServiceScope(nil), negotiated...), negotiated[0])
	if !sameControlScopes(withDuplicate) {
		t.Fatal("complete scope set with a duplicate was rejected")
	}
	if sameControlScopes(negotiated[:len(negotiated)-1]) {
		t.Fatal("genuinely missing scope was accepted")
	}
	if sameControlScopes(append(append([]ServiceScope(nil), negotiated...), ServiceScope("unexpected"))) {
		t.Fatal("unexpected granted scope was accepted")
	}
}

func pinnedHandshakeResponseScopes(t *testing.T) []ServiceScope {
	t.Helper()
	contents, err := os.ReadFile(filepath.Join("..", "..", "protocol", "comis", "fixtures", "valid.json"))
	if err != nil {
		t.Fatalf("read pinned valid fixture: %v", err)
	}
	var fixture struct {
		Steps []struct {
			Target  string `json:"target"`
			Payload struct {
				Result struct {
					ActiveScopes []ServiceScope `json:"activeScopes"`
				} `json:"result"`
			} `json:"payload"`
		} `json:"steps"`
	}
	if err := json.Unmarshal(contents, &fixture); err != nil {
		t.Fatalf("decode pinned valid fixture: %v", err)
	}
	for _, step := range fixture.Steps {
		if step.Target == "handshake-response" && len(step.Payload.Result.ActiveScopes) > 0 {
			return step.Payload.Result.ActiveScopes
		}
	}
	t.Fatal("pinned valid fixture has no handshake response scopes")
	return nil
}

func TestControlSessionDispatchesAbandonAndFailsClosed(t *testing.T) {
	valid := AbandonRequest{
		JSONRPC: JSONRPCVersion, ID: "operation_abandon_a", Method: MethodManagedRunsAbandon,
		Params: AbandonRequestParams{
			OperationID: "operation_abandon_a", ExternalRunRef: "task-0001",
			RegistrationNonce: "registration-nonce_a", Reason: AbandonReasonOwnerCancelled,
			Disposition: AbandonDispositionPreserve,
		},
	}
	t.Run("success", func(t *testing.T) {
		response := dispatchControlTestFrame(t, controlHandlerStub{}, authenticatedAbandonRequest{
			AbandonRequest: valid, Bearer: controlTestBearer,
		})
		var abandoned AbandonResponse
		if err := json.Unmarshal(response, &abandoned); err != nil {
			t.Fatal(err)
		}
		if abandoned.Result != abandonResult(valid.Params) {
			t.Fatalf("abandon response = %#v", abandoned)
		}
	})
	t.Run("altered envelope operation", func(t *testing.T) {
		altered := valid
		altered.ID = "operation_envelope_other"
		response := dispatchControlTestFrame(t, controlHandlerStub{}, authenticatedAbandonRequest{
			AbandonRequest: altered, Bearer: controlTestBearer,
		})
		assertControlFailure(t, response, ErrorKindInvalidRequest)
	})
	t.Run("handler rejection normalized", func(t *testing.T) {
		handler := controlHandlerStub{abandon: func(context.Context, AbandonRequestParams) (AbandonResponseResult, error) {
			return AbandonResponseResult{}, RPCError{Code: 1, Kind: ErrorKindPreconditionFailed, Message: "preparation is already active", Retryable: true}
		}}
		response := dispatchControlTestFrame(t, handler, authenticatedAbandonRequest{
			AbandonRequest: valid, Bearer: controlTestBearer,
		})
		assertControlFailure(t, response, ErrorKindPreconditionFailed)
	})
	t.Run("private handler error redacted", func(t *testing.T) {
		handler := controlHandlerStub{abandon: func(context.Context, AbandonRequestParams) (AbandonResponseResult, error) {
			return AbandonResponseResult{}, errors.New("private durable store detail")
		}}
		response := dispatchControlTestFrame(t, handler, authenticatedAbandonRequest{
			AbandonRequest: valid, Bearer: controlTestBearer,
		})
		assertControlFailure(t, response, ErrorKindInternalError)
		if strings.Contains(string(response), "private") {
			t.Fatal("handler detail leaked into wire response")
		}
	})
	t.Run("unknown method", func(t *testing.T) {
		response := dispatchControlTestFrame(t, controlHandlerStub{}, map[string]any{
			"jsonrpc": JSONRPCVersion, "id": "operation_unknown_a", "method": "managedRuns.delete", "params": map[string]any{}, "bearer": controlTestBearer,
		})
		assertControlFailure(t, response, ErrorKindMethodNotFound)
	})
}

func TestControlSessionDispatchesAuthenticatedTerminalEvents(t *testing.T) {
	valid := TerminalEventRequest{
		JSONRPC: JSONRPCVersion, ID: "operation_terminal_running", Method: MethodManagedRunsTerminalEvent,
		Params: TerminalEventRequestParams{
			OperationID: "operation_terminal_running", ManagedRunID: "managed-run_scope",
			WorkspaceLeaseID: "workspace-lease_scope", TerminalSessionID: "terminal-session_scope",
			Transition: CapabilityTerminalTransitionRunning,
		},
	}
	t.Run("success", func(t *testing.T) {
		response := dispatchControlTestFrame(t, controlHandlerStub{}, map[string]any{
			"jsonrpc": valid.JSONRPC, "id": valid.ID, "method": valid.Method,
			"params": valid.Params, "bearer": controlTestBearer,
		})
		var acknowledged TerminalEventResponse
		if err := json.Unmarshal(response, &acknowledged); err != nil {
			t.Fatal(err)
		}
		if acknowledged.Result.ManagedRunID != valid.Params.ManagedRunID ||
			acknowledged.Result.TerminalSessionID != valid.Params.TerminalSessionID ||
			acknowledged.Result.Transition != valid.Params.Transition {
			t.Fatalf("terminal event response = %#v", acknowledged)
		}
	})
	for _, test := range []struct {
		name  string
		frame map[string]any
		kind  ErrorKind
	}{
		{name: "credential", frame: map[string]any{"jsonrpc": valid.JSONRPC, "id": valid.ID, "method": valid.Method, "params": valid.Params, "bearer": strings.Repeat("x", len(controlTestBearer))}, kind: ErrorKindUnauthorizedInstance},
		{name: "envelope", frame: map[string]any{"jsonrpc": valid.JSONRPC, "id": "operation_terminal_other", "method": valid.Method, "params": valid.Params, "bearer": controlTestBearer}, kind: ErrorKindInvalidRequest},
		{name: "unknown field", frame: map[string]any{"jsonrpc": valid.JSONRPC, "id": valid.ID, "method": valid.Method, "params": valid.Params, "bearer": controlTestBearer, "content": "must not cross"}, kind: ErrorKindInvalidRequest},
	} {
		t.Run(test.name, func(t *testing.T) {
			assertControlFailure(t, dispatchControlTestFrame(t, controlHandlerStub{}, test.frame), test.kind)
		})
	}
	handler := controlHandlerStub{terminal: func(context.Context, TerminalEventRequestParams) (TerminalEventResponseResult, error) {
		return TerminalEventResponseResult{}, RPCError{Kind: ErrorKindPreconditionFailed, Message: "terminal binding differs"}
	}}
	assertControlFailure(t, dispatchControlTestFrame(t, handler, map[string]any{
		"jsonrpc": valid.JSONRPC, "id": valid.ID, "method": valid.Method,
		"params": valid.Params, "bearer": controlTestBearer,
	}), ErrorKindPreconditionFailed)
}

func abandonResult(params AbandonRequestParams) AbandonResponseResult {
	return AbandonResponseResult{
		Disposition: params.Disposition, ExternalRunRef: params.ExternalRunRef,
		State:              ManagedRunStateAbandoned,
		TerminalTransition: AbandonTerminalTransitionUnboundPreparationAbandoned,
	}
}

func TestControlSessionDispatchesActivationFailuresWithoutBroadeningScope(t *testing.T) {
	valid := ActivateRequest{
		JSONRPC: JSONRPCVersion, ID: "operation_activate_scope", Method: MethodManagedRunsActivate,
		Params: ActivateRequestParams{
			OperationID: "operation_activate_scope", ManagedRunID: "managed-run_scope",
			ExternalRunRef: "task-0001", RegistrationNonce: "registration-nonce_scope",
		},
	}
	t.Run("altered envelope operation", func(t *testing.T) {
		altered := valid
		altered.ID = "operation_activate_other"
		response := dispatchControlTestFrame(t, controlHandlerStub{}, authenticatedActivateRequest{
			ActivateRequest: altered, Bearer: controlTestBearer,
		})
		assertControlFailure(t, response, ErrorKindInvalidRequest)
	})
	t.Run("invalid private fields", func(t *testing.T) {
		altered := valid
		altered.Params.RegistrationNonce = "short"
		response := dispatchControlTestFrame(t, controlHandlerStub{}, authenticatedActivateRequest{
			ActivateRequest: altered, Bearer: controlTestBearer,
		})
		assertControlFailure(t, response, ErrorKindInvalidParams)
	})
	t.Run("durable replay conflict", func(t *testing.T) {
		handler := controlHandlerStub{activate: func(context.Context, ActivateRequestParams) (ActivateResponseResult, error) {
			return ActivateResponseResult{}, RPCError{Kind: ErrorKindReplayConflict, Message: "altered durable replay"}
		}}
		response := dispatchControlTestFrame(t, handler, authenticatedActivateRequest{
			ActivateRequest: valid, Bearer: controlTestBearer,
		})
		assertControlFailure(t, response, ErrorKindReplayConflict)
	})
	t.Run("unknown private field", func(t *testing.T) {
		response := dispatchControlTestFrame(t, controlHandlerStub{}, map[string]any{
			"jsonrpc": JSONRPCVersion, "id": valid.ID, "method": valid.Method,
			"params": valid.Params, "bearer": controlTestBearer, "managedRunId": "forged-run",
		})
		assertControlFailure(t, response, ErrorKindInvalidRequest)
	})
}

func TestControlConnectionReportRejectsInvalidUncertainAndMismatchedOutcomes(t *testing.T) {
	connection := &ControlConnection{
		config:  ControlConnectionConfig{Credential: controlTestBearer, RequestTimeout: 50 * time.Millisecond},
		changed: make(chan struct{}),
	}
	valid := ReportRequestParams{
		OperationID: "operation_report_scope", ManagedRunID: "managed-run_scope",
		ServiceReportID: "service-report_scope", Kind: ReportKindProgress, Summary: "bounded progress",
	}
	if _, err := connection.Report(nilControlContext(), valid); err == nil {
		t.Fatal("Report(nil) error = nil")
	}
	invalid := valid
	invalid.ManagedRunID = ""
	if _, err := connection.Report(context.Background(), invalid); err == nil {
		t.Fatal("Report(invalid) error = nil")
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := connection.Report(cancelled, valid); !errors.Is(err, context.Canceled) {
		t.Fatalf("Report(cancelled) error = %v", err)
	}

	service, host := net.Pipe()
	session := newControlSession(service, controlTestBearer, controlHandlerStub{}, time.Second)
	connection.config.RequestTimeout = time.Second
	connection.publish(session)
	serveContext, stop := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- session.serve(serveContext) }()
	t.Cleanup(func() {
		stop()
		_ = host.Close()
		<-done
	})
	hostDone := make(chan error, 1)
	go func() {
		var request authenticatedReportRequest
		if err := readControlFrame(host, &request); err != nil {
			hostDone <- err
			return
		}
		hostDone <- writeControlFrame(host, ReportResponse{
			JSONRPC: JSONRPCVersion, ID: request.ID,
			Result: ReportResponseResult{
				AcceptedSequence: 1, ManagedRunID: "managed-run_forged",
				ServiceReportID: request.Params.ServiceReportID, RetainedUntilMs: 1_800_000_000_000,
			},
		})
	}()
	if _, err := connection.Report(context.Background(), valid); err == nil || !strings.Contains(err.Error(), "identity differs") {
		t.Fatalf("Report(mismatched acknowledgement) error = %v", err)
	}
	if err := <-hostDone; err != nil {
		t.Fatal(err)
	}
}

func TestControlConnectionRunAndHandshakeCancellationBoundaries(t *testing.T) {
	connection := &ControlConnection{config: ControlConnectionConfig{MinimumBackoff: time.Millisecond, MaximumBackoff: time.Second}}
	if err := connection.Run(nilControlContext()); err == nil {
		t.Fatal("Run(nil) error = nil")
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if err := connection.Run(cancelled); !errors.Is(err, context.Canceled) {
		t.Fatalf("Run(cancelled) error = %v", err)
	}
	if err := waitControlBackoff(cancelled, time.Second); !errors.Is(err, context.Canceled) {
		t.Fatalf("waitControlBackoff(cancelled) error = %v", err)
	}

	service, host := net.Pipe()
	session := newControlSession(service, controlTestBearer, controlHandlerStub{}, time.Second)
	t.Cleanup(func() {
		_ = session.close()
		_ = host.Close()
	})
	handshake := controlHandshake(ControlConnectionConfig{
		ServiceInstanceID: "service-instance_a", HandshakeOperationID: "operation_handshake_scope",
	})
	hostDone := make(chan error, 1)
	go func() {
		var request authenticatedHandshakeRequest
		if err := readControlFrame(host, &request); err != nil {
			hostDone <- err
			return
		}
		hostDone <- writeControlFrame(host, validHandshakeResponse(func(response *HandshakeResponse) {
			response.ID = request.ID
			response.Result.ServiceInstanceID = request.Params.ServiceInstanceID
			response.Result.ActiveScopes = []ServiceScope{ServiceScopeReport, ServiceScopeHealth}
		}))
	}()
	if err := session.handshake(context.Background(), handshake); err == nil {
		t.Fatal("handshake(authority drift) error = nil")
	}
	if err := <-hostDone; err != nil {
		t.Fatal(err)
	}
}

func TestControlConnectionConnectFailsClosedAtSocketAndHandshakeBoundaries(t *testing.T) {
	base := ControlConnectionConfig{
		Credential: controlTestBearer, ServiceInstanceID: "service-instance_a",
		HandshakeOperationID: "operation_handshake_boundary", Handler: controlHandlerStub{},
		RequestTimeout: time.Second, MinimumBackoff: time.Millisecond, MaximumBackoff: time.Second,
	}
	missing := &ControlConnection{config: base, changed: make(chan struct{})}
	missing.config.SocketPath = filepath.Join("/private/tmp", "devcrew-missing-control.sock")
	if _, err := missing.connect(context.Background()); err == nil {
		t.Fatal("connect(missing socket) error = nil")
	}

	directory, err := os.MkdirTemp("/private/tmp", "dc-stale-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(directory) })
	stalePath := filepath.Join(directory, "stale.sock")
	address, err := net.ResolveUnixAddr("unix", stalePath)
	if err != nil {
		t.Fatal(err)
	}
	stale, err := net.ListenUnix("unix", address)
	if err != nil {
		t.Fatal(err)
	}
	stale.SetUnlinkOnClose(false)
	if err := stale.Close(); err != nil {
		t.Fatal(err)
	}
	staleConnection := &ControlConnection{config: base, changed: make(chan struct{})}
	staleConnection.config.SocketPath = stalePath
	if _, err := staleConnection.connect(context.Background()); err == nil {
		t.Fatal("connect(stale socket) error = nil")
	}

	socketPath, listener := controlTestListener(t)
	boundary := &ControlConnection{config: base, changed: make(chan struct{})}
	boundary.config.SocketPath = socketPath
	hostDone := make(chan error, 1)
	go func() {
		peer, err := listener.AcceptUnix()
		if err != nil {
			hostDone <- err
			return
		}
		defer peer.Close()
		var request authenticatedHandshakeRequest
		if err := readControlFrame(peer, &request); err != nil {
			hostDone <- err
			return
		}
		hostDone <- writeControlFrame(peer, validHandshakeResponse(func(response *HandshakeResponse) {
			response.ID = request.ID
			response.Result.ServiceInstanceID = request.Params.ServiceInstanceID
			response.Result.ProtocolID = "comis.capability-service/2"
		}))
	}()
	if _, err := boundary.connect(context.Background()); err == nil {
		t.Fatal("connect(protocol drift) error = nil")
	}
	if err := <-hostDone; err != nil {
		t.Fatal(err)
	}
}

func TestControlSessionCallReturnsSessionFailureWithoutRetry(t *testing.T) {
	service, host := net.Pipe()
	session := newControlSession(service, controlTestBearer, controlHandlerStub{}, time.Second)
	request := authenticatedReportRequest{
		ReportRequest: ReportRequest{
			JSONRPC: JSONRPCVersion, ID: "operation_disconnect", Method: MethodManagedRunsReport,
			Params: ReportRequestParams{
				OperationID: "operation_disconnect", ManagedRunID: "managed-run_disconnect",
				ServiceReportID: "service-report_disconnect", Kind: ReportKindProgress, Summary: "progress",
			},
		},
		Bearer: controlTestBearer,
	}
	hostReady := make(chan struct{})
	go func() {
		var received authenticatedReportRequest
		_ = readControlFrame(host, &received)
		close(hostReady)
	}()
	done := make(chan error, 1)
	go func() {
		var response ReportResponse
		done <- session.call(context.Background(), request, request.ID, &response)
	}()
	<-hostReady
	session.fail(errors.New("fixture transport disconnected"))
	if err := <-done; err == nil || !strings.Contains(err.Error(), "disconnected") {
		t.Fatalf("call(disconnect) error = %v", err)
	}
	_ = host.Close()
}

func TestControlSessionResponseValidationAndEncodingFailures(t *testing.T) {
	service, host := net.Pipe()
	session := newControlSession(service, controlTestBearer, controlHandlerStub{}, time.Second)
	t.Cleanup(func() {
		_ = session.close()
		_ = host.Close()
	})
	if err := session.writeValidated(PayloadActivateResponse, ActivateResponse{}); err == nil {
		t.Fatal("writeValidated(invalid response) error = nil")
	}
	if err := session.writeUnlocked(strings.Repeat("x", MaxLineBytes)); err == nil {
		t.Fatal("writeUnlocked(oversize) error = nil")
	}
	if err := session.resolveResponse(controlFrameHeader{ID: json.RawMessage("null")}, nil); err == nil {
		t.Fatal("resolveResponse(null ID) error = nil")
	}
	if err := session.resolveResponse(controlFrameHeader{ID: json.RawMessage("{")}, nil); err == nil {
		t.Fatal("resolveResponse(malformed ID) error = nil")
	}
	longFailure := handlerWireFailure(RPCError{Kind: ErrorKindRateLimited, Message: strings.Repeat("x", 1025)})
	if longFailure.Code != -32016 || longFailure.Message != "control request rejected" || !longFailure.Retryable {
		t.Fatalf("handlerWireFailure(long) = %#v", longFailure)
	}
}

func TestControlSessionRejectsMalformedAndExcessFrames(t *testing.T) {
	service, host := net.Pipe()
	session := newControlSession(service, controlTestBearer, controlHandlerStub{}, time.Second)
	t.Cleanup(func() {
		_ = session.close()
		_ = host.Close()
	})
	if err := session.route(context.Background(), []byte(`{"jsonrpc":"2.0","jsonrpc":"2.0"}`)); err == nil {
		t.Fatal("route(duplicate JSON name) error = nil")
	}
	if err := session.route(context.Background(), []byte(`{"jsonrpc":"1.0","id":"operation_bad"}`)); err == nil {
		t.Fatal("route(protocol drift) error = nil")
	}
	for range MaxInFlightRequests {
		session.limit <- struct{}{}
	}
	request := authenticatedActivateRequest{
		ActivateRequest: ActivateRequest{
			JSONRPC: JSONRPCVersion, ID: "operation_limit", Method: MethodManagedRunsActivate,
			Params: ActivateRequestParams{
				OperationID: "operation_limit", ManagedRunID: "managed-run_limit",
				ExternalRunRef: "task-0001", RegistrationNonce: "registration-nonce_limit",
			},
		},
		Bearer: controlTestBearer,
	}
	encoded, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	if err := session.route(context.Background(), encoded); err == nil {
		t.Fatal("route(excess request) error = nil")
	}
	reader := bufio.NewReader(strings.NewReader(strings.Repeat("x", MaxLineBytes+1)))
	if _, err := readControlLine(reader); err == nil {
		t.Fatal("readControlLine(oversize) error = nil")
	}
}

func TestControlSessionCallDeadlineRemovesUncertainOperation(t *testing.T) {
	service, host := net.Pipe()
	session := newControlSession(service, controlTestBearer, controlHandlerStub{}, 20*time.Millisecond)
	t.Cleanup(func() {
		_ = session.close()
		_ = host.Close()
	})
	request := authenticatedReportRequest{
		ReportRequest: ReportRequest{
			JSONRPC: JSONRPCVersion, ID: "operation_timeout_a", Method: MethodManagedRunsReport,
			Params: ReportRequestParams{
				OperationID: "operation_timeout_a", ManagedRunID: "managed-run_a",
				ServiceReportID: "service-report_a", Kind: ReportKindProgress, Summary: "progress",
			},
		},
		Bearer: controlTestBearer,
	}
	read := make(chan error, 1)
	go func() {
		var received authenticatedReportRequest
		read <- readControlFrame(host, &received)
	}()
	var response ReportResponse
	err := session.call(context.Background(), request, request.ID, &response)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("call() error = %v, want deadline", err)
	}
	if err := <-read; err != nil {
		t.Fatal(err)
	}
	session.mu.Lock()
	pending := len(session.pending)
	session.mu.Unlock()
	if pending != 0 {
		t.Fatalf("pending operations = %d, want 0", pending)
	}
}

func TestControlSessionRejectsDuplicateResponseAndPendingOperation(t *testing.T) {
	service, host := net.Pipe()
	session := newControlSession(service, controlTestBearer, controlHandlerStub{}, time.Second)
	t.Cleanup(func() {
		_ = session.close()
		_ = host.Close()
	})
	encoded, err := json.Marshal(ReportResponse{
		JSONRPC: JSONRPCVersion, ID: "operation_orphan_a",
		Result: ReportResponseResult{
			AcceptedSequence: 1, ManagedRunID: "managed-run_a",
			ServiceReportID: "service-report_a", RetainedUntilMs: 1_800_000_000_000,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := session.route(context.Background(), encoded); err == nil {
		t.Fatal("route(orphan response) error = nil")
	}
	session.pending["operation_duplicate_a"] = make(chan controlCallResult, 1)
	if err := session.call(context.Background(), ReportRequest{}, "operation_duplicate_a", &ReportResponse{}); err == nil {
		t.Fatal("call(duplicate) error = nil")
	}

	session.fail(errors.New("fixture session failed"))
	if !strings.Contains(session.failure().Error(), "fixture") {
		t.Fatalf("failure() = %v", session.failure())
	}
}

func dispatchControlTestFrame(t *testing.T, handler ControlHandler, frame any) []byte {
	t.Helper()
	service, host := net.Pipe()
	session := newControlSession(service, controlTestBearer, handler, time.Second)
	t.Cleanup(func() {
		_ = session.close()
		_ = host.Close()
	})
	encoded, err := json.Marshal(frame)
	if err != nil {
		t.Fatal(err)
	}
	dispatched := make(chan error, 1)
	go func() {
		var header controlFrameHeader
		if err := json.Unmarshal(encoded, &header); err != nil || header.Method == nil {
			dispatched <- errors.New("test frame has no method")
			return
		}
		dispatched <- session.dispatch(context.Background(), *header.Method, encoded)
	}()
	line := make([]byte, 0, 1024)
	for {
		var next [1]byte
		if _, err := host.Read(next[:]); err != nil {
			t.Fatal(err)
		}
		if next[0] == '\n' {
			break
		}
		line = append(line, next[0])
	}
	if err := <-dispatched; err != nil {
		t.Fatalf("dispatch() error = %v", err)
	}
	return line
}

func assertControlFailure(t *testing.T, response []byte, want ErrorKind) {
	t.Helper()
	if err := ValidatePayload(PayloadErrorResponse, response); err != nil {
		t.Fatalf("error response validation = %v: %s", err, response)
	}
	var failure ErrorResponse
	if err := json.Unmarshal(response, &failure); err != nil {
		t.Fatal(err)
	}
	if failure.Error.Kind != want {
		t.Fatalf("error kind = %q, want %q", failure.Error.Kind, want)
	}
}

func nilControlContext() context.Context { return nil }
