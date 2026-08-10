//go:build integration

package integration_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/comisai/comis-dev-crew/internal/comiswire"
	"github.com/comisai/comis-dev-crew/internal/domain"
	"github.com/comisai/comis-dev-crew/internal/localapi"
	"github.com/comisai/comis-dev-crew/internal/mcpadapter"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const installedCredential = "installed_fixture_bearer_0123456789abcdef"

func TestInstalledComposition_JoinsMCPActivationFixtureAndReports(t *testing.T) {
	root := integrationShortTempDir(t)
	gitExecutable := installedGitExecutable(t)
	approvedRoot, primary, worktreeRoot, baseRevision := installedRepository(t, root)
	runRoot := filepath.Join(root, "run")
	if err := os.MkdirAll(runRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(runRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	controlSocket := filepath.Join(runRoot, "comis.sock")
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: controlSocket, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	if err := os.Chmod(controlSocket, 0o600); err != nil {
		t.Fatal(err)
	}
	credentialFile := filepath.Join(root, "config", "comis.credential")
	if err := os.MkdirAll(filepath.Dir(credentialFile), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(credentialFile, []byte(installedCredential), 0o600); err != nil {
		t.Fatal(err)
	}

	peerReady := make(chan installedControlPeer, 1)
	hostError := make(chan error, 1)
	go acceptInstalledControl(listener, peerReady, hostError)

	serviceBinary := buildInstalledBinary(t, "devcrew-service")
	mcpBinary := buildInstalledBinary(t, "devcrew-mcp")
	operatorSocket := filepath.Join(runRoot, "operator.sock")
	mcpSocket := filepath.Join(runRoot, "mcp.sock")
	database := filepath.Join(root, "state", "devcrew.db")
	serviceStderr := &bytes.Buffer{}
	serviceCommand := exec.Command(serviceBinary,
		"--database", database, "--socket", operatorSocket, "--mcp-socket", mcpSocket,
		"--runtime-root", filepath.Join(runRoot, "tasks"),
		"--service-instance", parityServiceID,
		"--git-executable", gitExecutable, "--approved-root", approvedRoot,
		"--repository-id", "product-api", "--repository-primary", primary,
		"--worktree-root", worktreeRoot, "--repository-default-branch", "main",
		"--comis-socket", controlSocket, "--comis-credential-file", credentialFile,
		"--comis-handshake-operation", "installed-handshake-0001",
		"--preparation-ttl", "10m", "--fixture-worker",
		"--fixture-decision", "use the bounded fixture choice",
	)
	serviceCommand.Stderr = serviceStderr
	if err := serviceCommand.Start(); err != nil {
		t.Fatalf("start installed service: %v", err)
	}
	serviceStopped := false
	t.Cleanup(func() {
		if serviceStopped || serviceCommand.Process == nil {
			return
		}
		_ = serviceCommand.Process.Signal(syscall.SIGTERM)
		_ = serviceCommand.Wait()
	})

	var peer installedControlPeer
	select {
	case peer = <-peerReady:
	case err := <-hostError:
		t.Fatalf("installed Comis peer: %v; service stderr=%q", err, serviceStderr.String())
	case <-time.After(10 * time.Second):
		t.Fatalf("installed Comis handshake timed out; service stderr=%q", serviceStderr.String())
	}
	defer peer.connection.Close()
	waitForInstalledSocket(t, operatorSocket, serviceCommand, serviceStderr)
	waitForInstalledSocket(t, mcpSocket, serviceCommand, serviceStderr)

	mcpStderr := &bytes.Buffer{}
	mcpCommand := exec.Command(mcpBinary, "--socket", mcpSocket, "--service-instance", parityServiceID)
	mcpCommand.Stderr = mcpStderr
	mcpClient := mcp.NewClient(&mcp.Implementation{Name: "installed-composition-client", Version: "integration"}, nil)
	mcpSession, err := mcpClient.Connect(context.Background(), &mcp.CommandTransport{
		Command: mcpCommand, TerminateDuration: 100 * time.Millisecond,
	}, nil)
	if err != nil {
		t.Fatalf("connect installed MCP facade: %v, stderr=%q", err, mcpStderr.String())
	}
	t.Cleanup(func() { _ = mcpSession.Close() })
	prepared, err := mcpSession.CallTool(context.Background(), &mcp.CallToolParams{
		Meta: parityCallMeta("installed-prepare-0001"), Name: mcpadapter.ToolPrepareTask,
		Arguments: mcpadapter.PrepareTaskInput{
			Shape: domain.ShapeScout, RepositoryID: "product-api", BaseRevision: baseRevision,
			AcceptanceCriteria: []string{"Exercise the installed composition end to end."},
			Constraints:        []string{"Stop at a validation candidate."}, ValidationProfile: "go-default",
			DeliveryMode: domain.DeliveryReport, WorkerProfileID: "fixture-worker",
		},
	})
	if err != nil || prepared.IsError {
		t.Fatalf("installed prepare = %#v, %v, stderr=%q", prepared, err, mcpStderr.String())
	}
	var registration comiswire.MCPManagedRunResult
	decodeJSON(t, encodeJSON(t, prepared.Meta[mcpadapter.ManagedRunResultMetaKey]), &registration)
	workspace := filepath.Join(worktreeRoot, string(registration.ExternalRunRef))
	if registration.RequestedWorkspace == nil || registration.RequestedWorkspace.RootHint != workspace ||
		registration.State != comiswire.ManagedRunStatePrepared {
		t.Fatalf("installed preparation metadata = %#v", registration)
	}
	if marker, err := os.Lstat(filepath.Join(workspace, ".git")); err != nil || !marker.Mode().IsRegular() {
		t.Fatalf("installed preparation did not create a linked worktree: %v, %v", marker, err)
	}

	lease := comiswire.WorkspaceLeaseID("workspace-lease-installed")
	attachmentID := comiswire.ExecutionAttachmentID("execution-attachment-installed")
	attachmentTarget := comiswire.AttachmentTargetName("attachment-dddddddddddddddddddddddddddddddd.sock")
	activation := comiswire.ActivateRequest{
		JSONRPC: comiswire.JSONRPCVersion, ID: "installed-activate-0001", Method: comiswire.MethodManagedRunsActivate,
		Params: comiswire.ActivateRequestParams{
			OperationID: "installed-activate-0001", ManagedRunID: "managed-run-installed",
			ExternalRunRef: registration.ExternalRunRef, RegistrationNonce: registration.RegistrationNonce,
			WorkspaceLeaseID: &lease, ExecutionAttachmentID: &attachmentID, AttachmentTargetName: &attachmentTarget,
		},
	}
	if err := writeInstalledFrame(peer.connection, installedAuthenticatedActivate{ActivateRequest: activation, Bearer: installedCredential}); err != nil {
		t.Fatal(err)
	}
	wantKinds := []comiswire.ReportKind{
		comiswire.ReportKindProgress, comiswire.ReportKindAttention,
		comiswire.ReportKindResolution, comiswire.ReportKindCandidateComplete,
	}
	activated := false
	reports := make([]comiswire.ReportRequestParams, 0, len(wantKinds))
	for !activated || len(reports) != len(wantKinds) {
		line, err := peer.reader.ReadBytes('\n')
		if err != nil {
			t.Fatalf("read installed control frame: %v", err)
		}
		var header struct {
			Method *comiswire.Method `json:"method"`
		}
		if err := json.Unmarshal(line, &header); err != nil {
			t.Fatal(err)
		}
		if header.Method == nil {
			var response comiswire.ActivateResponse
			decodeJSON(t, line, &response)
			if response.ID != activation.ID || response.Result.ManagedRunID != activation.Params.ManagedRunID ||
				response.Result.ExternalRunRef != registration.ExternalRunRef || response.Result.State != comiswire.ManagedRunStateActive {
				t.Fatalf("installed activation response = %#v", response)
			}
			activated = true
			continue
		}
		if *header.Method != comiswire.MethodManagedRunsReport {
			t.Fatalf("unexpected installed control method %q", *header.Method)
		}
		var report installedAuthenticatedReport
		decodeJSON(t, line, &report)
		if report.Bearer != installedCredential || report.Params.ManagedRunID != activation.Params.ManagedRunID {
			t.Fatalf("installed report authority = %#v", report)
		}
		reports = append(reports, report.Params)
		ack := comiswire.ReportResponse{
			JSONRPC: comiswire.JSONRPCVersion, ID: report.ID,
			Result: comiswire.ReportResponseResult{
				ManagedRunID: report.Params.ManagedRunID, ServiceReportID: report.Params.ServiceReportID,
				AcceptedSequence: int64(len(reports)), RetainedUntilMs: time.Now().UTC().Add(30 * 24 * time.Hour).UnixMilli(),
			},
		}
		if err := writeInstalledFrame(peer.connection, ack); err != nil {
			t.Fatal(err)
		}
	}
	for index, report := range reports {
		if report.Kind != wantKinds[index] {
			t.Fatalf("installed report %d kind = %q, want %q", index+1, report.Kind, wantKinds[index])
		}
	}

	operator, err := localapi.NewClient(operatorSocket, 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		detail, showErr := operator.ShowTask(context.Background(), "installed-show-0001", string(registration.ExternalRunRef))
		if showErr == nil && detail.Summary.State == domain.TaskValidating && detail.ReportCursor == 4 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("installed task did not reach validating: %#v, %v", detail, showErr)
		}
		time.Sleep(10 * time.Millisecond)
	}

	if err := mcpSession.Close(); err != nil {
		t.Fatal(err)
	}
	if err := serviceCommand.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	if err := serviceCommand.Wait(); err != nil {
		t.Fatalf("installed service stop: %v, stderr=%q", err, serviceStderr.String())
	}
	serviceStopped = true
	select {
	case err := <-hostError:
		if err != nil && !errors.Is(err, net.ErrClosed) {
			t.Fatalf("installed Comis host error: %v", err)
		}
	default:
	}
}

type installedControlPeer struct {
	connection *net.UnixConn
	reader     *bufio.Reader
}

type installedAuthenticatedHandshake struct {
	comiswire.HandshakeRequest
	Bearer string `json:"bearer"`
}

type installedAuthenticatedActivate struct {
	comiswire.ActivateRequest
	Bearer string `json:"bearer"`
}

type installedAuthenticatedReport struct {
	comiswire.ReportRequest
	Bearer string `json:"bearer"`
}

func acceptInstalledControl(listener *net.UnixListener, ready chan<- installedControlPeer, result chan<- error) {
	connection, err := listener.AcceptUnix()
	if err != nil {
		result <- err
		return
	}
	reader := bufio.NewReaderSize(connection, comiswire.MaxLineBytes+1)
	line, err := reader.ReadBytes('\n')
	if err != nil {
		_ = connection.Close()
		result <- err
		return
	}
	var handshake installedAuthenticatedHandshake
	if err := json.Unmarshal(line, &handshake); err != nil {
		_ = connection.Close()
		result <- err
		return
	}
	if handshake.Bearer != installedCredential || handshake.Params.ProtocolID != comiswire.ProtocolID ||
		handshake.Params.BundleDigest != comiswire.BundleDigest || handshake.Params.ServiceInstanceID != parityServiceID {
		_ = connection.Close()
		result <- errors.New("installed handshake authority differs")
		return
	}
	response := comiswire.HandshakeResponse{
		JSONRPC: comiswire.JSONRPCVersion, ID: handshake.ID,
		Result: comiswire.HandshakeResponseResult{
			ProtocolID: comiswire.ProtocolID, BundleDigest: comiswire.BundleDigest,
			ServiceInstanceID: handshake.Params.ServiceInstanceID,
			ActiveScopes:      append([]comiswire.ServiceScope(nil), handshake.Params.RequestedScopes...),
			Limits: comiswire.ProtocolLimits{
				MaxEvidenceBytes: comiswire.MaxEvidenceBytes, MaxInFlightRequests: comiswire.MaxInFlightRequests,
				MaxLineBytes: comiswire.MaxLineBytes, MaxReportBytes: comiswire.MaxReportBytes,
				MaxRequestBytes: comiswire.MaxRequestBytes, MaxResponseBytes: comiswire.MaxResponseBytes,
				ReportRetentionDays: comiswire.ReportRetentionDays,
			},
		},
	}
	if err := writeInstalledFrame(connection, response); err != nil {
		_ = connection.Close()
		result <- err
		return
	}
	ready <- installedControlPeer{connection: connection, reader: reader}
}

func writeInstalledFrame(connection net.Conn, value any) error {
	encoded, err := json.Marshal(value)
	if err != nil {
		return err
	}
	encoded = append(encoded, '\n')
	_, err = connection.Write(encoded)
	return err
}

func buildInstalledBinary(t *testing.T, name string) string {
	t.Helper()
	binary := filepath.Join(t.TempDir(), name)
	command := exec.Command("go", "build", "-trimpath", "-o", binary, "./cmd/"+name)
	command.Dir = integrationRepositoryRoot(t)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("build %s: %v\n%s", name, err, output)
	}
	return binary
}

func installedGitExecutable(t *testing.T) string {
	t.Helper()
	executable, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	absolute, err := filepath.Abs(executable)
	if err != nil {
		t.Fatal(err)
	}
	canonical, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		t.Fatal(err)
	}
	return canonical
}

func installedRepository(t *testing.T, root string) (string, string, string, string) {
	t.Helper()
	approvedRoot := filepath.Join(root, "repositories")
	primary := filepath.Join(approvedRoot, "product-api")
	worktreeRoot := filepath.Join(approvedRoot, "worktrees")
	if err := os.MkdirAll(primary, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(worktreeRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	runGit(t, primary, "init", "--initial-branch=main")
	runGit(t, primary, "config", "user.name", "Installed Fixture")
	runGit(t, primary, "config", "user.email", "fixture@example.invalid")
	if err := os.WriteFile(filepath.Join(primary, "README.md"), []byte("installed fixture\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runGit(t, primary, "add", "README.md")
	runGit(t, primary, "commit", "-m", "fixture")
	baseRevision := strings.TrimSpace(runGit(t, primary, "rev-parse", "HEAD"))
	return approvedRoot, primary, worktreeRoot, baseRevision
}

func waitForInstalledSocket(t *testing.T, path string, command *exec.Cmd, stderr *bytes.Buffer) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		if info, err := os.Lstat(path); err == nil && info.Mode()&os.ModeSocket != 0 {
			return
		}
		if command.ProcessState != nil {
			t.Fatalf("installed service stopped before socket %s: %q", filepath.Base(path), stderr.String())
		}
		if time.Now().After(deadline) {
			t.Fatalf("installed socket %s timed out; stderr=%q", filepath.Base(path), stderr.String())
		}
		time.Sleep(10 * time.Millisecond)
	}
}
