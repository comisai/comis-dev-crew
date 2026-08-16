//go:build integration

package integration_test

import (
	"bufio"
	"bytes"
	"context"
	"database/sql"
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

	"github.com/comisai/comis-dev-crew/internal/application"
	"github.com/comisai/comis-dev-crew/internal/comiswire"
	"github.com/comisai/comis-dev-crew/internal/domain"
	"github.com/comisai/comis-dev-crew/internal/mcpadapter"
	"github.com/comisai/comis-dev-crew/internal/reporter"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	_ "modernc.org/sqlite"
)

const installedCredential = "installed_fixture_bearer_0123456789abcdef"

func TestInstalledComposition_JoinsMCPActivationAndReviewedCodexLaunchPlan(t *testing.T) {
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
	codexExecutable := installedCodexExecutable(t, root)
	candidateConfig := installedCandidateConfig(t, root)
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
		"--preparation-ttl", "10m",
		"--codex-profile", "codex-reviewed", "--codex-executable", codexExecutable,
		"--codex-version", "codex-cli 0.147.0", "--codex-model", "gpt-5.5-codex",
		"--codex-effort", "high", "--codex-terminal-allow-entry", "codex-confined",
		"--codex-network", "restricted", "--codex-concurrency", "2",
		"--candidate-config", candidateConfig,
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
			DeliveryMode: domain.DeliveryReport, WorkerProfileID: "codex-reviewed",
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
	line, err := peer.reader.ReadBytes('\n')
	if err != nil {
		t.Fatalf("read installed activation response: %v", err)
	}
	var response comiswire.ActivateResponse
	decodeJSON(t, line, &response)
	if response.ID != activation.ID || response.Result.ManagedRunID != activation.Params.ManagedRunID ||
		response.Result.ExternalRunRef != registration.ExternalRunRef || response.Result.State != comiswire.ManagedRunStateActive {
		t.Fatalf("installed activation response = %#v", response)
	}
	planResult, err := mcpSession.CallTool(context.Background(), &mcp.CallToolParams{
		Meta: parityCallMeta("installed-launch-plan-0001"), Name: mcpadapter.ToolGetLaunchPlan,
		Arguments: mcpadapter.TaskInput{TaskHandle: string(registration.ExternalRunRef)},
	})
	if err != nil || planResult.IsError {
		t.Fatalf("installed get_launch_plan = %#v, %v", planResult, err)
	}
	var plan application.LaunchPlan
	decodeStructured(t, planResult.StructuredContent, &plan)
	if plan.TaskHandle != string(registration.ExternalRunRef) || plan.State != domain.TaskReady ||
		plan.WorkerProfileID != "codex-reviewed" || plan.TerminalAllowEntryID != "codex-confined" ||
		plan.AttachmentTargetName != string(attachmentTarget) || plan.BriefRevisionHash == "" {
		t.Fatalf("installed launch plan = %#v", plan)
	}
	visible := string(encodeJSON(t, planResult.StructuredContent))
	for _, forbidden := range []string{workspace, codexExecutable, "/run/comis/attachments", string(attachmentID), "--model"} {
		if strings.Contains(visible, forbidden) {
			t.Fatalf("installed launch plan leaked %q: %s", forbidden, visible)
		}
	}
	terminalSessionID := comiswire.TerminalSessionID("terminal-session-installed")
	created := comiswire.TerminalEventRequest{
		JSONRPC: comiswire.JSONRPCVersion, ID: "installed-terminal-created-0001", Method: comiswire.MethodManagedRunsTerminalEvent,
		Params: comiswire.TerminalEventRequestParams{
			OperationID: "installed-terminal-created-0001", ManagedRunID: activation.Params.ManagedRunID,
			WorkspaceLeaseID: lease, TerminalSessionID: terminalSessionID,
			Transition: comiswire.CapabilityTerminalTransitionCreated,
		},
	}
	createdResponse := installedTerminalEvent(t, peer, created)
	if createdResponse.Result.ManagedRunID != activation.Params.ManagedRunID ||
		createdResponse.Result.TerminalSessionID != terminalSessionID ||
		createdResponse.Result.Transition != comiswire.CapabilityTerminalTransitionCreated {
		t.Fatalf("installed created acknowledgement = %#v", createdResponse)
	}
	if state := installedTaskState(t, mcpSession, "installed-task-launching-0001", string(registration.ExternalRunRef)); state != domain.TaskLaunching {
		t.Fatalf("installed task after created = %q, want launching", state)
	}
	recoveryPlanResult, err := mcpSession.CallTool(context.Background(), &mcp.CallToolParams{
		Meta: parityCallMeta("installed-launch-plan-recovery-0001"), Name: mcpadapter.ToolGetLaunchPlan,
		Arguments: mcpadapter.TaskInput{TaskHandle: string(registration.ExternalRunRef)},
	})
	if err != nil || recoveryPlanResult.IsError {
		t.Fatalf("installed launching get_launch_plan = %#v, %v", recoveryPlanResult, err)
	}
	var recoveryPlan application.LaunchPlan
	decodeStructured(t, recoveryPlanResult.StructuredContent, &recoveryPlan)
	if recoveryPlan.State != domain.TaskLaunching || recoveryPlan.TaskHandle != string(registration.ExternalRunRef) {
		t.Fatalf("installed launching recovery plan = %#v", recoveryPlan)
	}

	alteredCreated := created
	alteredCreated.Params.WorkspaceLeaseID = "workspace-lease-altered"
	alteredResponse := installedTerminalError(t, peer, alteredCreated)
	if alteredResponse.Error.Kind != comiswire.ErrorKindReplayConflict {
		t.Fatalf("installed altered created replay = %#v, want replay conflict", alteredResponse)
	}

	running := comiswire.TerminalEventRequest{
		JSONRPC: comiswire.JSONRPCVersion, ID: "installed-terminal-running-0001", Method: comiswire.MethodManagedRunsTerminalEvent,
		Params: comiswire.TerminalEventRequestParams{
			OperationID: "installed-terminal-running-0001", ManagedRunID: activation.Params.ManagedRunID,
			WorkspaceLeaseID: lease, TerminalSessionID: terminalSessionID,
			Transition: comiswire.CapabilityTerminalTransitionRunning,
		},
	}
	installedTerminalEvent(t, peer, running)
	if state := installedTaskState(t, mcpSession, "installed-task-running-0001", string(registration.ExternalRunRef)); state != domain.TaskLaunching {
		t.Fatalf("installed task after running without wrapper acknowledgement = %q, want launching", state)
	}
	if registration.RequestedAttachment == nil {
		t.Fatal("installed preparation omitted the reporter attachment")
	}
	relayIdentity := installedRuntimeRelayIdentity(t, database, string(registration.ExternalRunRef))
	runtimeClient, err := reporter.NewRuntimeClient(
		registration.RequestedAttachment.SourcePath, relayIdentity, time.Second,
	)
	if err != nil {
		t.Fatalf("open installed runtime attachment: %v", err)
	}
	if err := runtimeClient.Acknowledge(context.Background(), workspace); err != nil {
		t.Fatalf("acknowledge installed worker wrapper: %v", err)
	}
	if state := installedTaskState(t, mcpSession, "installed-task-working-0001", string(registration.ExternalRunRef)); state != domain.TaskWorking {
		t.Fatalf("installed task after joined launch evidence = %q, want working", state)
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

func installedRuntimeRelayIdentity(t *testing.T, databasePath, taskHandle string) string {
	t.Helper()
	database, err := sql.Open("sqlite", "file:"+databasePath+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	var identity string
	if err := database.QueryRowContext(
		context.Background(),
		"SELECT requested_attachment_relay_identity FROM task_preparations WHERE external_run_ref = ?",
		taskHandle,
	).Scan(&identity); err != nil {
		t.Fatal(err)
	}
	if application.ValidateRuntimeRelayIdentity(identity) != nil {
		t.Fatal("installed runtime relay identity is invalid")
	}
	return identity
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

type installedAuthenticatedTerminalEvent struct {
	comiswire.TerminalEventRequest
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

func installedTerminalEvent(t *testing.T, peer installedControlPeer, request comiswire.TerminalEventRequest) comiswire.TerminalEventResponse {
	t.Helper()
	if err := writeInstalledFrame(peer.connection, installedAuthenticatedTerminalEvent{
		TerminalEventRequest: request, Bearer: installedCredential,
	}); err != nil {
		t.Fatal(err)
	}
	line, err := peer.reader.ReadBytes('\n')
	if err != nil {
		t.Fatalf("read installed terminal response: %v", err)
	}
	var response comiswire.TerminalEventResponse
	decodeJSON(t, line, &response)
	if response.ID != request.ID {
		t.Fatalf("installed terminal response = %s", line)
	}
	return response
}

func installedTerminalError(t *testing.T, peer installedControlPeer, request comiswire.TerminalEventRequest) comiswire.ErrorResponse {
	t.Helper()
	if err := writeInstalledFrame(peer.connection, installedAuthenticatedTerminalEvent{
		TerminalEventRequest: request, Bearer: installedCredential,
	}); err != nil {
		t.Fatal(err)
	}
	line, err := peer.reader.ReadBytes('\n')
	if err != nil {
		t.Fatalf("read installed terminal error: %v", err)
	}
	var response comiswire.ErrorResponse
	decodeJSON(t, line, &response)
	if response.ID == nil || *response.ID != request.ID {
		t.Fatalf("installed terminal error = %s", line)
	}
	return response
}

func installedTaskState(t *testing.T, session *mcp.ClientSession, operationID, taskHandle string) domain.TaskState {
	t.Helper()
	result, err := session.CallTool(context.Background(), &mcp.CallToolParams{
		Meta: parityCallMeta(operationID), Name: mcpadapter.ToolGetTask,
		Arguments: mcpadapter.TaskInput{TaskHandle: taskHandle},
	})
	if err != nil || result.IsError {
		t.Fatalf("installed get_task = %#v, %v", result, err)
	}
	var detail application.TaskDetail
	decodeStructured(t, result.StructuredContent, &detail)
	return detail.Summary.State
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

func installedCodexExecutable(t *testing.T, root string) string {
	t.Helper()
	executable := filepath.Join(root, "bin", "codex")
	if err := os.MkdirAll(filepath.Dir(executable), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(executable, []byte("#!/bin/sh\nprintf 'codex-cli 0.147.0\\n'\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	return executable
}

func installedCandidateConfig(t *testing.T, root string) string {
	t.Helper()
	trueExecutable, err := exec.LookPath("true")
	if err != nil {
		t.Fatal(err)
	}
	trueExecutable, err = filepath.EvalSymlinks(trueExecutable)
	if err != nil {
		t.Fatal(err)
	}
	configRoot := filepath.Join(root, "config")
	credentialRoot := filepath.Join(root, "forge-credentials")
	if err := os.MkdirAll(credentialRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	readCredential := filepath.Join(configRoot, "forge-read.credential")
	pushCredential := filepath.Join(configRoot, "forge-push.credential")
	if err := os.WriteFile(readCredential, []byte("installed_forge_read_credential"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(pushCredential, []byte("installed_forge_push_credential"), 0o600); err != nil {
		t.Fatal(err)
	}
	document := map[string]any{
		"programs": []map[string]any{{"id": "repo-check", "executable": trueExecutable}},
		"profiles": []map[string]any{{
			"id": "go-default", "evidenceTtl": "24h",
			"localChecks": []map[string]any{{
				"id": "unit", "programId": "repo-check", "timeout": "1m", "required": true,
				"arguments": []map[string]any{{"kind": "literal", "value": "--version"}},
			}},
			"forgeChecks": []map[string]any{{"name": "ci/unit", "required": true}},
		}},
		"maxOutputBytes": 65536, "pollInterval": "250ms",
		"forge": map[string]any{
			"apiBaseUrl": "https://api.github.com", "owner": "comisai", "repository": "product-api",
			"remoteUrl":          "https://github.com/comisai/product-api.git",
			"readCredentialFile": readCredential, "pushCredentialFile": pushCredential,
			"credentialDirectory": credentialRoot,
		},
	}
	contents, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(configRoot, "candidate.json")
	if err := os.WriteFile(path, contents, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
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
