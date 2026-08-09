package localapi

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/comisai/comis-dev-crew/internal/domain"
)

func TestNewClient_RejectsUnsafeConfiguration(t *testing.T) {
	if _, err := NewClient("relative.sock", time.Second); err == nil {
		t.Fatal("NewClient(relative) error = nil")
	}
	if _, err := NewClient("/"+strings.Repeat("x", maximumSocketPath+1), time.Second); err == nil {
		t.Fatal("NewClient(long path) error = nil")
	}
	if _, err := NewClient(filepath.Join(canonicalTempDir(t), "api.sock"), 0); err == nil {
		t.Fatal("NewClient(zero timeout) error = nil")
	}
}

func TestClient_RejectsInvalidCallsBeforeConnecting(t *testing.T) {
	client, err := NewClient(filepath.Join(canonicalTempDir(t), "missing.sock"), time.Second)
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	//lint:ignore SA1012 The boundary test proves nil is rejected before dialing.
	if _, err := client.Diagnose(nil, "read-0001"); err == nil {
		t.Fatal("Diagnose(nil context) error = nil")
	}
	if _, err := client.Diagnose(context.Background(), "bad id"); err == nil {
		t.Fatal("Diagnose(invalid operation ID) error = nil")
	}
	if _, err := client.Diagnose(context.Background(), "read-0001"); err == nil {
		t.Fatal("Diagnose(missing service) error = nil")
	} else {
		var failure *domain.Failure
		if !errors.As(err, &failure) || failure.Code != domain.ErrorUnavailable {
			t.Fatalf("Diagnose(missing service) error = %v, want unavailable Failure", err)
		}
		if strings.Contains(err.Error(), client.socketPath) {
			t.Fatalf("Diagnose(missing service) leaked socket path: %q", err)
		}
	}
}

func TestClient_ValidatesEveryOutcomeBeforeReturningIt(t *testing.T) {
	completedResult := `{"schemaVersion":1,"capturedAtMs":1,"stateVersion":0,"completeness":"partial","serviceHealth":"healthy","comisHealth":"unavailable","checks":[]}`
	tests := []struct {
		name     string
		response string
		wantCode domain.ErrorCode
	}{
		{name: "identity mismatch", response: outcomeJSON("other-read", "completed", completedResult, "null")},
		{name: "completed without result", response: outcomeJSON("read-0001", "completed", "null", "null")},
		{name: "rejected without error", response: outcomeJSON("read-0001", "rejected", "null", "null")},
		{name: "valid rejection", response: outcomeJSON("read-0001", "rejected", "null", `{"code":"conflict","message":"request conflicts","retryable":false,"hint":"inspect current state"}`), wantCode: domain.ErrorConflict},
		{name: "invalid rejection code", response: outcomeJSON("read-0001", "rejected", "null", `{"code":"invented","message":"request failed","retryable":false,"hint":"inspect"}`)},
		{name: "accepted read", response: outcomeJSON("read-0001", "accepted", "null", "null"), wantCode: domain.ErrorUnknown},
		{name: "unknown read", response: outcomeJSON("read-0001", "unknown", "null", "null"), wantCode: domain.ErrorUnknown},
		{name: "unknown status", response: outcomeJSON("read-0001", "invented", "null", "null")},
		{name: "strict result", response: outcomeJSON("read-0001", "completed", `{"schemaVersion":1,"capturedAtMs":1,"stateVersion":0,"completeness":"partial","serviceHealth":"healthy","comisHealth":"unavailable","checks":[],"extra":true}`, "null")},
		{name: "state version mismatch", response: `{"protocolVersion":"devcrew.local.v1","operationId":"read-0001","status":"completed","stateVersion":1,"result":` + completedResult + `,"error":null}`},
		{name: "duplicate outcome field", response: `{"protocolVersion":"devcrew.local.v1","operationId":"read-0001","operationId":"read-0002","status":"completed","result":` + completedResult + `}`},
		{name: "missing newline", response: outcomeJSON("read-0001", "completed", completedResult, "null"), wantCode: ""},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			response := test.response
			if test.name != "missing newline" {
				response += "\n"
			}
			socketPath, wait := startResponseServer(t, response)
			client, err := NewClient(socketPath, time.Second)
			if err != nil {
				t.Fatalf("NewClient() error = %v", err)
			}
			_, callErr := client.Diagnose(context.Background(), "read-0001")
			wait()
			if callErr == nil {
				t.Fatal("Diagnose() error = nil, want response rejection")
			}
			if test.wantCode != "" {
				var failure *domain.Failure
				if !errors.As(callErr, &failure) || failure.Code != test.wantCode {
					t.Fatalf("Diagnose() error = %v, want Failure %q", callErr, test.wantCode)
				}
			}
		})
	}
}

func TestClient_RejectsOversizedResponse(t *testing.T) {
	socketPath, wait := startResponseServer(t, strings.Repeat("x", MaxResponseBytes+1)+"\n")
	client, err := NewClient(socketPath, time.Second)
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	if _, err := client.Diagnose(context.Background(), "read-0001"); err == nil {
		t.Fatal("Diagnose(oversized response) error = nil")
	}
	wait()
}

func TestClientAndSocketHelpers_FailClosedForUnknownOrMissingIdentity(t *testing.T) {
	if _, ok := projectedStateVersion(&struct{}{}); ok {
		t.Fatal("projectedStateVersion(unknown projection) ok = true")
	}
	if err := removeOwnedSocket("", nil); err != nil {
		t.Fatalf("removeOwnedSocket(empty) error = %v", err)
	}
	path := filepath.Join(canonicalTempDir(t), "removed.sock")
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatalf("create identity fixture: %v", err)
	}
	info, err := os.Lstat(path)
	if err != nil {
		t.Fatalf("inspect identity fixture: %v", err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatalf("remove identity fixture: %v", err)
	}
	if err := removeOwnedSocket(path, info); err != nil {
		t.Fatalf("removeOwnedSocket(already absent) error = %v", err)
	}
}

func TestCodec_RejectsNestedDuplicatesAndNonObjectPayloads(t *testing.T) {
	var decoded any
	if err := decodeStrict([]byte(`{"outer":[{"value":1},{"value":2}]}`), &decoded); err != nil {
		t.Fatalf("decodeStrict(valid nested JSON) error = %v", err)
	}
	for _, data := range []string{
		`{"outer":{"value":1,"value":2}}`,
		`{"unterminated":`,
		`{"value":1} {"value":2}`,
	} {
		if err := decodeStrict([]byte(data), &decoded); err == nil {
			t.Fatalf("decodeStrict(%q) error = nil", data)
		}
	}
	for _, data := range []string{"", "null", "[]"} {
		if err := decodeObject([]byte(data), &struct{}{}); err == nil {
			t.Fatalf("decodeObject(%q) error = nil", data)
		}
	}

	mismatched := json.NewDecoder(strings.NewReader(`[]`))
	if _, err := mismatched.Token(); err != nil {
		t.Fatalf("read opening array token: %v", err)
	}
	if err := consumeDelimiter(mismatched, '}'); err == nil {
		t.Fatal("consumeDelimiter(mismatch) error = nil")
	}
	unterminated := json.NewDecoder(strings.NewReader(`[`))
	if _, err := unterminated.Token(); err != nil {
		t.Fatalf("read unterminated opening token: %v", err)
	}
	if err := consumeDelimiter(unterminated, ']'); err == nil {
		t.Fatal("consumeDelimiter(unterminated) error = nil")
	}
	if err := walkJSONValue(json.NewDecoder(strings.NewReader(`{}`)), json.Delim('(')); err == nil {
		t.Fatal("walkJSONValue(unexpected delimiter) error = nil")
	}
	if err := requireJSONEnd(json.NewDecoder(strings.NewReader(`?`))); err == nil {
		t.Fatal("requireJSONEnd(malformed trailing data) error = nil")
	}
	if err := rejectDuplicateKeys(nil); err == nil {
		t.Fatal("rejectDuplicateKeys(empty) error = nil")
	}
	for _, malformed := range []string{`{"key"`, `{"key":`, `[?`} {
		decoder := json.NewDecoder(strings.NewReader(malformed))
		opening, err := decoder.Token()
		if err != nil {
			continue
		}
		if err := walkJSONValue(decoder, opening); err == nil {
			t.Fatalf("walkJSONValue(%q) error = nil", malformed)
		}
	}
}

func TestHandler_DefensiveConstructionAuthorizationAndErrorPaths(t *testing.T) {
	if _, err := NewHandler(HandlerConfig{Clock: time.Now}); err == nil {
		t.Fatal("NewHandler(nil queries) error = nil")
	}
	if _, err := NewHandler(HandlerConfig{Queries: &apiQueries{}}); err == nil {
		t.Fatal("NewHandler(nil clock) error = nil")
	}
	for _, test := range []struct {
		caller CallerClass
		method Method
		want   bool
	}{
		{caller: CallerOperatorCLI, method: MethodFleet, want: true},
		{caller: CallerMCPFacade, method: MethodFleet, want: true},
		{caller: CallerWorkerReport, method: MethodFleet, want: false},
		{caller: CallerComisControl, method: MethodFleet, want: false},
		{caller: CallerClass("invented"), method: MethodFleet, want: false},
	} {
		if got := methodAllowed(test.caller, test.method); got != test.want {
			t.Fatalf("methodAllowed(%q, %q) = %v, want %v", test.caller, test.method, got, test.want)
		}
	}
	if outcome := queryOutcome("read-0001", 0, make(chan int), nil); outcome.Error == nil || outcome.Error.Code != domain.ErrorInternal {
		t.Fatalf("queryOutcome(unencodable) = %#v, want internal rejection", outcome)
	}
	if outcome := outcomeFromError("read-0001", errors.New("private")); outcome.Error == nil || outcome.Error.Code != domain.ErrorInternal {
		t.Fatalf("outcomeFromError(untyped) = %#v, want internal rejection", outcome)
	}
	handler, err := NewHandler(HandlerConfig{Queries: &apiQueries{}, Clock: time.Now})
	if err != nil {
		t.Fatalf("NewHandler() error = %v", err)
	}
	outcome := handler.dispatch(context.Background(), Request{OperationID: "read-0001", Method: Method("invented"), Payload: json.RawMessage(`{}`)})
	if outcome.Error == nil || outcome.Error.Code != domain.ErrorInvalidArgument {
		t.Fatalf("dispatch(unknown) = %#v, want invalid argument", outcome)
	}
}

func outcomeJSON(operationID, status, result, wireError string) string {
	return `{"protocolVersion":"devcrew.local.v1","operationId":"` + operationID + `","status":"` + status + `","stateVersion":0,"result":` + result + `,"error":` + wireError + `}`
}

func startResponseServer(t *testing.T, response string) (string, func()) {
	t.Helper()
	socketPath := filepath.Join(canonicalTempDir(t), "response.sock")
	address, err := net.ResolveUnixAddr("unix", socketPath)
	if err != nil {
		t.Fatalf("resolve response socket: %v", err)
	}
	listener, err := net.ListenUnix("unix", address)
	if err != nil {
		t.Fatalf("listen response socket: %v", err)
	}
	done := make(chan error, 1)
	go func() {
		connection, acceptErr := listener.AcceptUnix()
		if acceptErr != nil {
			done <- acceptErr
			return
		}
		_, readErr := bufio.NewReader(connection).ReadBytes('\n')
		if readErr == nil {
			_, readErr = connection.Write([]byte(response))
		}
		closeErr := connection.Close()
		done <- errors.Join(readErr, closeErr, listener.Close())
	}()
	wait := func() {
		if err := <-done; err != nil {
			t.Errorf("response server error = %v", err)
		}
	}
	return socketPath, wait
}

func TestServer_DefensivePathAndLifecycleBranches(t *testing.T) {
	handler, err := NewHandler(HandlerConfig{Queries: &apiQueries{}, Clock: time.Now})
	if err != nil {
		t.Fatalf("NewHandler() error = %v", err)
	}
	if _, err := Listen(filepath.Join(canonicalTempDir(t), "nil.sock"), CallerOperatorCLI, nil); err == nil {
		t.Fatal("Listen(nil handler) error = nil")
	}
	if _, err := Listen(filepath.Join(canonicalTempDir(t), "caller.sock"), CallerClass("invented"), handler); err == nil {
		t.Fatal("Listen(invalid caller) error = nil")
	}
	if _, err := Listen(filepath.Join(string(os.PathSeparator), "devcrew.sock"), CallerOperatorCLI, handler); err == nil {
		t.Fatal("Listen(root parent) error = nil")
	}
	root := canonicalTempDir(t)
	nonCanonicalPath := root + string(os.PathSeparator) + "nested" + string(os.PathSeparator) + ".." + string(os.PathSeparator) + "api.sock"
	if _, err := Listen(nonCanonicalPath, CallerOperatorCLI, handler); err == nil {
		t.Fatal("Listen(non-canonical path) error = nil")
	}
	if _, err := Listen("/"+strings.Repeat("x", maximumSocketPath+1), CallerOperatorCLI, handler); err == nil {
		t.Fatal("Listen(long path) error = nil")
	}
	fileParent := filepath.Join(root, "file-parent")
	if err := os.WriteFile(fileParent, nil, 0o600); err != nil {
		t.Fatalf("create file parent: %v", err)
	}
	if _, err := Listen(filepath.Join(fileParent, "api.sock"), CallerOperatorCLI, handler); err == nil {
		t.Fatal("Listen(non-directory parent) error = nil")
	}
	target := filepath.Join(root, "target")
	if err := os.WriteFile(target, nil, 0o600); err != nil {
		t.Fatalf("create symlink target: %v", err)
	}
	linkedTarget := filepath.Join(root, "linked.sock")
	if err := os.Symlink(target, linkedTarget); err != nil {
		t.Fatalf("create target symlink: %v", err)
	}
	if _, err := Listen(linkedTarget, CallerOperatorCLI, handler); err == nil {
		t.Fatal("Listen(symlink target) error = nil")
	}

	stalePath := filepath.Join(root, "stale.sock")
	address, err := net.ResolveUnixAddr("unix", stalePath)
	if err != nil {
		t.Fatalf("resolve stale socket: %v", err)
	}
	stale, err := net.ListenUnix("unix", address)
	if err != nil {
		t.Fatalf("listen stale socket: %v", err)
	}
	stale.SetUnlinkOnClose(false)
	if err := stale.Close(); err != nil {
		t.Fatalf("close stale socket: %v", err)
	}
	server, err := Listen(stalePath, CallerOperatorCLI, handler)
	if err != nil {
		t.Fatalf("Listen(stale socket) error = %v", err)
	}
	if err := server.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if err := server.Close(); err != nil {
		t.Fatalf("Close(second) error = %v", err)
	}
	//lint:ignore SA1012 The boundary test proves the server rejects a nil lifecycle context.
	if err := server.Serve(nil); err == nil {
		t.Fatal("Serve(nil context) error = nil")
	}
	var nilServer *Server
	if err := nilServer.Close(); err != nil {
		t.Fatalf("nil Server.Close() error = %v", err)
	}
	if err := (&Server{}).Close(); err != nil {
		t.Fatalf("empty Server.Close() error = %v", err)
	}
	server.recordServeError(errors.New("first"))
	server.recordServeError(errors.New("second"))
	if err := server.firstServeError(); err == nil || err.Error() != "first" {
		t.Fatalf("firstServeError() = %v, want first", err)
	}
}

func TestServer_RejectsRequestWithoutNewline(t *testing.T) {
	socketPath, stop := startAPIServer(t, &apiQueries{}, CallerOperatorCLI, time.Now)
	defer stop()
	connection, err := net.DialTimeout("unix", socketPath, time.Second)
	if err != nil {
		t.Fatalf("dial API socket: %v", err)
	}
	request := `{"protocolVersion":"devcrew.local.v1","operationId":"read-0001","method":"Diagnose","payload":{}}`
	if _, err := connection.Write([]byte(request)); err != nil {
		t.Fatalf("write request: %v", err)
	}
	if err := connection.(*net.UnixConn).CloseWrite(); err != nil {
		t.Fatalf("close request write side: %v", err)
	}
	var outcome Outcome
	if err := json.NewDecoder(connection).Decode(&outcome); err != nil {
		t.Fatalf("decode rejection: %v", err)
	}
	if err := connection.Close(); err != nil {
		t.Fatalf("close connection: %v", err)
	}
	if outcome.Error == nil || outcome.Error.Code != domain.ErrorInvalidArgument {
		t.Fatalf("outcome = %#v, want invalid argument", outcome)
	}
}

func TestServer_ClosePreservesAReplacementWithDifferentIdentity(t *testing.T) {
	handler, err := NewHandler(HandlerConfig{Queries: &apiQueries{}, Clock: time.Now})
	if err != nil {
		t.Fatalf("NewHandler() error = %v", err)
	}
	root := canonicalTempDir(t)
	socketPath := filepath.Join(root, "owned.sock")
	server, err := Listen(socketPath, CallerOperatorCLI, handler)
	if err != nil {
		t.Fatalf("Listen() error = %v", err)
	}
	movedPath := filepath.Join(root, "moved-owned.sock")
	if err := os.Rename(socketPath, movedPath); err != nil {
		t.Fatalf("move owned socket: %v", err)
	}
	if err := os.WriteFile(socketPath, []byte("replacement"), 0o600); err != nil {
		t.Fatalf("create replacement: %v", err)
	}
	closeErr := server.Close()
	if closeErr == nil || !strings.Contains(closeErr.Error(), "identity changed") {
		t.Fatalf("Close() error = %v, want identity-change preservation", closeErr)
	}
	contents, err := os.ReadFile(socketPath)
	if err != nil {
		t.Fatalf("read preserved replacement: %v", err)
	}
	if string(contents) != "replacement" {
		t.Fatalf("replacement contents = %q, want preserved", contents)
	}
	if err := os.Remove(movedPath); err != nil {
		t.Fatalf("remove moved owned socket: %v", err)
	}
}

func TestWriteOutcome_ReportsDisconnectedPeer(t *testing.T) {
	serverConnection, clientConnection := net.Pipe()
	if err := clientConnection.Close(); err != nil {
		t.Fatalf("close peer: %v", err)
	}
	if err := writeOutcome(serverConnection, Outcome{ProtocolVersion: ProtocolVersion}); err == nil {
		t.Fatal("writeOutcome(disconnected) error = nil")
	}
	if err := serverConnection.Close(); err != nil {
		t.Fatalf("close server connection: %v", err)
	}
}
