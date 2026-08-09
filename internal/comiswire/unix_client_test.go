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
	for _, test := range []struct {
		name    string
		path    string
		timeout time.Duration
	}{
		{name: "relative path", path: "capability.sock", timeout: time.Second},
		{name: "noncanonical path", path: filepath.Join(t.TempDir(), "nested", "..", "capability.sock"), timeout: time.Second},
		{name: "oversized path", path: filepath.Join(string(filepath.Separator), strings.Repeat("x", 104)), timeout: time.Second},
		{name: "nonpositive timeout", path: filepath.Join(t.TempDir(), "capability.sock"), timeout: 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := NewUnixClient(test.path, test.timeout); err == nil {
				t.Fatal("expected unsafe endpoint rejection")
			}
		})
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
	path := filepath.Join(t.TempDir(), "capability.sock")
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
