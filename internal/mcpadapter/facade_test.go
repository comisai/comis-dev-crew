package mcpadapter

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/comisai/comis-dev-crew/internal/application"
	"github.com/comisai/comis-dev-crew/internal/comiswire"
	"github.com/comisai/comis-dev-crew/internal/domain"
	"github.com/comisai/comis-dev-crew/internal/localapi"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestFacade_OfficialSDKCatalogAndPrivatePreparation(t *testing.T) {
	client := &fakeClient{prepareResults: []localapi.PrepareTaskResult{preparedResult()}}
	facade, err := New(Config{
		Client: client, ServiceInstanceID: "service-instance-0001", Version: "test",
		NewOperationID: func() (string, error) { return "reconcile-0001", nil },
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	session := connectFacade(t, facade)
	tools, err := session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("ListTools() error = %v", err)
	}
	assertToolCatalog(t, tools.Tools)

	result, err := session.CallTool(context.Background(), &mcp.CallToolParams{
		Meta: callMeta("prepare-0001", "service-instance-0001"), Name: ToolPrepareTask,
		Arguments: prepareInput(),
	})
	if err != nil || result.IsError {
		t.Fatalf("CallTool(prepare_task) = %#v, %v", result, err)
	}
	visible, err := json.Marshal(result.StructuredContent)
	if err != nil {
		t.Fatal(err)
	}
	for _, private := range []string{"managedRun", "registration-nonce_private", "expiresAt", "/approved/workspaces/task-0001", "/approved/runtime/task-0001/attachment.sock"} {
		if strings.Contains(string(visible), private) {
			t.Fatalf("visible result leaked %q: %s", private, visible)
		}
	}
	if len(result.Content) != 1 || strings.Contains(result.Content[0].(*mcp.TextContent).Text, "registration-nonce_private") {
		t.Fatalf("content leaked private preparation: %#v", result.Content)
	}
	extension, ok := result.Meta[ManagedRunResultMetaKey]
	if !ok {
		t.Fatalf("result metadata = %#v, want %s", result.Meta, ManagedRunResultMetaKey)
	}
	encodedExtension, err := json.Marshal(extension)
	if err != nil || comiswire.ValidatePayload(comiswire.PayloadMCPManagedRunResult, encodedExtension) != nil {
		t.Fatalf("managed-run extension = %s, %v", encodedExtension, err)
	}
	var prepared comiswire.MCPManagedRunResult
	if err := json.Unmarshal(encodedExtension, &prepared); err != nil || prepared.RequestedWorkspace == nil ||
		prepared.RequestedWorkspace.RootHint != "/approved/workspaces/task-0001" || prepared.RequestedAttachment == nil ||
		prepared.RequestedAttachment.Kind != comiswire.ExecutionAttachmentKindUnixSocket ||
		prepared.RequestedAttachment.SourcePath != "/approved/runtime/task-0001/attachment.sock" {
		t.Fatalf("managed-run requested resources = workspace:%#v attachment:%#v, %v", prepared.RequestedWorkspace, prepared.RequestedAttachment, err)
	}
	if got := strings.Join(client.calls, ","); got != "prepare:prepare-0001" {
		t.Fatalf("local calls = %q, want one canonical prepare", got)
	}
}

func TestFacade_PrepareTaskSchemaExplainsEveryClosedArgument(t *testing.T) {
	client := &fakeClient{}
	facade, err := New(Config{
		Client: client, ServiceInstanceID: "service-instance-0001", Version: "test",
		NewOperationID: func() (string, error) { return "reconcile-0001", nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	tools, err := connectFacade(t, facade).ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, listed := range tools.Tools {
		if listed.Name != ToolPrepareTask {
			continue
		}
		schema, marshalErr := json.Marshal(listed.InputSchema)
		if marshalErr != nil {
			t.Fatal(marshalErr)
		}
		visible := listed.Description + "\n" + string(schema)
		for _, required := range []string{
			"acceptanceCriteria and constraints must be JSON arrays",
			"ordered acceptance criteria",
			"ordered task constraints",
			"ship or scout",
			"pull_request for ship or report for scout",
			"exact 40-character lowercase hexadecimal Git revision",
		} {
			if !strings.Contains(visible, required) {
				t.Fatalf("prepare_task model contract omitted %q: %s", required, visible)
			}
		}
		return
	}
	t.Fatal("prepare_task tool is absent")
}

func TestFacade_ReadToolsUseOneCanonicalCommandAndIgnoreForgedRunMetadata(t *testing.T) {
	client := &fakeClient{}
	facade, err := New(Config{
		Client: client, ServiceInstanceID: "service-instance-0001", Version: "test",
		NewOperationID: func() (string, error) { return "reconcile-0001", nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	session := connectFacade(t, facade)
	meta := callMeta("read-0001", "service-instance-0001")
	contextValue := meta[CallContextMetaKey].(map[string]any)
	contextValue["managedRunId"] = "forged-run-0001"

	tests := []struct {
		name string
		args any
		want string
	}{
		{name: ToolListTasks, args: EmptyInput{}, want: "list:read-0001"},
		{name: ToolGetTask, args: TaskInput{TaskHandle: "task-0001"}, want: "get:read-0001:task-0001"},
		{name: ToolExplainTask, args: TaskInput{TaskHandle: "task-0001"}, want: "explain:read-0001:task-0001"},
		{name: ToolGetLaunchPlan, args: TaskInput{TaskHandle: "task-0001"}, want: "launch-plan:read-0001:task-0001"},
		{name: ToolDoctor, args: EmptyInput{}, want: "doctor:read-0001"},
		{name: ToolWorkerProfiles, args: EmptyInput{}, want: "profiles:read-0001"},
	}
	for _, test := range tests {
		client.calls = nil
		result, callErr := session.CallTool(context.Background(), &mcp.CallToolParams{
			Meta: meta, Name: test.name, Arguments: test.args,
		})
		if callErr != nil || result.IsError {
			t.Fatalf("CallTool(%s) = %#v, %v", test.name, result, callErr)
		}
		if got := strings.Join(client.calls, ","); got != test.want {
			t.Fatalf("CallTool(%s) calls = %q, want %q", test.name, got, test.want)
		}
	}
}

func TestFacade_HandbackTaskUsesAuthenticatedCanonicalMutation(t *testing.T) {
	client := &fakeClient{handbackResult: localapi.TaskMutationResult{
		SchemaVersion: 1, OperationID: "handback-0001", TaskHandle: "task-0001",
		State: domain.TaskValidating, StateVersion: 18, SideEffect: localapi.SideEffectMutate,
	}}
	facade, err := New(Config{
		Client: client, ServiceInstanceID: "service-instance-0001", Version: "test",
		NewOperationID: func() (string, error) { return "reconcile-0001", nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	session := connectFacade(t, facade)
	result, err := session.CallTool(context.Background(), &mcp.CallToolParams{
		Meta: callMeta("handback-0001", "service-instance-0001"), Name: ToolHandbackTask,
		Arguments: HandbackTaskInput{TaskHandle: "task-0001", Action: application.HandbackValidateDeveloperWork},
	})
	if err != nil || result.IsError {
		t.Fatalf("CallTool(handback_task) = %#v, %v", result, err)
	}
	if got := strings.Join(client.calls, ","); got != "handback:handback-0001:task-0001:validate-developer-work" {
		t.Fatalf("handback calls = %q", got)
	}
}

func TestFacade_ReconcileTaskUsesClosedIdempotentCanonicalMutation(t *testing.T) {
	client := &fakeClient{reconcileResult: localapi.TaskMutationResult{
		SchemaVersion: 1, OperationID: "reconcile-task-0001", TaskHandle: "task-0001",
		State: domain.TaskValidating, StateVersion: 20, SideEffect: localapi.SideEffectMutate,
	}}
	facade, err := New(Config{
		Client: client, ServiceInstanceID: "service-instance-0001", Version: "test",
		NewOperationID: func() (string, error) { return "reconcile-read-0001", nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	session := connectFacade(t, facade)
	result, err := session.CallTool(context.Background(), &mcp.CallToolParams{
		Meta: callMeta("reconcile-task-0001", "service-instance-0001"), Name: ToolReconcileTask,
		Arguments: ReconcileTaskInput{TaskHandle: "task-0001", Action: application.ReconcileValidateCleanCandidate},
	})
	if err != nil || result.IsError {
		t.Fatalf("CallTool(reconcile_task) = %#v, %v", result, err)
	}
	if got := strings.Join(client.calls, ","); got != "reconcile:reconcile-task-0001:task-0001:validate-clean-candidate" {
		t.Fatalf("reconciliation calls = %q", got)
	}
	tools, err := session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, listed := range tools.Tools {
		if listed.Name == ToolReconcileTask && (listed.Annotations == nil || listed.Annotations.ReadOnlyHint ||
			!listed.Annotations.IdempotentHint || listed.Annotations.DestructiveHint == nil || *listed.Annotations.DestructiveHint ||
			listed.Annotations.OpenWorldHint == nil || *listed.Annotations.OpenWorldHint) {
			t.Fatalf("reconciliation annotations = %#v", listed.Annotations)
		}
	}
}

func TestFacade_CleanupTaskDeclaresDestructiveOpenWorldMutation(t *testing.T) {
	client := &fakeClient{cleanupResult: localapi.TaskMutationResult{
		SchemaVersion: 1, OperationID: "cleanup-0001", TaskHandle: "task-0001",
		State: domain.TaskCleaned, StateVersion: 27, SideEffect: localapi.SideEffectMutate,
	}}
	facade, err := New(Config{
		Client: client, ServiceInstanceID: "service-instance-0001", Version: "test",
		NewOperationID: func() (string, error) { return "reconcile-0001", nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	session := connectFacade(t, facade)
	result, err := session.CallTool(context.Background(), &mcp.CallToolParams{
		Meta: callMeta("cleanup-0001", "service-instance-0001"), Name: ToolCleanupTask,
		Arguments: TaskInput{TaskHandle: "task-0001"},
	})
	if err != nil || result.IsError {
		t.Fatalf("CallTool(cleanup_task) = %#v, %v", result, err)
	}
	if got := strings.Join(client.calls, ","); got != "cleanup:cleanup-0001:task-0001" {
		t.Fatalf("cleanup calls = %q", got)
	}
	tools, err := session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, listed := range tools.Tools {
		if listed.Name == ToolCleanupTask && (listed.Annotations == nil || listed.Annotations.DestructiveHint == nil ||
			!*listed.Annotations.DestructiveHint || listed.Annotations.OpenWorldHint == nil || !*listed.Annotations.OpenWorldHint) {
			t.Fatalf("cleanup annotations = %#v", listed.Annotations)
		}
	}
}

func TestFacade_RejectsHostilePrivateMetadataBeforeAuthority(t *testing.T) {
	unknown := callMeta("read-0001", "service-instance-0001")
	unknown[CallContextMetaKey].(map[string]any)["authority"] = "broaden"
	tests := []struct {
		name string
		meta mcp.Meta
	}{
		{name: "missing", meta: nil},
		{name: "wrong type", meta: mcp.Meta{CallContextMetaKey: "private-value"}},
		{name: "unknown context field", meta: unknown},
		{name: "wrong service", meta: callMeta("read-0001", "other-service-0001")},
		{name: "local operation mismatch", meta: callMeta("UPPER.operation", "service-instance-0001")},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			client := &fakeClient{}
			facade, err := New(Config{
				Client: client, ServiceInstanceID: "service-instance-0001", Version: "test",
				NewOperationID: func() (string, error) { return "reconcile-0001", nil },
			})
			if err != nil {
				t.Fatal(err)
			}
			session := connectFacade(t, facade)
			result, callErr := session.CallTool(context.Background(), &mcp.CallToolParams{
				Meta: test.meta, Name: ToolListTasks, Arguments: EmptyInput{},
			})
			if callErr != nil || result == nil || !result.IsError {
				t.Fatalf("CallTool(hostile) = %#v, %v, want tool error", result, callErr)
			}
			if len(client.calls) != 0 || result.Meta[ManagedRunResultMetaKey] != nil {
				t.Fatalf("hostile metadata broadened authority: calls=%v meta=%v", client.calls, result.Meta)
			}
			if strings.Contains(result.Content[0].(*mcp.TextContent).Text, "private-value") {
				t.Fatal("tool error leaked hostile metadata")
			}
		})
	}
}

func TestFacade_UncertainPreparationReconcilesBeforeOneExactRetry(t *testing.T) {
	unavailable, err := domain.NewFailure(domain.ErrorUnavailable, true, "local service is unavailable", "reconcile the operation", errors.New("private disconnect"))
	if err != nil {
		t.Fatal(err)
	}
	client := &fakeClient{
		prepareResults: []localapi.PrepareTaskResult{{}, preparedResult()},
		prepareErrors:  []error{unavailable, nil},
		operation: application.OperationView{
			SchemaVersion: 1, OperationID: "prepare-0001", Command: "PrepareTask",
			Status: domain.OperationCompleted, StateVersion: 7,
		},
	}
	facade, err := New(Config{
		Client: client, ServiceInstanceID: "service-instance-0001", Version: "test",
		NewOperationID:   func() (string, error) { return "reconcile-0001", nil },
		ReconcileTimeout: time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	request := &mcp.CallToolRequest{Params: &mcp.CallToolParamsRaw{Meta: callMeta("prepare-0001", "service-instance-0001")}}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	result, output, err := facade.prepareTask(canceled, request, prepareInput())
	if err != nil || result == nil || output.TaskHandle != "task-0001" {
		t.Fatalf("prepareTask() = %#v, %#v, %v", result, output, err)
	}
	want := "prepare:prepare-0001,operation:reconcile-0001:prepare-0001,prepare:prepare-0001"
	if got := strings.Join(client.calls, ","); got != want {
		t.Fatalf("calls = %q, want %q", got, want)
	}
}

func TestFacade_UncertainTerminalMutationsReconcileBeforeExactRetry(t *testing.T) {
	unavailable, err := domain.NewFailure(domain.ErrorUnavailable, true, "local service is unavailable", "reconcile the operation", errors.New("private disconnect"))
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name    string
		command string
		call    func(*Facade, context.Context, *mcp.CallToolRequest) (localapi.TaskMutationResult, error)
		want    string
	}{
		{
			name: "reconciliation", command: "ReconcileTask", want: "reconcile:reconciliation-0001:task-0001:validate-clean-candidate,operation:reconcile-0001:reconciliation-0001,reconcile:reconciliation-0001:task-0001:validate-clean-candidate",
			call: func(facade *Facade, ctx context.Context, request *mcp.CallToolRequest) (localapi.TaskMutationResult, error) {
				_, result, callErr := facade.reconcileTask(ctx, request, ReconcileTaskInput{TaskHandle: "task-0001", Action: application.ReconcileValidateCleanCandidate})
				return result, callErr
			},
		},
		{
			name: "handback", command: "HandbackTask", want: "handback:handback-0001:task-0001:validate-developer-work,operation:reconcile-0001:handback-0001,handback:handback-0001:task-0001:validate-developer-work",
			call: func(facade *Facade, ctx context.Context, request *mcp.CallToolRequest) (localapi.TaskMutationResult, error) {
				_, result, callErr := facade.handbackTask(ctx, request, HandbackTaskInput{TaskHandle: "task-0001", Action: application.HandbackValidateDeveloperWork})
				return result, callErr
			},
		},
		{
			name: "cleanup", command: "CleanupTask", want: "cleanup:cleanup-0001:task-0001,operation:reconcile-0001:cleanup-0001,cleanup:cleanup-0001:task-0001",
			call: func(facade *Facade, ctx context.Context, request *mcp.CallToolRequest) (localapi.TaskMutationResult, error) {
				_, result, callErr := facade.cleanupTask(ctx, request, TaskInput{TaskHandle: "task-0001"})
				return result, callErr
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			operationID := test.name + "-0001"
			client := &fakeClient{
				operation:       application.OperationView{SchemaVersion: 1, OperationID: operationID, Command: test.command, Status: domain.OperationCompleted, StateVersion: 7},
				reconcileResult: localapi.TaskMutationResult{TaskHandle: "task-0001", State: domain.TaskValidating},
				handbackResult:  localapi.TaskMutationResult{TaskHandle: "task-0001", State: domain.TaskValidating},
				cleanupResult:   localapi.TaskMutationResult{TaskHandle: "task-0001", State: domain.TaskCleaned},
				reconcileErrors: []error{unavailable, nil}, handbackErrors: []error{unavailable, nil}, cleanupErrors: []error{unavailable, nil},
			}
			facade, createErr := New(Config{Client: client, ServiceInstanceID: "service-instance-0001", Version: "test", NewOperationID: func() (string, error) { return "reconcile-0001", nil }, ReconcileTimeout: time.Second})
			if createErr != nil {
				t.Fatal(createErr)
			}
			request := &mcp.CallToolRequest{Params: &mcp.CallToolParamsRaw{Meta: callMeta(operationID, "service-instance-0001")}}
			canceled, cancel := context.WithCancel(context.Background())
			cancel()
			result, callErr := test.call(facade, canceled, request)
			if callErr != nil || result.TaskHandle != "task-0001" {
				t.Fatalf("terminal mutation = %#v, %v", result, callErr)
			}
			if got := strings.Join(client.calls, ","); got != test.want {
				t.Fatalf("calls = %q, want %q", got, test.want)
			}
		})
	}
}

func assertToolCatalog(t *testing.T, tools []*mcp.Tool) {
	t.Helper()
	want := map[string]bool{ToolPrepareTask: false, ToolReconcileTask: false, ToolHandbackTask: false, ToolCleanupTask: false, ToolDiscardTask: false, ToolSyncPrimary: false, ToolAttestScout: false, ToolPauseTask: false, ToolCancelTask: false, ToolResumeTask: false, ToolVerifyTask: false, ToolPromoteScout: false, ToolReplaceWorker: false, ToolSteerTask: false, ToolListTasks: true, ToolGetTask: true, ToolExplainTask: true, ToolGetLaunchPlan: true, ToolDoctor: true, ToolWorkerProfiles: true}
	if len(tools) != len(want) {
		t.Fatalf("tool count = %d, want %d", len(tools), len(want))
	}
	// Destructive is an explicit set, not a single name. A tool that quietly
	// became destructive would otherwise fail this test with an annotation dump
	// rather than a statement about which tools may destroy work.
	destructiveTools := map[string]bool{ToolCleanupTask: true, ToolCancelTask: true, ToolDiscardTask: true}
	for _, tool := range tools {
		readOnly, ok := want[tool.Name]
		destructive := destructiveTools[tool.Name]
		if !ok || tool.Annotations == nil || tool.Annotations.ReadOnlyHint != readOnly ||
			!tool.Annotations.IdempotentHint || tool.Annotations.DestructiveHint == nil || *tool.Annotations.DestructiveHint != destructive ||
			tool.Annotations.OpenWorldHint == nil || *tool.Annotations.OpenWorldHint != destructive {
			t.Fatalf("tool %q annotations = %#v", tool.Name, tool.Annotations)
		}
		delete(want, tool.Name)
	}
}

func connectFacade(t *testing.T, facade *Facade) *mcp.ClientSession {
	t.Helper()
	clientTransport, serverTransport := mcp.NewInMemoryTransports()
	serverSession, err := facade.Server().Connect(context.Background(), serverTransport, nil)
	if err != nil {
		t.Fatalf("connect server: %v", err)
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "1"}, nil)
	clientSession, err := client.Connect(context.Background(), clientTransport, nil)
	if err != nil {
		t.Fatalf("connect client: %v", err)
	}
	t.Cleanup(func() { _ = clientSession.Close(); _ = serverSession.Close() })
	return clientSession
}

func callMeta(operationID, serviceInstanceID string) mcp.Meta {
	return mcp.Meta{CallContextMetaKey: map[string]any{
		"operationId": operationID, "serviceInstanceId": serviceInstanceID,
		"agentId": "agent-0001", "conversationRef": "conversation-0001",
		"workspacePolicyHash": strings.Repeat("a", 64), "rootRunId": "root-run-0001",
		"traceId": "trace-0001",
	}}
}

func prepareInput() PrepareTaskInput {
	return PrepareTaskInput{
		Shape: domain.ShapeScout, RepositoryID: "product-api", BaseRevision: strings.Repeat("a", 40),
		AcceptanceCriteria: []string{"Return one report."}, Constraints: []string{"Do not deliver."},
		ValidationProfile: "go-default", DeliveryMode: domain.DeliveryReport, WorkerProfileID: "fixture-worker",
	}
}

func preparedResult() localapi.PrepareTaskResult {
	return localapi.PrepareTaskResult{
		SchemaVersion: 1, OperationID: "prepare-0001", TaskHandle: "task-0001",
		State: domain.TaskPrepared, StateVersion: 7, SideEffect: localapi.SideEffectMutate,
		ManagedRun: application.ManagedRunPreparation{
			ExternalRunRef: "task-0001", RegistrationNonce: "registration-nonce_private",
			RequestedWorkspaceRoot: "/approved/workspaces/task-0001",
			RequestedAttachment: application.PreparedRuntimeAttachment{
				Kind: application.RuntimeAttachmentUnixSocket, SourcePath: "/approved/runtime/task-0001/attachment.sock",
				RelayIdentity: strings.Repeat("ab", 32),
			},
			ExpiresAt: time.Date(2026, time.August, 10, 0, 0, 0, 0, time.UTC), State: application.PreparationOpen,
		},
	}
}

type fakeClient struct {
	calls           []string
	profiles        application.WorkerProfileList
	pauseResult     localapi.TaskMutationResult
	cancelResult    localapi.TaskMutationResult
	resumeResult    localapi.TaskMutationResult
	verifyResult    localapi.TaskMutationResult
	promoteResult   localapi.PrepareTaskResult
	replaceResult   localapi.TaskMutationResult
	steerResult     localapi.TaskMutationResult
	steerErrors     []error
	replaceErrors   []error
	promoteErrors   []error
	verifyErrors    []error
	resumeErrors    []error
	cancelErrors    []error
	pauseErrors     []error
	prepareResults  []localapi.PrepareTaskResult
	prepareErrors   []error
	operation       application.OperationView
	operationError  error
	handbackResult  localapi.TaskMutationResult
	reconcileResult localapi.TaskMutationResult
	cleanupResult   localapi.TaskMutationResult
	handbackErrors  []error
	reconcileErrors []error
	cleanupErrors   []error
	discardResult   localapi.TaskMutationResult
	discardErrors   []error
	syncReport      application.PrimarySyncReport
	syncErrors      []error
	attestResult    localapi.TaskMutationResult
	attestErrors    []error
}

func (client *fakeClient) ReconcileTask(
	_ context.Context,
	operationID string,
	input localapi.ReconcileTaskInput,
) (localapi.TaskMutationResult, error) {
	client.calls = append(client.calls, "reconcile:"+operationID+":"+input.TaskHandle+":"+string(input.Action))
	if len(client.reconcileErrors) == 0 {
		return client.reconcileResult, nil
	}
	err := client.reconcileErrors[0]
	client.reconcileErrors = client.reconcileErrors[1:]
	return client.reconcileResult, err
}

func (client *fakeClient) SteerTask(
	_ context.Context,
	operationID string,
	input localapi.SteerTaskInput,
) (localapi.TaskMutationResult, error) {
	client.calls = append(client.calls, "steer:"+operationID+":"+input.TaskHandle+":"+input.Instruction)
	if len(client.steerErrors) == 0 {
		return client.steerResult, nil
	}
	failure := client.steerErrors[0]
	client.steerErrors = client.steerErrors[1:]
	if failure == nil {
		return client.steerResult, nil
	}
	return localapi.TaskMutationResult{}, failure
}

func (client *fakeClient) ReplaceWorker(
	_ context.Context,
	operationID string,
	input localapi.ReplaceWorkerInput,
) (localapi.TaskMutationResult, error) {
	client.calls = append(client.calls, "replace:"+operationID+":"+input.TaskHandle+":"+input.WorkerProfileID)
	if len(client.replaceErrors) == 0 {
		return client.replaceResult, nil
	}
	failure := client.replaceErrors[0]
	client.replaceErrors = client.replaceErrors[1:]
	if failure == nil {
		return client.replaceResult, nil
	}
	return localapi.TaskMutationResult{}, failure
}

func (client *fakeClient) PromoteScout(
	_ context.Context,
	operationID string,
	input localapi.PromoteScoutInput,
) (localapi.PrepareTaskResult, error) {
	client.calls = append(client.calls, "promote:"+operationID+":"+input.ScoutTaskHandle)
	if len(client.promoteErrors) == 0 {
		return client.promoteResult, nil
	}
	failure := client.promoteErrors[0]
	client.promoteErrors = client.promoteErrors[1:]
	if failure == nil {
		return client.promoteResult, nil
	}
	return localapi.PrepareTaskResult{}, failure
}

func (client *fakeClient) VerifyTask(
	_ context.Context,
	operationID string,
	input localapi.VerifyTaskInput,
) (localapi.TaskMutationResult, error) {
	client.calls = append(client.calls, "verify:"+operationID+":"+input.TaskHandle)
	if len(client.verifyErrors) == 0 {
		return client.verifyResult, nil
	}
	failure := client.verifyErrors[0]
	client.verifyErrors = client.verifyErrors[1:]
	if failure == nil {
		return client.verifyResult, nil
	}
	return localapi.TaskMutationResult{}, failure
}

func (client *fakeClient) ResumeTask(
	_ context.Context,
	operationID string,
	input localapi.ResumeTaskInput,
) (localapi.TaskMutationResult, error) {
	client.calls = append(client.calls, "resume:"+operationID+":"+input.TaskHandle)
	if len(client.resumeErrors) == 0 {
		return client.resumeResult, nil
	}
	failure := client.resumeErrors[0]
	client.resumeErrors = client.resumeErrors[1:]
	if failure == nil {
		return client.resumeResult, nil
	}
	return localapi.TaskMutationResult{}, failure
}

func (client *fakeClient) CancelTask(
	_ context.Context,
	operationID string,
	input localapi.CancelTaskInput,
) (localapi.TaskMutationResult, error) {
	client.calls = append(client.calls, "cancel:"+operationID+":"+input.TaskHandle)
	if len(client.cancelErrors) == 0 {
		return client.cancelResult, nil
	}
	failure := client.cancelErrors[0]
	client.cancelErrors = client.cancelErrors[1:]
	if failure == nil {
		return client.cancelResult, nil
	}
	return localapi.TaskMutationResult{}, failure
}

func (client *fakeClient) PauseTask(
	_ context.Context,
	operationID string,
	input localapi.PauseTaskInput,
) (localapi.TaskMutationResult, error) {
	client.calls = append(client.calls, "pause:"+operationID+":"+input.TaskHandle)
	if len(client.pauseErrors) == 0 {
		return client.pauseResult, nil
	}
	failure := client.pauseErrors[0]
	client.pauseErrors = client.pauseErrors[1:]
	if failure == nil {
		return client.pauseResult, nil
	}
	return localapi.TaskMutationResult{}, failure
}

func (client *fakeClient) CleanupTask(
	_ context.Context,
	operationID string,
	input localapi.CleanupTaskInput,
) (localapi.TaskMutationResult, error) {
	client.calls = append(client.calls, "cleanup:"+operationID+":"+input.TaskHandle)
	if len(client.cleanupErrors) == 0 {
		return client.cleanupResult, nil
	}
	err := client.cleanupErrors[0]
	client.cleanupErrors = client.cleanupErrors[1:]
	return client.cleanupResult, err
}

func (client *fakeClient) HandbackTask(
	_ context.Context,
	operationID string,
	input localapi.HandbackTaskInput,
) (localapi.TaskMutationResult, error) {
	client.calls = append(client.calls, "handback:"+operationID+":"+input.TaskHandle+":"+string(input.Action))
	if len(client.handbackErrors) == 0 {
		return client.handbackResult, nil
	}
	err := client.handbackErrors[0]
	client.handbackErrors = client.handbackErrors[1:]
	return client.handbackResult, err
}

func (client *fakeClient) PrepareTask(_ context.Context, operationID string, _ localapi.PrepareTaskInput) (localapi.PrepareTaskResult, error) {
	client.calls = append(client.calls, "prepare:"+operationID)
	if len(client.prepareErrors) > 0 {
		err := client.prepareErrors[0]
		client.prepareErrors = client.prepareErrors[1:]
		result := client.prepareResults[0]
		client.prepareResults = client.prepareResults[1:]
		return result, err
	}
	if len(client.prepareResults) == 0 {
		return localapi.PrepareTaskResult{}, nil
	}
	result := client.prepareResults[0]
	client.prepareResults = client.prepareResults[1:]
	return result, nil
}

func (client *fakeClient) ListWorkerProfiles(
	_ context.Context,
	operationID string,
) (application.WorkerProfileList, error) {
	client.calls = append(client.calls, "profiles:"+operationID)
	return client.profiles, nil
}

func (client *fakeClient) ListTasks(_ context.Context, operationID string, _ localapi.ListTasksInput) (application.TaskList, error) {
	client.calls = append(client.calls, "list:"+operationID)
	return application.TaskList{SchemaVersion: 1, StateVersion: 7, Tasks: []application.TaskSummary{}}, nil
}

func (client *fakeClient) ShowTask(_ context.Context, operationID, taskHandle string) (application.TaskDetail, error) {
	client.calls = append(client.calls, "get:"+operationID+":"+taskHandle)
	return application.TaskDetail{SchemaVersion: 1, StateVersion: 7, Summary: application.TaskSummary{TaskHandle: taskHandle, StateVersion: 7}}, nil
}

func (client *fakeClient) ExplainTask(_ context.Context, operationID, taskHandle string) (application.TaskExplanation, error) {
	client.calls = append(client.calls, "explain:"+operationID+":"+taskHandle)
	return application.TaskExplanation{SchemaVersion: 1, Summary: application.TaskSummary{TaskHandle: taskHandle, StateVersion: 7}}, nil
}

func (client *fakeClient) Operation(_ context.Context, operationID, target string) (application.OperationView, error) {
	client.calls = append(client.calls, "operation:"+operationID+":"+target)
	return client.operation, client.operationError
}

func (client *fakeClient) GetLaunchPlan(_ context.Context, operationID, taskHandle string) (application.LaunchPlan, error) {
	client.calls = append(client.calls, "launch-plan:"+operationID+":"+taskHandle)
	return application.LaunchPlan{
		SchemaVersion: 1, StateVersion: 7, TaskHandle: taskHandle,
		WorkerProfileID: "codex-reviewed", TerminalAllowEntryID: "terminal-codex-reviewed",
	}, nil
}

// Readiness is a read like the others; the fake records the call so the parity
// and authorization tests can assert it reached the canonical client.
func (client *fakeClient) Diagnose(_ context.Context, operationID string) (application.DiagnosticReport, error) {
	client.calls = append(client.calls, "doctor:"+operationID)
	return application.DiagnosticReport{SchemaVersion: 1}, nil
}
