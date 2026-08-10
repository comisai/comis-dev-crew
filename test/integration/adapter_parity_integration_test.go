//go:build integration

package integration_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/comisai/comis-dev-crew/internal/application"
	"github.com/comisai/comis-dev-crew/internal/cli"
	"github.com/comisai/comis-dev-crew/internal/comiswire"
	"github.com/comisai/comis-dev-crew/internal/domain"
	"github.com/comisai/comis-dev-crew/internal/localapi"
	"github.com/comisai/comis-dev-crew/internal/mcpadapter"
	"github.com/comisai/comis-dev-crew/internal/service"
	"github.com/comisai/comis-dev-crew/internal/store/sqlite"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const parityOperationID = "parity-prepare-0001"
const parityServiceID = "service-instance-parity"
const parityLaunchTaskHandle = "task-parity-launch-0001"

func TestAdapterParity_PrepareReplayAndErrorsMatchAcrossDirectCLIAndMCP(t *testing.T) {
	harness := newParityHarness(t)
	direct, err := harness.operator.PrepareTask(context.Background(), parityOperationID, parityPrepareInput())
	if err != nil {
		t.Fatalf("direct PrepareTask() error = %v", err)
	}

	cliStdout, cliStderr, exit := harness.runCLI([]string{
		"--socket", harness.operatorSocket, "task", "prepare", "--input", "-",
		"--operation", parityOperationID, "--format", "json",
	}, parityOperationID, encodeJSON(t, parityPrepareInput()))
	if exit != cli.ExitSuccess || cliStderr != "" {
		t.Fatalf("CLI prepare exit=%d stdout=%q stderr=%q", exit, cliStdout, cliStderr)
	}
	var cliResult localapi.PrepareTaskResult
	decodeJSON(t, []byte(cliStdout), &cliResult)

	mcpResult, err := harness.mcpSession.CallTool(context.Background(), &mcp.CallToolParams{
		Meta: parityCallMeta(parityOperationID), Name: mcpadapter.ToolPrepareTask,
		Arguments: parityMCPPrepareInput(),
	})
	if err != nil || mcpResult.IsError {
		t.Fatalf("MCP prepare = %#v, %v", mcpResult, err)
	}
	var mcpOutput mcpadapter.PrepareTaskOutput
	decodeStructured(t, mcpResult.StructuredContent, &mcpOutput)

	want := normalizeLocalResult(direct)
	if got := normalizeLocalResult(cliResult); got != want {
		t.Fatalf("CLI normalized = %#v, want %#v", got, want)
	}
	if got := normalizeMCPResult(mcpOutput); got != want {
		t.Fatalf("MCP normalized = %#v, want %#v", got, want)
	}
	assertPrivatePreparationParity(t, direct, mcpResult)
	if direct.SideEffect != localapi.MethodPrepareTask.SideEffect() || direct.SideEffect != localapi.SideEffectMutate {
		t.Fatalf("prepare side effect = %q", direct.SideEffect)
	}
	assertToolReadOnly(t, harness.mcpSession, mcpadapter.ToolPrepareTask, false)

	listed, err := harness.operator.ListTasks(context.Background(), "parity-list-count")
	if err != nil || len(listed.Tasks) != 1 || listed.Tasks[0].TaskHandle != direct.TaskHandle {
		t.Fatalf("task list after three replays = %#v, %v", listed, err)
	}

	altered := parityPrepareInput()
	altered.AcceptanceCriteria = []string{"A changed subject must conflict."}
	_, directErr := harness.operator.PrepareTask(context.Background(), parityOperationID, altered)
	directCode := failureCode(t, directErr)
	_, cliError, cliExit := harness.runCLI([]string{
		"--socket", harness.operatorSocket, "task", "prepare", "--input", "-",
		"--operation", parityOperationID, "--format", "json",
	}, parityOperationID, encodeJSON(t, altered))
	mcpError, err := harness.mcpSession.CallTool(context.Background(), &mcp.CallToolParams{
		Meta: parityCallMeta(parityOperationID), Name: mcpadapter.ToolPrepareTask,
		Arguments: parityMCPInput(altered),
	})
	if err != nil || mcpError == nil || !mcpError.IsError {
		t.Fatalf("MCP altered replay = %#v, %v", mcpError, err)
	}
	if directCode != domain.ErrorConflict || cliExit != cli.ExitRejected ||
		!strings.Contains(cliError, string(directCode)) ||
		!strings.HasPrefix(mcpError.Content[0].(*mcp.TextContent).Text, string(directCode)+":") {
		t.Fatalf("error parity direct=%s CLI=%d/%q MCP=%q", directCode, cliExit, cliError, mcpError.Content[0].(*mcp.TextContent).Text)
	}
}

func TestAdapterParity_ReadOutcomesAndClassificationsMatch(t *testing.T) {
	harness := newParityHarness(t)
	prepared, err := harness.operator.PrepareTask(context.Background(), parityOperationID, parityPrepareInput())
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name      string
		operation string
		method    localapi.Method
		tool      string
		cliArgs   []string
		mcpArgs   any
		direct    func(string) (any, error)
		newResult func() any
	}{
		{
			name: "list", operation: "parity-list-0001", method: localapi.MethodListTasks, tool: mcpadapter.ToolListTasks,
			cliArgs: []string{"--socket", harness.operatorSocket, "tasks", "list", "--format", "json"},
			mcpArgs: mcpadapter.EmptyInput{},
			direct: func(operation string) (any, error) {
				return harness.operator.ListTasks(context.Background(), operation)
			},
			newResult: func() any { return &application.TaskList{} },
		},
		{
			name: "get", operation: "parity-get-0001", method: localapi.MethodShowTask, tool: mcpadapter.ToolGetTask,
			cliArgs: []string{"--socket", harness.operatorSocket, "task", "show", prepared.TaskHandle, "--format", "json"},
			mcpArgs: mcpadapter.TaskInput{TaskHandle: prepared.TaskHandle},
			direct: func(operation string) (any, error) {
				return harness.operator.ShowTask(context.Background(), operation, prepared.TaskHandle)
			},
			newResult: func() any { return &application.TaskDetail{} },
		},
		{
			name: "explain", operation: "parity-explain-0001", method: localapi.MethodExplainTask, tool: mcpadapter.ToolExplainTask,
			cliArgs: []string{"--socket", harness.operatorSocket, "task", "explain", prepared.TaskHandle, "--format", "json"},
			mcpArgs: mcpadapter.TaskInput{TaskHandle: prepared.TaskHandle},
			direct: func(operation string) (any, error) {
				return harness.operator.ExplainTask(context.Background(), operation, prepared.TaskHandle)
			},
			newResult: func() any { return &application.TaskExplanation{} },
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			direct, err := test.direct(test.operation)
			if err != nil {
				t.Fatalf("direct error = %v", err)
			}
			cliOutput, cliError, exit := harness.runCLI(test.cliArgs, test.operation, nil)
			if exit != cli.ExitSuccess || cliError != "" {
				t.Fatalf("CLI exit=%d output=%q error=%q", exit, cliOutput, cliError)
			}
			cliResult := test.newResult()
			decodeJSON(t, []byte(cliOutput), cliResult)
			mcpResult, err := harness.mcpSession.CallTool(context.Background(), &mcp.CallToolParams{
				Meta: parityCallMeta(test.operation), Name: test.tool, Arguments: test.mcpArgs,
			})
			if err != nil || mcpResult.IsError {
				t.Fatalf("MCP result = %#v, %v", mcpResult, err)
			}
			mcpOutput := test.newResult()
			decodeStructured(t, mcpResult.StructuredContent, mcpOutput)
			if !reflect.DeepEqual(cliResult, pointerTo(direct)) || !reflect.DeepEqual(mcpOutput, pointerTo(direct)) {
				t.Fatalf("parity mismatch direct=%#v CLI=%#v MCP=%#v", direct, cliResult, mcpOutput)
			}
			if test.method.SideEffect() != localapi.SideEffectRead {
				t.Fatalf("method %s side effect = %s", test.method, test.method.SideEffect())
			}
			assertToolReadOnly(t, harness.mcpSession, test.tool, true)
		})
	}
}

func TestAdapterParity_LaunchPlanMatchesDirectCLIJSONAndMCPWithoutProcessMaterial(t *testing.T) {
	harness := newLaunchPlanParityHarness(t)
	direct, err := harness.operator.GetLaunchPlan(context.Background(), "parity-launch-direct", parityLaunchTaskHandle)
	if err != nil {
		t.Fatalf("direct GetLaunchPlan() error = %v", err)
	}
	cliOutput, cliError, exit := harness.runCLI([]string{
		"--socket", harness.operatorSocket, "task", "launch-plan", parityLaunchTaskHandle, "--format", "json",
	}, "parity-launch-cli", nil)
	if exit != cli.ExitSuccess || cliError != "" {
		t.Fatalf("CLI launch-plan exit=%d output=%q error=%q", exit, cliOutput, cliError)
	}
	var cliPlan application.LaunchPlan
	decodeJSON(t, []byte(cliOutput), &cliPlan)
	mcpResult, err := harness.mcpSession.CallTool(context.Background(), &mcp.CallToolParams{
		Meta: parityCallMeta("parity-launch-mcp"), Name: mcpadapter.ToolGetLaunchPlan,
		Arguments: mcpadapter.TaskInput{TaskHandle: parityLaunchTaskHandle},
	})
	if err != nil || mcpResult.IsError {
		t.Fatalf("MCP get_launch_plan = %#v, %v", mcpResult, err)
	}
	var mcpPlan application.LaunchPlan
	decodeStructured(t, mcpResult.StructuredContent, &mcpPlan)
	if !reflect.DeepEqual(cliPlan, direct) || !reflect.DeepEqual(mcpPlan, direct) {
		t.Fatalf("launch-plan parity mismatch direct=%#v CLI=%#v MCP=%#v", direct, cliPlan, mcpPlan)
	}
	encoded := string(encodeJSON(t, direct)) + cliOutput + string(encodeJSON(t, mcpResult.StructuredContent))
	for _, forbidden := range []string{"/private/", "/run/comis/attachments", "--model", "DEV_CREW_ATTACHMENT", "execution-attachment-parity"} {
		if strings.Contains(encoded, forbidden) {
			t.Fatalf("launch-plan parity fixture leaked %q: %s", forbidden, encoded)
		}
	}
	if localapi.MethodGetLaunchPlan.SideEffect() != localapi.SideEffectRead {
		t.Fatalf("GetLaunchPlan side effect = %s", localapi.MethodGetLaunchPlan.SideEffect())
	}
	assertToolReadOnly(t, harness.mcpSession, mcpadapter.ToolGetLaunchPlan, true)
}

type parityHarness struct {
	operatorSocket string
	operator       *localapi.Client
	mcpSession     *mcp.ClientSession
}

func newParityHarness(t *testing.T) *parityHarness {
	return newParityHarnessConfigured(t, false)
}

func newLaunchPlanParityHarness(t *testing.T) *parityHarness {
	return newParityHarnessConfigured(t, true)
}

func newParityHarnessConfigured(t *testing.T, seedLaunchPlan bool) *parityHarness {
	t.Helper()
	root := integrationShortTempDir(t)
	databasePath := filepath.Join(root, "state", "devcrew.db")
	operatorSocket := filepath.Join(root, "run", "operator.sock")
	mcpSocket := filepath.Join(root, "run", "mcp.sock")
	ready := make(chan struct{})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	now := time.Now().UTC()
	clock := func() time.Time { return now }
	if seedLaunchPlan {
		seedParityLaunchTask(t, databasePath, root, clock)
	}
	go func() {
		done <- service.Run(ctx, service.Config{
			DatabasePath: databasePath,
			SocketPath:   operatorSocket, MCPSocketPath: mcpSocket, ServiceInstanceID: parityServiceID,
			Repositories:       parityRepositoryCatalog{},
			Workspaces:         parityWorkspacePreparer{root: filepath.Join(root, "worktrees", "task-parity-0001")},
			RuntimeAttachments: integrationRuntimeAttachments{},
			TaskIDs:            func(string) (string, error) { return "task-parity-0001", nil },
			RegistrationNonces: func() (string, error) { return "registration-nonce_parity", nil },
			PreparationTTL:     time.Hour, Clock: clock, Ready: func() { close(ready) },
			WorkerHarnesses: parityLaunchHarnesses{},
		})
	}()
	select {
	case <-ready:
	case err := <-done:
		cancel()
		t.Fatalf("service stopped before ready: %v", err)
	case <-time.After(5 * time.Second):
		cancel()
		t.Fatal("service readiness timed out")
	}
	t.Cleanup(func() {
		cancel()
		if err := <-done; err != nil {
			t.Errorf("service stop error = %v", err)
		}
	})
	operator, err := localapi.NewClient(operatorSocket, 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	mcpLocal, err := localapi.NewClient(mcpSocket, 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	facade, err := mcpadapter.New(mcpadapter.Config{
		Client: mcpLocal, ServiceInstanceID: parityServiceID, Version: "integration",
		NewOperationID: func() (string, error) { return "parity-reconcile-0001", nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	clientTransport, serverTransport := mcp.NewInMemoryTransports()
	serverSession, err := facade.Server().Connect(context.Background(), serverTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	mcpClient := mcp.NewClient(&mcp.Implementation{Name: "parity-client", Version: "1"}, nil)
	mcpSession, err := mcpClient.Connect(context.Background(), clientTransport, nil)
	if err != nil {
		_ = serverSession.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = mcpSession.Close(); _ = serverSession.Close() })
	return &parityHarness{operatorSocket: operatorSocket, operator: operator, mcpSession: mcpSession}
}

func seedParityLaunchTask(t *testing.T, databasePath, root string, clock application.Clock) {
	t.Helper()
	store, err := sqlite.Open(context.Background(), databasePath)
	if err != nil {
		t.Fatalf("open launch-plan seed store: %v", err)
	}
	defer func() {
		if err := store.Close(); err != nil {
			t.Fatalf("close launch-plan seed store: %v", err)
		}
	}()
	mutations, err := application.NewMutations(application.MutationConfig{
		Store: store, Repositories: parityRepositoryCatalog{},
		Workspaces:         parityWorkspacePreparer{root: filepath.Join(root, "worktrees", parityLaunchTaskHandle)},
		RuntimeAttachments: integrationRuntimeAttachments{},
		TaskIDs:            func(string) (string, error) { return parityLaunchTaskHandle, nil },
		RegistrationNonces: func() (string, error) { return "registration-nonce_launch-parity", nil },
		PreparationTTL:     time.Hour, Clock: clock,
	})
	if err != nil {
		t.Fatalf("create launch-plan seed mutations: %v", err)
	}
	prepared, err := mutations.PrepareTask(context.Background(), application.PrepareTaskCommand{
		OperationID: "parity-launch-prepare", ServiceInstanceID: parityServiceID,
		Shape: domain.ShapeScout, RepositoryID: "product-api", BaseRevision: strings.Repeat("c", 40),
		AcceptanceCriteria: []string{"Return a safe launch plan."}, ValidationProfile: "go-default",
		DeliveryMode: domain.DeliveryReport, WorkerProfileID: "codex-reviewed",
	})
	if err != nil {
		t.Fatalf("prepare launch-plan seed: %v", err)
	}
	_, err = mutations.ActivateManagedRun(context.Background(), application.ActivateManagedRunCommand{
		OperationID: "parity-launch-activate", ServiceInstanceID: parityServiceID,
		ManagedRunID: "managed-run-launch-parity", ExternalRunRef: prepared.Task.Handle,
		RegistrationNonce:     prepared.Preparation.RegistrationNonce,
		WorkspaceLeaseID:      "workspace-lease-launch-parity",
		ExecutionAttachmentID: "execution-attachment-parity",
		AttachmentTargetName:  "attachment-bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb.sock",
	})
	if err != nil {
		t.Fatalf("activate launch-plan seed: %v", err)
	}
}

type parityLaunchHarnesses struct{}

func (parityLaunchHarnesses) ResolveWorkerHarness(string) (application.WorkerHarnessAdapter, error) {
	return parityLaunchAdapter{}, nil
}

type parityLaunchAdapter struct{}

func (parityLaunchAdapter) ID() string { return "codex" }

func (parityLaunchAdapter) ProbeVersion(context.Context) (application.HarnessVersionProbe, error) {
	return application.HarnessVersionProbe{Version: "codex-cli 1.2.3", Availability: application.HarnessAvailable}, nil
}

func (parityLaunchAdapter) BuildLaunchDescriptor(
	_ context.Context,
	request application.WorkerLaunchRequest,
) (application.WorkerLaunchDescriptor, error) {
	return application.WorkerLaunchDescriptor{
		ProfileID: request.ProfileID, TerminalAllowEntry: "terminal-codex-reviewed",
		Attachment: request.Attachment,
		ExpectedAcknowledgement: application.LaunchAcknowledgement{
			TaskHandle: request.TaskHandle, ManagedRunID: request.ManagedRunID,
			WorkspaceLeaseID: request.WorkspaceLeaseID, WorkingDirectory: request.WorkingDirectory,
			BriefRevision: request.BriefRevision, BriefRevisionHash: request.BriefRevisionHash,
		},
	}, nil
}

func (parityLaunchAdapter) ClassifySemanticActivity(application.HarnessObservation) application.SemanticActivityResult {
	return application.SemanticActivityResult{State: application.ActivityUnknown, Reason: application.SemanticReasonMissing}
}

func (harness *parityHarness) runCLI(args []string, operation string, stdin []byte) (string, string, int) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exit := cli.Run(context.Background(), args, &stdout, &stderr, cli.Config{
		DefaultSocketPath: harness.operatorSocket, Version: "integration",
		NewClient:      func(path string) (cli.ReadClient, error) { return localapi.NewClient(path, 2*time.Second) },
		NewOperationID: func() (string, error) { return operation, nil }, Stdin: bytes.NewReader(stdin),
	})
	return stdout.String(), stderr.String(), exit
}

type normalizedPreparation struct {
	SchemaVersion int
	OperationID   string
	TaskHandle    string
	State         domain.TaskState
	StateVersion  int64
	SideEffect    localapi.SideEffectClass
}

func normalizeLocalResult(result localapi.PrepareTaskResult) normalizedPreparation {
	return normalizedPreparation{result.SchemaVersion, result.OperationID, result.TaskHandle, result.State, result.StateVersion, result.SideEffect}
}

func normalizeMCPResult(result mcpadapter.PrepareTaskOutput) normalizedPreparation {
	return normalizedPreparation{result.SchemaVersion, result.OperationID, result.TaskHandle, result.State, result.StateVersion, result.SideEffect}
}

func parityPrepareInput() localapi.PrepareTaskInput {
	return localapi.PrepareTaskInput{
		Shape: domain.ShapeScout, RepositoryID: "product-api", BaseRevision: strings.Repeat("a", 40),
		AcceptanceCriteria: []string{"Prove adapter parity."}, Constraints: []string{"Do not deliver."},
		ValidationProfile: "go-default", DeliveryMode: domain.DeliveryReport, WorkerProfileID: "fixture-worker",
	}
}

func parityMCPPrepareInput() mcpadapter.PrepareTaskInput { return parityMCPInput(parityPrepareInput()) }

func parityMCPInput(input localapi.PrepareTaskInput) mcpadapter.PrepareTaskInput {
	return mcpadapter.PrepareTaskInput{
		Shape: input.Shape, RepositoryID: input.RepositoryID, BaseRevision: input.BaseRevision,
		AcceptanceCriteria: input.AcceptanceCriteria, Constraints: input.Constraints,
		ValidationProfile: input.ValidationProfile, DeliveryMode: input.DeliveryMode,
		WorkerProfileID: input.WorkerProfileID,
	}
}

func parityCallMeta(operation string) mcp.Meta {
	return mcp.Meta{mcpadapter.CallContextMetaKey: map[string]any{
		"operationId": operation, "serviceInstanceId": parityServiceID,
		"agentId": "agent-parity", "conversationRef": "conversation-parity",
		"workspacePolicyHash": strings.Repeat("b", 64), "rootRunId": "root-run-parity",
		"traceId": "trace-parity",
	}}
}

func assertPrivatePreparationParity(t *testing.T, direct localapi.PrepareTaskResult, result *mcp.CallToolResult) {
	t.Helper()
	encoded := encodeJSON(t, result.Meta[mcpadapter.ManagedRunResultMetaKey])
	if err := comiswire.ValidatePayload(comiswire.PayloadMCPManagedRunResult, encoded); err != nil {
		t.Fatalf("private extension invalid: %v", err)
	}
	var extension comiswire.MCPManagedRunResult
	decodeJSON(t, encoded, &extension)
	if string(extension.ExternalRunRef) != direct.ManagedRun.ExternalRunRef ||
		string(extension.RegistrationNonce) != direct.ManagedRun.RegistrationNonce ||
		extension.ExpiresAt != direct.ManagedRun.ExpiresAt.Format(time.RFC3339Nano) {
		t.Fatalf("private extension = %#v, want %#v", extension, direct.ManagedRun)
	}
}

func assertToolReadOnly(t *testing.T, session *mcp.ClientSession, name string, want bool) {
	t.Helper()
	tools, err := session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, tool := range tools.Tools {
		if tool.Name == name {
			if tool.Annotations == nil || tool.Annotations.ReadOnlyHint != want {
				t.Fatalf("tool %s annotations = %#v, want readOnly=%v", name, tool.Annotations, want)
			}
			return
		}
	}
	t.Fatalf("tool %s is missing", name)
}

func failureCode(t *testing.T, err error) domain.ErrorCode {
	t.Helper()
	var failure *domain.Failure
	if !errors.As(err, &failure) {
		t.Fatalf("error = %v, want domain failure", err)
	}
	return failure.Code
}

func decodeStructured(t *testing.T, value any, destination any) {
	t.Helper()
	decodeJSON(t, encodeJSON(t, value), destination)
}

func encodeJSON(t *testing.T, value any) []byte {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("encode JSON: %v", err)
	}
	return encoded
}

func decodeJSON(t *testing.T, encoded []byte, destination any) {
	t.Helper()
	if err := json.Unmarshal(encoded, destination); err != nil {
		t.Fatalf("decode JSON %q: %v", encoded, err)
	}
}

func pointerTo(value any) any {
	switch typed := value.(type) {
	case application.TaskList:
		return &typed
	case application.TaskDetail:
		return &typed
	case application.TaskExplanation:
		return &typed
	case application.LaunchPlan:
		return &typed
	default:
		panic(fmt.Sprintf("unknown parity value %T", value))
	}
}

type parityRepositoryCatalog struct{}

func (parityRepositoryCatalog) ValidateRepository(context.Context, string) error { return nil }

type parityWorkspacePreparer struct{ root string }

func (preparer parityWorkspacePreparer) PrepareWorkspace(
	context.Context,
	application.WorkspacePreparationRequest,
) (application.PreparedWorkspace, error) {
	return application.PreparedWorkspace{CanonicalRoot: preparer.root}, nil
}
