package localapi

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/comisai/comis-dev-crew/internal/application"
	"github.com/comisai/comis-dev-crew/internal/domain"
)

func TestServerClient_ReadQueriesOverOwnerOnlyUnixSocket(t *testing.T) {
	now := time.Date(2026, time.August, 8, 21, 0, 0, 0, time.UTC)
	queries := &apiQueries{
		diagnostic: application.DiagnosticReport{SchemaVersion: 1, CapturedAtMs: now.UnixMilli(), StateVersion: 7},
		fleet:      application.FleetSnapshot{SchemaVersion: 1, CapturedAtMs: now.UnixMilli(), StateVersion: 7, Tasks: []application.TaskSummary{}},
		list:       application.TaskList{SchemaVersion: 1, CapturedAtMs: now.UnixMilli(), StateVersion: 7, Tasks: []application.TaskSummary{}},
		detail:     application.TaskDetail{SchemaVersion: 1, CapturedAtMs: now.UnixMilli(), StateVersion: 7, Summary: application.TaskSummary{TaskHandle: "task-0001", StateVersion: 7}},
		explanation: application.TaskExplanation{SchemaVersion: 1, CapturedAtMs: now.UnixMilli(), Summary: application.TaskSummary{
			TaskHandle: "task-0001", StateVersion: 7,
		}},
		operation: application.OperationView{SchemaVersion: 1, CapturedAtMs: now.UnixMilli(), OperationID: "op-0001", StateVersion: 7},
		launchPlan: application.LaunchPlan{
			SchemaVersion: 1, CapturedAtMs: now.UnixMilli(), StateVersion: 7,
			TaskHandle: "task-0001", WorkerProfileID: "codex-reviewed",
			TerminalAllowEntryID: "terminal-codex-reviewed",
		},
	}
	socketPath, stop := startAPIServer(t, queries, CallerOperatorCLI, time.Now)
	defer stop()
	client, err := NewClient(socketPath, time.Second)
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}

	diagnostic, err := client.Diagnose(context.Background(), "read-0001")
	if err != nil || !reflect.DeepEqual(diagnostic, queries.diagnostic) {
		t.Fatalf("Diagnose() = %#v, %v, want %#v", diagnostic, err, queries.diagnostic)
	}
	fleet, err := client.Fleet(context.Background(), "read-0002")
	if err != nil || !reflect.DeepEqual(fleet, queries.fleet) {
		t.Fatalf("Fleet() = %#v, %v, want %#v", fleet, err, queries.fleet)
	}
	list, err := client.ListTasks(context.Background(), "read-0003")
	if err != nil || !reflect.DeepEqual(list, queries.list) {
		t.Fatalf("ListTasks() = %#v, %v, want %#v", list, err, queries.list)
	}
	detail, err := client.ShowTask(context.Background(), "read-0004", "task-0001")
	if err != nil || !reflect.DeepEqual(detail, queries.detail) {
		t.Fatalf("ShowTask() = %#v, %v, want %#v", detail, err, queries.detail)
	}
	explanation, err := client.ExplainTask(context.Background(), "read-0005", "task-0001")
	if err != nil || !reflect.DeepEqual(explanation, queries.explanation) {
		t.Fatalf("ExplainTask() = %#v, %v, want %#v", explanation, err, queries.explanation)
	}
	operation, err := client.Operation(context.Background(), "read-0006", "op-0001")
	if err != nil || !reflect.DeepEqual(operation, queries.operation) {
		t.Fatalf("Operation() = %#v, %v, want %#v", operation, err, queries.operation)
	}
	launchPlan, err := client.GetLaunchPlan(context.Background(), "read-0007", "task-0001")
	if err != nil || !reflect.DeepEqual(launchPlan, queries.launchPlan) {
		t.Fatalf("GetLaunchPlan() = %#v, %v, want %#v", launchPlan, err, queries.launchPlan)
	}

	assertMode(t, filepath.Dir(socketPath), 0o700)
	assertMode(t, socketPath, 0o600)
}

func TestServer_StrictlyRejectsMalformedAndAuthorityBroadeningRequests(t *testing.T) {
	now := time.Date(2026, time.August, 8, 21, 0, 0, 0, time.UTC)
	socketPath, stop := startAPIServer(t, &apiQueries{}, CallerOperatorCLI, func() time.Time { return now })
	defer stop()
	validPrefix := `{"protocolVersion":"devcrew.local.v1","operationId":"read-0001",`
	tests := []struct {
		name string
		line string
		code domain.ErrorCode
	}{
		{name: "unknown request field", line: validPrefix + `"method":"Diagnose","payload":{},"actor":"operator"}`, code: domain.ErrorInvalidArgument},
		{name: "duplicate request field", line: `{"protocolVersion":"devcrew.local.v1","operationId":"read-0001","operationId":"read-0002","method":"Diagnose","payload":{}}`, code: domain.ErrorInvalidArgument},
		{name: "trailing value", line: validPrefix + `"method":"Diagnose","payload":{}} {}`, code: domain.ErrorInvalidArgument},
		{name: "wrong protocol", line: `{"protocolVersion":"other","operationId":"read-0001","method":"Diagnose","payload":{}}`, code: domain.ErrorInvalidArgument},
		{name: "invalid operation id", line: `{"protocolVersion":"devcrew.local.v1","operationId":"bad id","method":"Diagnose","payload":{}}`, code: domain.ErrorInvalidArgument},
		{name: "unknown method", line: validPrefix + `"method":"DeleteEverything","payload":{}}`, code: domain.ErrorInvalidArgument},
		{name: "fleet payload field", line: validPrefix + `"method":"FleetStatus","payload":{"scope":"all"}}`, code: domain.ErrorInvalidArgument},
		{name: "list payload field", line: validPrefix + `"method":"ListTasks","payload":{"scope":"all"}}`, code: domain.ErrorInvalidArgument},
		{name: "unknown payload field", line: validPrefix + `"method":"ShowTask","payload":{"taskHandle":"task-0001","scope":"all"}}`, code: domain.ErrorInvalidArgument},
		{name: "duplicate payload field", line: validPrefix + `"method":"ShowTask","payload":{"taskHandle":"task-0001","taskHandle":"task-0002"}}`, code: domain.ErrorInvalidArgument},
		{name: "explain payload field", line: validPrefix + `"method":"ExplainTask","payload":{"taskHandle":"task-0001","scope":"all"}}`, code: domain.ErrorInvalidArgument},
		{name: "operation payload field", line: validPrefix + `"method":"GetOperation","payload":{"operationId":"op-0001","scope":"all"}}`, code: domain.ErrorInvalidArgument},
		{name: "launch plan payload field", line: validPrefix + `"method":"GetLaunchPlan","payload":{"taskHandle":"task-0001","path":"/tmp/redirect"}}`, code: domain.ErrorInvalidArgument},
		{name: "expired deadline", line: validPrefix + `"method":"Diagnose","payload":{},"deadlineAtMs":` + formatInt(now.Add(-time.Second).UnixMilli()) + `}`, code: domain.ErrorDeadlineExceeded},
		{name: "oversized request", line: validPrefix + `"method":"Diagnose","payload":{"padding":"` + strings.Repeat("x", MaxRequestBytes) + `"}}`, code: domain.ErrorInvalidArgument},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			outcome := sendRawRequest(t, socketPath, test.line+"\n")
			if outcome.Status != domain.OperationRejected || outcome.Error == nil || outcome.Error.Code != test.code {
				t.Fatalf("outcome = %#v, want rejected %q", outcome, test.code)
			}
		})
	}

	workerSocket, stopWorker := startAPIServer(t, &apiQueries{}, CallerWorkerReport, func() time.Time { return now })
	defer stopWorker()
	outcome := sendRawRequest(t, workerSocket, validPrefix+`"method":"Diagnose","payload":{}}`+"\n")
	if outcome.Error == nil || outcome.Error.Code != domain.ErrorUnauthorized {
		t.Fatalf("worker caller outcome = %#v, want unauthorized", outcome)
	}
}

func TestServer_DeadlineCancelsCanonicalHandler(t *testing.T) {
	started := make(chan struct{})
	queries := &apiQueries{diagnose: func(ctx context.Context) (application.DiagnosticReport, error) {
		close(started)
		<-ctx.Done()
		failure, err := domain.NewFailure(domain.ErrorDeadlineExceeded, true, "query did not complete", "retry the read request", ctx.Err())
		if err != nil {
			return application.DiagnosticReport{}, err
		}
		return application.DiagnosticReport{}, failure
	}}
	now := time.Now().UTC()
	socketPath, stop := startAPIServer(t, queries, CallerOperatorCLI, time.Now)
	defer stop()
	deadline := now.Add(100 * time.Millisecond).UnixMilli()
	line := `{"protocolVersion":"devcrew.local.v1","operationId":"read-0001","method":"Diagnose","payload":{},"deadlineAtMs":` + formatInt(deadline) + `}` + "\n"
	outcome := sendRawRequest(t, socketPath, line)
	<-started
	if outcome.Error == nil || outcome.Error.Code != domain.ErrorDeadlineExceeded {
		t.Fatalf("deadline outcome = %#v, want deadline-exceeded", outcome)
	}
}

func TestListen_RejectsUnsafeSocketPathsAndLiveEndpointReplacement(t *testing.T) {
	handler, err := NewHandler(HandlerConfig{Queries: &apiQueries{}, Clock: time.Now})
	if err != nil {
		t.Fatalf("NewHandler() error = %v", err)
	}
	if _, err := Listen("relative/devcrew.sock", CallerOperatorCLI, handler); err == nil {
		t.Fatal("Listen(relative) error = nil")
	}
	root := canonicalTempDir(t)
	realDirectory := filepath.Join(root, "real")
	if err := os.Mkdir(realDirectory, 0o700); err != nil {
		t.Fatalf("create real directory: %v", err)
	}
	linkedDirectory := filepath.Join(root, "linked")
	if err := os.Symlink(realDirectory, linkedDirectory); err != nil {
		t.Fatalf("create symlink: %v", err)
	}
	if _, err := Listen(filepath.Join(linkedDirectory, "devcrew.sock"), CallerOperatorCLI, handler); err == nil {
		t.Fatal("Listen(symlink parent) error = nil")
	}
	regularTarget := filepath.Join(root, "regular.sock")
	if err := os.WriteFile(regularTarget, nil, 0o600); err != nil {
		t.Fatalf("create regular target: %v", err)
	}
	if _, err := Listen(regularTarget, CallerOperatorCLI, handler); err == nil {
		t.Fatal("Listen(regular target) error = nil")
	}

	livePath := filepath.Join(root, "runtime", "live.sock")
	first, err := Listen(livePath, CallerOperatorCLI, handler)
	if err != nil {
		t.Fatalf("Listen(first) error = %v", err)
	}
	defer first.Close()
	if _, err := Listen(livePath, CallerOperatorCLI, handler); err == nil {
		t.Fatal("Listen(live endpoint) error = nil, want replacement refusal")
	}
}

type apiQueries struct {
	diagnostic  application.DiagnosticReport
	fleet       application.FleetSnapshot
	list        application.TaskList
	detail      application.TaskDetail
	explanation application.TaskExplanation
	operation   application.OperationView
	launchPlan  application.LaunchPlan
	diagnose    func(context.Context) (application.DiagnosticReport, error)
}

func (queries *apiQueries) Diagnose(ctx context.Context) (application.DiagnosticReport, error) {
	if queries.diagnose != nil {
		return queries.diagnose(ctx)
	}
	return queries.diagnostic, nil
}

func (queries *apiQueries) Fleet(context.Context) (application.FleetSnapshot, error) {
	return queries.fleet, nil
}

func (queries *apiQueries) ListTasks(context.Context) (application.TaskList, error) {
	return queries.list, nil
}

func (queries *apiQueries) ShowTask(context.Context, string) (application.TaskDetail, error) {
	return queries.detail, nil
}

func (queries *apiQueries) ExplainTask(context.Context, string) (application.TaskExplanation, error) {
	return queries.explanation, nil
}

func (queries *apiQueries) Operation(context.Context, string) (application.OperationView, error) {
	return queries.operation, nil
}

func (queries *apiQueries) GetLaunchPlan(context.Context, string) (application.LaunchPlan, error) {
	return queries.launchPlan, nil
}

func startAPIServer(t *testing.T, queries ReadQueries, caller CallerClass, clock application.Clock) (string, func()) {
	t.Helper()
	handler, err := NewHandler(HandlerConfig{Queries: queries, Clock: clock})
	if err != nil {
		t.Fatalf("NewHandler() error = %v", err)
	}
	socketPath := filepath.Join(canonicalTempDir(t), "runtime", "devcrew.sock")
	server, err := Listen(socketPath, caller, handler)
	if err != nil {
		t.Fatalf("Listen() error = %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- server.Serve(ctx) }()
	stop := func() {
		cancel()
		if err := server.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
			t.Errorf("Server.Close() error = %v", err)
		}
		if err := <-done; err != nil {
			t.Errorf("Server.Serve() error = %v", err)
		}
	}
	return socketPath, stop
}

func sendRawRequest(t *testing.T, socketPath, request string) Outcome {
	t.Helper()
	connection, err := net.DialTimeout("unix", socketPath, time.Second)
	if err != nil {
		t.Fatalf("dial API socket: %v", err)
	}
	defer connection.Close()
	if _, err := connection.Write([]byte(request)); err != nil {
		t.Fatalf("write raw request: %v", err)
	}
	var outcome Outcome
	decoder := json.NewDecoder(connection)
	if err := decoder.Decode(&outcome); err != nil {
		t.Fatalf("decode raw outcome: %v", err)
	}
	return outcome
}

func assertMode(t *testing.T, path string, want os.FileMode) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
	if got := info.Mode().Perm(); got != want {
		t.Fatalf("mode for %s = %#o, want %#o", path, got, want)
	}
}

func canonicalTempDir(t *testing.T) string {
	t.Helper()
	directory, err := os.MkdirTemp(os.TempDir(), "devcrew-api-")
	if err != nil {
		t.Fatalf("create short temporary directory: %v", err)
	}
	t.Cleanup(func() {
		if err := os.RemoveAll(directory); err != nil {
			t.Errorf("remove short temporary directory: %v", err)
		}
	})
	resolved, err := filepath.EvalSymlinks(directory)
	if err != nil {
		t.Fatalf("resolve temporary directory: %v", err)
	}
	return resolved
}

func formatInt(value int64) string {
	return strconv.FormatInt(value, 10)
}

func FuzzStrictDecoder(f *testing.F) {
	for _, seed := range []string{
		`{}`,
		`{"value":1}`,
		`{"nested":[{"value":true},null]}`,
		`{"duplicate":1,"duplicate":2}`,
		`[]`,
		`null`,
		`{"unterminated":`,
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, input string) {
		if len(input) > MaxRequestBytes {
			return
		}
		var decoded any
		if err := decodeStrict([]byte(input), &decoded); err == nil {
			if _, err := json.Marshal(decoded); err != nil {
				t.Fatalf("marshal successfully decoded value: %v", err)
			}
		}
	})
}
