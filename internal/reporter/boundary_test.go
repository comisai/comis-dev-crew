package reporter

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/comisai/comis-dev-crew/internal/application"
	"github.com/comisai/comis-dev-crew/internal/domain"
)

func TestRunCommandCoversSafeDependencyBoundaries(t *testing.T) {
	brief := boundaryBrief("task-boundary-0001")
	valid := &boundaryCapability{brief: brief, receipt: domain.ReportReceipt{LocalReportID: "report-boundary-0001", StateVersion: 2}}
	tests := []struct {
		name   string
		ctx    context.Context
		args   []string
		config CommandConfig
		want   int
	}{
		{name: "short help", ctx: context.Background(), args: []string{"-h"}, config: CommandConfig{}, want: 0},
		{name: "nil context", args: []string{"brief"}, config: CommandConfig{Capability: valid}, want: 1},
		{name: "brief arguments", ctx: context.Background(), args: []string{"brief", "extra"}, config: CommandConfig{Capability: valid}, want: 2},
		{name: "missing capability", ctx: context.Background(), args: []string{"brief"}, config: CommandConfig{}, want: 1},
		{name: "invalid brief", ctx: context.Background(), args: []string{"brief"}, config: CommandConfig{Capability: &boundaryCapability{}}, want: 1},
		{name: "missing clock", ctx: context.Background(), args: []string{"progress", "--summary", "bounded"}, config: CommandConfig{Capability: valid, NewLocalReportID: func() (string, error) { return "report-boundary-0001", nil }}, want: 1},
		{name: "id generation", ctx: context.Background(), args: []string{"progress", "--summary", "bounded"}, config: CommandConfig{Capability: valid, Clock: time.Now, NewLocalReportID: func() (string, error) { return "", errors.New("private") }}, want: 1},
		{name: "invalid generated id", ctx: context.Background(), args: []string{"progress", "--summary", "bounded"}, config: CommandConfig{Capability: valid, Clock: time.Now, NewLocalReportID: func() (string, error) { return "bad id", nil }}, want: 2},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			if got := RunCommand(test.ctx, test.args, &stdout, &stderr, test.config); got != test.want {
				t.Fatalf("RunCommand() = %d, want %d; stdout=%q stderr=%q", got, test.want, stdout.String(), stderr.String())
			}
		})
	}
	if got := RunCommand(context.Background(), []string{"-h"}, nil, nil, CommandConfig{}); got != 0 {
		t.Fatalf("RunCommand(nil writers) = %d", got)
	}
}

func TestRuntimeConstructionRejectsUnsafeOrAmbiguousTargets(t *testing.T) {
	brief := boundaryBrief("task-boundary-0001")
	if _, err := ListenRuntime(RuntimeServerConfig{}); err == nil {
		t.Fatal("ListenRuntime accepted an empty config")
	}
	if _, err := ListenRuntime(RuntimeServerConfig{
		SocketPath: "relative/attachment.sock", Brief: brief, Reporter: &Client{},
		LaunchOperationID: "operation-partial-launch",
	}); err == nil {
		t.Fatal("ListenRuntime accepted a partial launch binding")
	}
	if _, err := ListenRuntime(RuntimeServerConfig{SocketPath: "relative/attachment.sock", Brief: brief, Reporter: &Client{}}); err == nil {
		t.Fatal("ListenRuntime accepted a relative target")
	}

	root := boundaryRuntimeDirectory(t)
	socketPath := filepath.Join(root, "attachment.sock")
	if err := os.WriteFile(socketPath, []byte("occupied"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ListenRuntime(RuntimeServerConfig{SocketPath: socketPath, Brief: brief, Reporter: &Client{}}); err == nil {
		t.Fatal("ListenRuntime replaced an existing target")
	}
	if _, err := NewRuntimeClient(socketPath, time.Second); err == nil {
		t.Fatal("NewRuntimeClient accepted a regular file")
	}
	if err := os.Remove(socketPath); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := ListenRuntime(RuntimeServerConfig{SocketPath: socketPath, Brief: brief, Reporter: &Client{}}); err == nil {
		t.Fatal("ListenRuntime accepted a broad parent")
	}
	if err := os.Chmod(root, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := NewRuntimeClient(socketPath, 0); err == nil {
		t.Fatal("NewRuntimeClient accepted a zero timeout")
	}
	if _, err := NewRuntimeClient(socketPath, time.Minute+time.Nanosecond); err == nil {
		t.Fatal("NewRuntimeClient accepted an unbounded timeout")
	}
	if _, err := NewRuntimeClient("relative/attachment.sock", time.Second); err == nil {
		t.Fatal("NewRuntimeClient accepted a relative path")
	}
	if _, err := NewRuntimeClient(socketPath, time.Second); err == nil {
		t.Fatal("NewRuntimeClient accepted a missing socket")
	}
}

func TestDevcrewReportBriefProbeConnectsThroughAssignedMountedTarget(t *testing.T) {
	mountRoot, err := os.MkdirTemp("/tmp", "dcr-mount-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(mountRoot) })
	mountRoot, err = filepath.EvalSymlinks(mountRoot)
	if err != nil {
		t.Fatal(err)
	}
	targetName := "attachment-0123456789abcdef0123456789abcdef.sock"
	socketPath := filepath.Join(mountRoot, targetName)
	address, err := net.ResolveUnixAddr("unix", socketPath)
	if err != nil {
		t.Fatal(err)
	}
	listener, err := net.ListenUnix("unix", address)
	if err != nil {
		t.Fatal(err)
	}
	listener.SetUnlinkOnClose(false)
	if err := os.Chmod(socketPath, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(mountRoot, 0o711); err != nil {
		t.Fatal(err)
	}
	info, err := os.Lstat(socketPath)
	if err != nil {
		t.Fatal(err)
	}
	brief := boundaryBrief("task-mounted-probe")
	server := &RuntimeServer{
		listener: listener, socketPath: socketPath, socketInfo: info,
		brief: brief, reporter: &Client{},
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- server.Serve(ctx) }()
	t.Cleanup(func() {
		cancel()
		_ = server.Close()
		if err := <-done; err != nil {
			t.Errorf("mounted runtime probe stop = %v", err)
		}
	})
	client, err := newMountedRuntimeClient(socketPath, targetName, mountRoot, time.Second)
	if err != nil {
		t.Fatalf("open assigned mounted target: %v", err)
	}
	var stdout, stderr bytes.Buffer
	if exit := RunCommand(context.Background(), []string{"brief"}, &stdout, &stderr, CommandConfig{Capability: client}); exit != 0 || stdout.String() != brief.Content || stderr.Len() != 0 {
		t.Fatalf("devcrew-report brief probe = %d, stdout=%q stderr=%q", exit, stdout.String(), stderr.String())
	}
	if err := os.Chmod(mountRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := client.Brief(context.Background()); err == nil {
		t.Fatal("mounted runtime client ignored a changed protected mount posture")
	}
}

func TestMountedRuntimeClientRejectsIntermediateSymlinkReplacementOnCall(t *testing.T) {
	const targetName = "attachment-0123456789abcdef0123456789abcdef.sock"
	mountDirectory := mountedRuntimeTestDirectory(t)
	socketPath := filepath.Join(mountDirectory, targetName)
	address, err := net.ResolveUnixAddr("unix", socketPath)
	if err != nil {
		t.Fatal(err)
	}
	listener, err := net.ListenUnix("unix", address)
	if err != nil {
		t.Fatal(err)
	}
	listener.SetUnlinkOnClose(false)
	if err := os.Chmod(socketPath, 0o600); err != nil {
		t.Fatal(err)
	}
	info, err := os.Lstat(socketPath)
	if err != nil {
		t.Fatal(err)
	}
	brief := boundaryBrief("task-mounted-replacement")
	server := &RuntimeServer{listener: listener, socketPath: socketPath, socketInfo: info, brief: brief, reporter: &Client{}}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- server.Serve(ctx) }()
	t.Cleanup(func() {
		cancel()
		_ = server.Close()
		if err := <-done; err != nil {
			t.Errorf("mounted runtime replacement stop = %v", err)
		}
	})
	client, err := newMountedRuntimeClient(socketPath, targetName, mountDirectory, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := client.Brief(context.Background()); err != nil || got != brief {
		t.Fatalf("Brief(before replacement) = %#v, %v", got, err)
	}
	runDirectory := filepath.Dir(filepath.Dir(mountDirectory))
	originalRunDirectory := filepath.Join(filepath.Dir(runDirectory), "original-run")
	if err := os.Rename(runDirectory, originalRunDirectory); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(originalRunDirectory, runDirectory); err != nil {
		t.Fatal(err)
	}
	if _, err := client.Brief(context.Background()); err == nil {
		t.Fatal("mounted runtime client accepted an intermediate symlink replacement")
	}
}

func TestMountedRuntimeClientRequiresExactAssignedTarget(t *testing.T) {
	targetName := "attachment-0123456789abcdef0123456789abcdef.sock"
	mountRoot := "/run/comis/attachments"
	validPath := filepath.Join(mountRoot, targetName)
	if err := validateMountedRuntimeSocketPath(validPath, targetName, mountRoot); err != nil {
		t.Fatalf("valid assigned target rejected: %v", err)
	}
	tests := []struct {
		name           string
		path           string
		assigned       string
		mountDirectory string
	}{
		{name: "relative", path: "run/comis/attachments/" + targetName, assigned: targetName, mountDirectory: mountRoot},
		{name: "unclean", path: mountRoot + "/../attachments/" + targetName, assigned: targetName, mountDirectory: mountRoot},
		{name: "different directory", path: "/run/comis/other/" + targetName, assigned: targetName, mountDirectory: mountRoot},
		{name: "source basename", path: mountRoot + "/attachment.sock", assigned: "attachment.sock", mountDirectory: mountRoot},
		{name: "different assigned name", path: validPath, assigned: "attachment-ffffffffffffffffffffffffffffffff.sock", mountDirectory: mountRoot},
		{name: "uppercase assigned name", path: validPath, assigned: "attachment-0123456789ABCDEF0123456789ABCDEF.sock", mountDirectory: mountRoot},
		{name: "control character", path: validPath + "\n", assigned: targetName, mountDirectory: mountRoot},
		{name: "unclean mount directory", path: validPath, assigned: targetName, mountDirectory: mountRoot + "/."},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := validateMountedRuntimeSocketPath(test.path, test.assigned, test.mountDirectory); err == nil {
				t.Fatal("mounted runtime path accepted an unassigned target")
			}
		})
	}
	if _, err := NewMountedRuntimeClient("/tmp/"+targetName, targetName, time.Second); err == nil {
		t.Fatal("NewMountedRuntimeClient accepted a socket outside the protected mount")
	}
	unsafeRoot := boundaryRuntimeDirectory(t)
	if err := os.Chmod(unsafeRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := newMountedRuntimeClient(filepath.Join(unsafeRoot, targetName), targetName, unsafeRoot, time.Second); err == nil {
		t.Fatal("mounted runtime client accepted a broadly accessible mount")
	}
}

func TestMountedRuntimeClientReproducesRealJailMountShape(t *testing.T) {
	const targetName = "attachment-0123456789abcdef0123456789abcdef.sock"

	t.Run("valid protected mount", func(t *testing.T) {
		mountDirectory := mountedRuntimeTestDirectory(t)
		socketPath := filepath.Join(mountDirectory, targetName)
		listenBoundarySocket(t, socketPath, 0o600)
		if _, err := newMountedRuntimeClient(socketPath, targetName, mountDirectory, time.Second); err != nil {
			t.Fatalf("NewMountedRuntimeClient(valid jail mount) error = %v", err)
		}
	})

	t.Run("execute only traversal mount", func(t *testing.T) {
		mountDirectory := mountedRuntimeTestDirectory(t)
		socketPath := filepath.Join(mountDirectory, targetName)
		listenBoundarySocket(t, socketPath, 0o600)
		if err := os.Chmod(mountDirectory, 0o711); err != nil {
			t.Fatal(err)
		}
		if _, err := newMountedRuntimeClient(socketPath, targetName, mountDirectory, time.Second); err != nil {
			t.Fatalf("NewMountedRuntimeClient(execute-only traversal mount) error = %v", err)
		}
	})

	tests := []struct {
		name string
		want string
		make func(*testing.T) (string, string)
	}{
		{
			name: "broad mount mode",
			want: "runtime mounted attachment directory permissions are unsafe: require owner rwx and no group or other read/write access",
			make: func(t *testing.T) (string, string) {
				mountDirectory := mountedRuntimeTestDirectory(t)
				socketPath := filepath.Join(mountDirectory, targetName)
				listenBoundarySocket(t, socketPath, 0o600)
				if err := os.Chmod(mountDirectory, 0o755); err != nil {
					t.Fatal(err)
				}
				return mountDirectory, socketPath
			},
		},
		{
			name: "symlinked component",
			want: "runtime mounted attachment directory is not canonical",
			make: func(t *testing.T) (string, string) {
				root := shortBoundaryDirectory(t)
				realMount := filepath.Join(root, "real", "comis", "attachments")
				if err := os.MkdirAll(realMount, 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(filepath.Join(root, "real"), filepath.Join(root, "run")); err != nil {
					t.Fatal(err)
				}
				mountDirectory := filepath.Join(root, "run", "comis", "attachments")
				socketPath := filepath.Join(mountDirectory, targetName)
				listenBoundarySocket(t, filepath.Join(realMount, targetName), 0o600)
				return mountDirectory, socketPath
			},
		},
		{
			name: "broad socket mode",
			want: "create runtime attachment client: socket permissions are unsafe: require 0600",
			make: func(t *testing.T) (string, string) {
				mountDirectory := mountedRuntimeTestDirectory(t)
				socketPath := filepath.Join(mountDirectory, targetName)
				listenBoundarySocket(t, socketPath, 0o644)
				return mountDirectory, socketPath
			},
		},
		{
			name: "socket absent",
			want: "create runtime attachment client: socket does not exist",
			make: func(t *testing.T) (string, string) {
				mountDirectory := mountedRuntimeTestDirectory(t)
				return mountDirectory, filepath.Join(mountDirectory, targetName)
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			mountDirectory, socketPath := test.make(t)
			_, err := newMountedRuntimeClient(socketPath, targetName, mountDirectory, time.Second)
			if err == nil || err.Error() != test.want {
				t.Fatalf("NewMountedRuntimeClient() error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestRuntimeServerRejectsClosedWireVocabulary(t *testing.T) {
	server, socketPath, stop := startBoundaryRuntime(t, &Client{})
	defer stop()
	requests := []string{
		`{"version":"devcrew.runtime.v1","kind":"brief","report":{}}` + "\n",
		`{"version":"devcrew.runtime.v1","kind":"report"}` + "\n",
		`{"version":"devcrew.runtime.v1","kind":"invented"}` + "\n",
		`{"version":"wrong","kind":"brief"}` + "\n",
		`{"version":"devcrew.runtime.v1","kind":"report","report":{"SchemaVersion":1}}` + "\n",
		`{"version":` + "\n",
	}
	for _, request := range requests {
		outcome := exchangeBoundaryRequest(t, socketPath, request)
		if outcome.Error == nil || outcome.Brief != nil || outcome.Receipt != nil {
			t.Fatalf("rejected request outcome = %#v", outcome)
		}
	}
	//lint:ignore SA1012 This boundary test exercises the explicit nil-context rejection.
	if err := server.Serve(nil); err == nil {
		t.Fatal("Serve accepted a nil context")
	}
	if err := (&RuntimeServer{}).Serve(context.Background()); err == nil {
		t.Fatal("Serve accepted an unavailable listener")
	}
}

func TestRuntimeClientRejectsMalformedAndMismatchedOutcomes(t *testing.T) {
	brief := boundaryBrief("task-boundary-0001")
	report := boundaryReport(brief)
	invalidBrief := brief
	invalidBrief.RevisionHash = strings.Repeat("0", sha256.Size*2)
	validReceipt := domain.ReportReceipt{
		TaskHandle: "task-boundary-0001", LocalReportID: report.LocalReportID, StateVersion: 2,
		AcceptedAt: time.Date(2026, time.August, 10, 15, 0, 0, 0, time.UTC),
	}
	private := "private decision response"
	cases := []struct {
		name    string
		outcome RuntimeOutcome
		call    func(*RuntimeClient) error
	}{
		{name: "brief error", outcome: runtimeRejected("unavailable"), call: func(client *RuntimeClient) error { _, err := client.Brief(context.Background()); return err }},
		{name: "invalid brief", outcome: RuntimeOutcome{Version: runtimeProtocolVersion, Brief: &invalidBrief}, call: func(client *RuntimeClient) error { _, err := client.Brief(context.Background()); return err }},
		{name: "brief mixed with receipt", outcome: RuntimeOutcome{Version: runtimeProtocolVersion, Brief: &brief, Receipt: &validReceipt}, call: func(client *RuntimeClient) error { _, err := client.Brief(context.Background()); return err }},
		{name: "report error", outcome: runtimeRejected("rejected"), call: func(client *RuntimeClient) error { _, err := client.Report(context.Background(), report); return err }},
		{name: "report mixed with brief", outcome: RuntimeOutcome{Version: runtimeProtocolVersion, Brief: &brief, Receipt: &validReceipt}, call: func(client *RuntimeClient) error { _, err := client.Report(context.Background(), report); return err }},
		{name: "mismatched receipt", outcome: RuntimeOutcome{Version: runtimeProtocolVersion, Receipt: &domain.ReportReceipt{TaskHandle: validReceipt.TaskHandle, LocalReportID: "report-other-0001", StateVersion: 2, AcceptedAt: validReceipt.AcceptedAt}}, call: func(client *RuntimeClient) error { _, err := client.Report(context.Background(), report); return err }},
		{name: "pending attention with content", outcome: RuntimeOutcome{Version: runtimeProtocolVersion, AttentionResponse: &runtimeAttentionOutcome{ExternalKey: "database-choice", State: AttentionResponsePending, Response: &private}}, call: func(client *RuntimeClient) error {
			_, err := client.AwaitDecision(context.Background(), "database-choice")
			return err
		}},
		{name: "delivered attention without content", outcome: RuntimeOutcome{Version: runtimeProtocolVersion, AttentionResponse: &runtimeAttentionOutcome{ExternalKey: "database-choice", State: AttentionResponseDelivered}}, call: func(client *RuntimeClient) error {
			_, err := client.AwaitDecision(context.Background(), "database-choice")
			return err
		}},
		{name: "attention identity drift", outcome: RuntimeOutcome{Version: runtimeProtocolVersion, AttentionResponse: &runtimeAttentionOutcome{ExternalKey: "other-choice", State: AttentionResponseDelivered, Response: &private}}, call: func(client *RuntimeClient) error {
			_, err := client.AwaitDecision(context.Background(), "database-choice")
			return err
		}},
		{name: "attention mixed with brief", outcome: RuntimeOutcome{Version: runtimeProtocolVersion, Brief: &brief, AttentionResponse: &runtimeAttentionOutcome{ExternalKey: "database-choice", State: AttentionResponseDelivered, Response: &private}}, call: func(client *RuntimeClient) error {
			_, err := client.AwaitDecision(context.Background(), "database-choice")
			return err
		}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			encoded, err := json.Marshal(test.outcome)
			if err != nil {
				t.Fatal(err)
			}
			client, done := startBoundaryResponder(t, append(encoded, '\n'))
			if err := test.call(client); err == nil {
				t.Fatal("RuntimeClient accepted an invalid outcome")
			}
			done()
		})
	}
	for _, response := range [][]byte{
		[]byte(`{"version":"wrong"}` + "\n"),
		[]byte(`{"version":"devcrew.runtime.v1","unknown":true}` + "\n"),
		[]byte(`{"version":"devcrew.runtime.v1"}`),
		append([]byte(`{"version":"devcrew.runtime.v1","error":{"code":"x"},"padding":"`), append([]byte(strings.Repeat("x", maximumRuntimeResponseBytes)), []byte(`"}`+"\n")...)...),
	} {
		client, done := startBoundaryResponder(t, response)
		if _, err := client.Brief(context.Background()); err == nil {
			t.Fatal("RuntimeClient accepted a malformed response")
		}
		done()
	}
}

func TestRuntimeAttentionServerRejectsUnavailableAndDriftedResponses(t *testing.T) {
	request := runtimeRequest{Version: runtimeProtocolVersion, Kind: "attention_response", ExternalKey: "database-choice"}
	launch := &RuntimeLaunchConfig{Expected: application.LaunchAcknowledgement{ManagedRunID: "managed-run.attention"}}
	valid := AttentionResponse{
		ManagedRunID: "managed-run.attention", ExternalKey: "database-choice", State: AttentionResponsePending,
	}
	cases := []struct {
		name       string
		request    runtimeRequest
		launch     *RuntimeLaunchConfig
		newID      func() (string, error)
		response   AttentionResponse
		receiveErr error
		code       string
	}{
		{name: "malformed key", request: runtimeRequest{Version: runtimeProtocolVersion, Kind: "attention_response", ExternalKey: "bad key"}, launch: launch, code: "malformed_request"},
		{name: "unbound launch", request: request, code: "attention_binding_unavailable"},
		{name: "missing dependencies", request: request, launch: launch, code: "attention_response_unavailable"},
		{name: "operation source failure", request: request, launch: launch, newID: func() (string, error) { return "", errors.New("unavailable") }, response: valid, code: "attention_response_unavailable"},
		{name: "invalid operation identity", request: request, launch: launch, newID: func() (string, error) { return "bad operation", nil }, response: valid, code: "attention_response_unavailable"},
		{name: "receiver failure", request: request, launch: launch, newID: func() (string, error) { return "attention-response-boundary", nil }, receiveErr: errors.New("unavailable"), code: "attention_response_unavailable"},
		{name: "managed run drift", request: request, launch: launch, newID: func() (string, error) { return "attention-response-boundary", nil }, response: AttentionResponse{ManagedRunID: "managed-run.other", ExternalKey: "database-choice", State: AttentionResponsePending}, code: "attention_response_unavailable"},
		{name: "pending content", request: request, launch: launch, newID: func() (string, error) { return "attention-response-boundary", nil }, response: AttentionResponse{ManagedRunID: "managed-run.attention", ExternalKey: "database-choice", State: AttentionResponsePending, Response: "unexpected"}, code: "attention_response_unavailable"},
		{name: "delivered without content", request: request, launch: launch, newID: func() (string, error) { return "attention-response-boundary", nil }, response: AttentionResponse{ManagedRunID: "managed-run.attention", ExternalKey: "database-choice", State: AttentionResponseDelivered}, code: "attention_response_unavailable"},
		{name: "unknown state", request: request, launch: launch, newID: func() (string, error) { return "attention-response-boundary", nil }, response: AttentionResponse{ManagedRunID: "managed-run.attention", ExternalKey: "database-choice", State: AttentionResponseState("unknown")}, code: "attention_response_unavailable"},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			server := &RuntimeServer{newAttentionOperationID: test.newID}
			if test.response != (AttentionResponse{}) || test.receiveErr != nil {
				server.attentionResponses = boundaryAttentionReceiver{response: test.response, err: test.receiveErr}
			}
			outcome := server.receiveAttentionResponse(context.Background(), test.request, test.launch)
			if outcome.Error == nil || outcome.Error.Code != test.code || outcome.AttentionResponse != nil {
				t.Fatalf("receiveAttentionResponse() = %#v", outcome)
			}
		})
	}
	var client *RuntimeClient
	//lint:ignore SA1012 This boundary test exercises explicit nil-context rejection.
	if _, err := client.AwaitDecision(nil, "database-choice"); err == nil {
		t.Fatal("AwaitDecision(nil context) error = nil")
	}
	if _, err := client.AwaitDecision(context.Background(), "bad key"); err == nil {
		t.Fatal("AwaitDecision(invalid key) error = nil")
	}
}

func TestRuntimeClientAndCloseDetectIdentityChanges(t *testing.T) {
	var nilClient *RuntimeClient
	if _, err := nilClient.call(context.Background(), runtimeRequest{}); err == nil {
		t.Fatal("nil RuntimeClient was usable")
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := nilClient.call(canceled, runtimeRequest{}); err == nil {
		t.Fatal("RuntimeClient ignored canceled context")
	}
	//lint:ignore SA1012 This boundary test exercises the explicit nil-context rejection.
	if _, err := nilClient.call(nil, runtimeRequest{}); err == nil {
		t.Fatal("RuntimeClient accepted nil context")
	}
	var nilServer *RuntimeServer
	if err := nilServer.Close(); err != nil {
		t.Fatalf("nil RuntimeServer Close = %v", err)
	}

	server, socketPath, stop := startBoundaryRuntime(t, &Client{})
	client, err := NewRuntimeClient(socketPath, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if err := server.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := client.Brief(context.Background()); err == nil {
		t.Fatal("RuntimeClient ignored removed socket identity")
	}
	stop()

	server, socketPath, stop = startBoundaryRuntime(t, &Client{})
	if err := os.Remove(socketPath); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(socketPath, []byte("replacement"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := server.Close(); err == nil || !strings.Contains(err.Error(), "ambiguous") {
		t.Fatalf("Close replacement error = %v", err)
	}
	if contents, err := os.ReadFile(socketPath); err != nil || string(contents) != "replacement" {
		t.Fatalf("Close removed ambiguous replacement: %q, %v", contents, err)
	}
	stop()

	if err := removeRuntimeSocket(filepath.Join(boundaryRuntimeDirectory(t), "attachment.sock"), nil, RuntimeSocketIdentity{}); err != nil {
		t.Fatalf("removeRuntimeSocket(missing) = %v", err)
	}
	left, right := net.Pipe()
	_ = right.Close()
	if err := writeRuntimeOutcome(left, runtimeRejected("test")); err == nil {
		t.Fatal("writeRuntimeOutcome ignored a closed connection")
	}
	_ = left.Close()
}

type boundaryCapability struct {
	brief   domain.WorkerBrief
	receipt domain.ReportReceipt
}

type boundaryAttentionReceiver struct {
	response AttentionResponse
	err      error
}

func (receiver boundaryAttentionReceiver) ReceiveRuntimeAttentionResponse(
	context.Context,
	AttentionResponseRequest,
) (AttentionResponse, error) {
	return receiver.response, receiver.err
}

func (capability *boundaryCapability) Acknowledge(context.Context, string) error { return nil }

func (capability *boundaryCapability) AwaitDecision(context.Context, string) (string, error) {
	return "bounded response", nil
}

func (capability *boundaryCapability) Brief(context.Context) (domain.WorkerBrief, error) {
	return capability.brief, nil
}

func (capability *boundaryCapability) Report(context.Context, domain.WorkerReport) (domain.ReportReceipt, error) {
	return capability.receipt, nil
}

func boundaryBrief(taskHandle string) domain.WorkerBrief {
	content := "taskHandle: " + taskHandle + "\nacceptanceCriteria:\n- prove boundary\n"
	digest := sha256.Sum256([]byte(content))
	return domain.WorkerBrief{Revision: 1, RevisionHash: hex.EncodeToString(digest[:]), Content: content}
}

func boundaryReport(brief domain.WorkerBrief) domain.WorkerReport {
	observed := time.Date(2026, time.August, 10, 14, 59, 0, 0, time.UTC)
	return domain.WorkerReport{
		SchemaVersion: 1, LocalReportID: "report-boundary-0001",
		BriefRevision: brief.Revision, BriefRevisionHash: brief.RevisionHash,
		Kind: domain.ReportProgress, Summary: "bounded", WorkerObservedAt: &observed,
	}
}

func boundaryRuntimeDirectory(t *testing.T) string {
	t.Helper()
	root, err := os.MkdirTemp("/tmp", "devcrew-runtime-boundary-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	canonical, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	return canonical
}

func shortBoundaryDirectory(t *testing.T) string {
	t.Helper()
	root, err := os.MkdirTemp("/tmp", "d-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	canonical, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	return canonical
}

func mountedRuntimeTestDirectory(t *testing.T) string {
	t.Helper()
	mountDirectory := filepath.Join(shortBoundaryDirectory(t), "run", "comis", "attachments")
	if err := os.MkdirAll(mountDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(mountDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	return mountDirectory
}

func listenBoundarySocket(t *testing.T, socketPath string, mode os.FileMode) {
	t.Helper()
	address, err := net.ResolveUnixAddr("unix", socketPath)
	if err != nil {
		t.Fatal(err)
	}
	listener, err := net.ListenUnix("unix", address)
	if err != nil {
		t.Fatal(err)
	}
	listener.SetUnlinkOnClose(false)
	t.Cleanup(func() {
		_ = listener.Close()
		_ = os.Remove(socketPath)
	})
	if err := os.Chmod(socketPath, mode); err != nil {
		t.Fatal(err)
	}
}

func startBoundaryRuntime(t *testing.T, client *Client) (*RuntimeServer, string, func()) {
	t.Helper()
	root := boundaryRuntimeDirectory(t)
	socketPath := filepath.Join(root, "attachment.sock")
	server, err := ListenRuntime(RuntimeServerConfig{SocketPath: socketPath, Brief: boundaryBrief("task-boundary-0001"), Reporter: client})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- server.Serve(ctx) }()
	var stopped bool
	stop := func() {
		if stopped {
			return
		}
		stopped = true
		cancel()
		closeErr := server.Close()
		serveErr := <-done
		if closeErr == nil && serveErr != nil {
			t.Errorf("Serve stop = %v", serveErr)
		}
		if closeErr != nil && serveErr == nil {
			t.Errorf("Serve stop omitted close error %v", closeErr)
		}
	}
	t.Cleanup(stop)
	return server, socketPath, stop
}

func exchangeBoundaryRequest(t *testing.T, socketPath, request string) RuntimeOutcome {
	t.Helper()
	connection, err := net.Dial("unix", socketPath)
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	if _, err := connection.Write([]byte(request)); err != nil {
		t.Fatal(err)
	}
	var outcome RuntimeOutcome
	if err := json.NewDecoder(bufio.NewReader(connection)).Decode(&outcome); err != nil {
		t.Fatal(err)
	}
	return outcome
}

func startBoundaryResponder(t *testing.T, response []byte) (*RuntimeClient, func()) {
	t.Helper()
	root := boundaryRuntimeDirectory(t)
	socketPath := filepath.Join(root, "attachment.sock")
	address, err := net.ResolveUnixAddr("unix", socketPath)
	if err != nil {
		t.Fatal(err)
	}
	listener, err := net.ListenUnix("unix", address)
	if err != nil {
		t.Fatal(err)
	}
	listener.SetUnlinkOnClose(false)
	if err := os.Chmod(socketPath, 0o600); err != nil {
		t.Fatal(err)
	}
	client, err := NewRuntimeClient(socketPath, 200*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		connection, acceptErr := listener.AcceptUnix()
		if acceptErr != nil {
			return
		}
		_, _ = bufio.NewReader(connection).ReadString('\n')
		_, _ = connection.Write(response)
		_ = connection.Close()
	}()
	var stopped bool
	stop := func() {
		if stopped {
			return
		}
		stopped = true
		_ = listener.Close()
		<-done
		_ = os.Remove(socketPath)
	}
	t.Cleanup(stop)
	return client, stop
}
