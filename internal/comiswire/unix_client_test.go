package comiswire

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestNewUnixClientRejectsUnsafeEndpointConfiguration(t *testing.T) {
	canonicalDirectory := newCanonicalTempDirectory(t)
	for _, test := range []struct {
		name    string
		path    string
		timeout time.Duration
	}{
		{name: "relative path", path: "capability.sock", timeout: time.Second},
		{name: "noncanonical path", path: filepath.Join(t.TempDir(), "nested", "..", "capability.sock"), timeout: time.Second},
		{name: "oversized path", path: filepath.Join(string(filepath.Separator), strings.Repeat("x", 104)), timeout: time.Second},
		{name: "nonpositive timeout", path: filepath.Join(canonicalDirectory, "capability.sock"), timeout: 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := NewUnixClient(test.path, test.timeout); err == nil {
				t.Fatal("expected unsafe endpoint rejection")
			}
		})
	}
	if _, err := NewUnixClient(filepath.Join(canonicalDirectory, "missing", "capability.sock"), time.Second); err == nil {
		t.Fatal("expected missing socket parent rejection")
	}
	realParent := newCanonicalTempDirectory(t)
	symlinkParent := filepath.Join(canonicalDirectory, "linked")
	if err := os.Symlink(realParent, symlinkParent); err != nil {
		t.Fatalf("create socket parent symlink: %v", err)
	}
	if _, err := NewUnixClient(filepath.Join(symlinkParent, "capability.sock"), time.Second); err == nil {
		t.Fatal("expected symlinked socket parent rejection")
	}
}

func TestUnixClientUsesFreshOwnerOnlySocketConnections(t *testing.T) {
	server := startWireServer(t, 2, func(request string) string {
		operationID := requestID(request)
		return healthResponse(operationID)
	})
	client, err := NewUnixClient(server.path, time.Second)
	if err != nil {
		t.Fatalf("create Unix client: %v", err)
	}
	for _, operationID := range []OperationID{"operation_health_a", "operation_health_b"} {
		result, err := client.Health(context.Background(), HealthRequestParams{
			BundleDigest: BundleDigest, OperationID: operationID, ProtocolID: ProtocolID, ServiceInstanceID: "service-instance_a",
		})
		if err != nil {
			t.Fatalf("health over Unix socket: %v", err)
		}
		if result.Status != HealthStatusHealthy {
			t.Fatalf("health status = %q", result.Status)
		}
	}
	server.wait(t)
	if server.connections != 2 {
		t.Fatalf("connection count = %d, want 2", server.connections)
	}
}

func TestUnixClientReturnsClosedRemoteError(t *testing.T) {
	server := startWireServer(t, 1, func(request string) string {
		return fmt.Sprintf(`{"jsonrpc":"2.0","id":%q,"error":{"code":-32018,"kind":"precondition_failed","retryable":false,"message":"transition unavailable","hint":"refresh state"}}`, requestID(request))
	})
	client, err := NewUnixClient(server.path, time.Second)
	if err != nil {
		t.Fatalf("create Unix client: %v", err)
	}
	_, callErr := client.Health(context.Background(), validHealthParams("operation_error"))
	var remote RPCError
	if !errors.As(callErr, &remote) || remote.Kind != ErrorKindPreconditionFailed || remote.Retryable {
		t.Fatalf("remote error = %#v, %v", remote, callErr)
	}
	server.wait(t)
}

func TestUnixClientRejectsUntrustedSocketAndResponseFraming(t *testing.T) {
	t.Run("group-accessible socket", func(t *testing.T) {
		server := startWireServer(t, 0, nil)
		if err := os.Chmod(server.path, 0o660); err != nil {
			t.Fatalf("change socket mode: %v", err)
		}
		client, err := NewUnixClient(server.path, time.Second)
		if err != nil {
			t.Fatalf("create Unix client: %v", err)
		}
		if _, err := client.Health(context.Background(), validHealthParams("operation_mode")); err == nil {
			t.Fatal("expected group-accessible socket rejection")
		}
	})

	responses := []struct {
		name     string
		response func(string) string
	}{
		{name: "unknown response field", response: func(id string) string { return strings.TrimSuffix(healthResponse(id), "}") + `,"unknown":true}` }},
		{name: "duplicate response field", response: func(id string) string { return fmt.Sprintf(`{"jsonrpc":"2.0","id":%q,"id":%q,"result":{}}`, id, id) }},
		{name: "trailing response value", response: func(string) string { return `{} {}` }},
		{name: "mismatched error ID", response: func(string) string {
			return `{"jsonrpc":"2.0","id":"operation_other","error":{"code":-32018,"kind":"precondition_failed","retryable":false,"message":"transition unavailable"}}`
		}},
		{name: "oversized response", response: func(string) string { return `{"padding":"` + strings.Repeat("x", MaxResponseBytes) + `"}` }},
	}
	for _, test := range responses {
		t.Run(test.name, func(t *testing.T) {
			server := startWireServer(t, 1, func(request string) string { return test.response(requestID(request)) })
			client, err := NewUnixClient(server.path, time.Second)
			if err != nil {
				t.Fatalf("create Unix client: %v", err)
			}
			if _, err := client.Health(context.Background(), validHealthParams("operation_invalid_response")); err == nil {
				t.Fatal("expected untrusted response rejection")
			}
			server.wait(t)
		})
	}
}

func TestUnixClientAppliesDeadlineToBlockedResponse(t *testing.T) {
	release := make(chan struct{})
	server := startWireServer(t, 1, func(request string) string {
		<-release
		return healthResponse(requestID(request))
	})
	client, err := NewUnixClient(server.path, 50*time.Millisecond)
	if err != nil {
		t.Fatalf("create Unix client: %v", err)
	}
	_, callErr := client.Health(context.Background(), validHealthParams("operation_timeout"))
	close(release)
	if callErr == nil {
		t.Fatal("expected blocked response deadline")
	}
}

func TestUnixRoundTripperRejectsUnsupportedRequestsAndSocketKinds(t *testing.T) {
	transport := &unixRoundTripper{socketPath: filepath.Join(newCanonicalTempDirectory(t), "missing.sock"), timeout: time.Second}
	if err := transport.roundTrip(missingContext(), HealthRequest{}, &HealthResponse{}); err == nil {
		t.Fatal("expected nil context rejection")
	}
	if err := transport.roundTrip(context.Background(), struct{}{}, &HealthResponse{}); err == nil {
		t.Fatal("expected unsupported request rejection")
	}
	if _, err := outboundOperationID(HandshakeRequest{ID: "operation_handshake"}); err != nil {
		t.Fatalf("handshake operation ID: %v", err)
	}
	if _, err := outboundOperationID(ReportRequest{ID: "operation_report"}); err != nil {
		t.Fatalf("report operation ID: %v", err)
	}

	regular := filepath.Join(newCanonicalTempDirectory(t), "not-a-socket")
	if err := os.WriteFile(regular, []byte("file"), 0o600); err != nil {
		t.Fatalf("write regular endpoint: %v", err)
	}
	if _, err := inspectOwnerOnlySocket(regular); err == nil {
		t.Fatal("expected regular endpoint rejection")
	}
	if _, err := inspectOwnerOnlySocket(filepath.Join(newCanonicalTempDirectory(t), "missing")); err == nil {
		t.Fatal("expected missing endpoint rejection")
	}
}

func TestDecodeWireResponseRejectsEveryEnvelopeContradiction(t *testing.T) {
	validResult := healthResponse("operation_response")
	tests := []struct {
		name     string
		response string
		target   any
	}{
		{name: "malformed envelope", response: "{", target: &HealthResponse{}},
		{name: "wrong JSON-RPC version", response: strings.Replace(validResult, `"2.0"`, `"1.0"`, 1), target: &HealthResponse{}},
		{name: "numeric operation ID", response: strings.Replace(validResult, `"operation_response"`, `1`, 1), target: &HealthResponse{}},
		{name: "missing result and error", response: `{"jsonrpc":"2.0","id":"operation_response"}`, target: &HealthResponse{}},
		{name: "result and error both present", response: `{"jsonrpc":"2.0","id":"operation_response","result":{},"error":{}}`, target: &HealthResponse{}},
		{name: "invalid closed remote error", response: `{"jsonrpc":"2.0","id":"operation_response","error":{"code":-32018,"kind":"invalid_request","retryable":false,"message":"invalid"}}`, target: &HealthResponse{}},
		{name: "result target cannot decode", response: validResult, target: make(chan int)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := decodeWireResponse([]byte(test.response), "operation_response", test.target); err == nil {
				t.Fatal("expected contradictory response rejection")
			}
		})
	}
	if err := contextOrTransportError(context.Background(), "read", errors.New("cause")); err == nil || !strings.Contains(err.Error(), "cause") {
		t.Fatalf("transport cause was not preserved: %v", err)
	}
}

type wireServer struct {
	path        string
	listener    *net.UnixListener
	done        chan struct{}
	err         error
	connections int
	mutex       sync.Mutex
}

func startWireServer(t *testing.T, calls int, respond func(string) string) *wireServer {
	t.Helper()
	directory := newCanonicalTempDirectory(t)
	path := filepath.Join(directory, "capability.sock")
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		t.Fatalf("listen on Unix socket: %v", err)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		_ = listener.Close()
		t.Fatalf("set Unix socket mode: %v", err)
	}
	server := &wireServer{path: path, listener: listener, done: make(chan struct{})}
	go func() {
		defer close(server.done)
		for range calls {
			connection, acceptErr := listener.AcceptUnix()
			if acceptErr != nil {
				server.setError(acceptErr)
				return
			}
			server.mutex.Lock()
			server.connections++
			server.mutex.Unlock()
			request, readErr := bufio.NewReader(connection).ReadString('\n')
			if readErr == nil {
				_, _ = connection.Write([]byte(respond(strings.TrimSuffix(request, "\n")) + "\n"))
			}
			closeErr := connection.Close()
			if readErr != nil {
				server.setError(readErr)
				return
			}
			if closeErr != nil {
				server.setError(closeErr)
				return
			}
		}
	}()
	t.Cleanup(func() {
		_ = listener.Close()
		select {
		case <-server.done:
		case <-time.After(time.Second):
			t.Error("Unix wire server did not stop")
		}
	})
	return server
}

func newCanonicalTempDirectory(t *testing.T) string {
	t.Helper()
	directory, err := os.MkdirTemp("", "cw-")
	if err != nil {
		t.Fatalf("create short Unix socket directory: %v", err)
	}
	directory, err = filepath.EvalSymlinks(directory)
	if err != nil {
		t.Fatalf("canonicalize Unix socket directory: %v", err)
	}
	t.Cleanup(func() {
		if err := os.RemoveAll(directory); err != nil {
			t.Errorf("remove Unix socket directory: %v", err)
		}
	})
	return directory
}

func (server *wireServer) setError(err error) {
	server.mutex.Lock()
	defer server.mutex.Unlock()
	server.err = err
}

func (server *wireServer) wait(t *testing.T) {
	t.Helper()
	select {
	case <-server.done:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for Unix wire server")
	}
	server.mutex.Lock()
	defer server.mutex.Unlock()
	if server.err != nil {
		t.Fatalf("Unix wire server: %v", server.err)
	}
}

func validHealthParams(operationID OperationID) HealthRequestParams {
	return HealthRequestParams{BundleDigest: BundleDigest, OperationID: operationID, ProtocolID: ProtocolID, ServiceInstanceID: "service-instance_a"}
}

func healthResponse(operationID string) string {
	return fmt.Sprintf(`{"jsonrpc":"2.0","id":%q,"result":{"protocolId":%q,"bundleDigest":%q,"serviceInstanceId":"service-instance_a","status":"healthy","observedAtMs":1,"reasonCodes":[]}}`, operationID, ProtocolID, BundleDigest)
}

func requestID(request string) string {
	value, err := decodeStrictValue([]byte(request))
	if err != nil {
		return ""
	}
	object, ok := value.(map[string]any)
	if !ok {
		return ""
	}
	id, ok := object["id"].(string)
	if !ok {
		return ""
	}
	return id
}
