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
	for _, private := range []string{"managedRun", "registration-nonce_private", "expiresAt"} {
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
	if got := strings.Join(client.calls, ","); got != "prepare:prepare-0001" {
		t.Fatalf("local calls = %q, want one canonical prepare", got)
	}
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
	result, output, err := facade.prepareTask(context.Background(), request, prepareInput())
	if err != nil || result == nil || output.TaskHandle != "task-0001" {
		t.Fatalf("prepareTask() = %#v, %#v, %v", result, output, err)
	}
	want := "prepare:prepare-0001,operation:reconcile-0001:prepare-0001,prepare:prepare-0001"
	if got := strings.Join(client.calls, ","); got != want {
		t.Fatalf("calls = %q, want %q", got, want)
	}
}

func assertToolCatalog(t *testing.T, tools []*mcp.Tool) {
	t.Helper()
	want := map[string]bool{ToolPrepareTask: false, ToolListTasks: true, ToolGetTask: true, ToolExplainTask: true}
	if len(tools) != len(want) {
		t.Fatalf("tool count = %d, want %d", len(tools), len(want))
	}
	for _, tool := range tools {
		readOnly, ok := want[tool.Name]
		if !ok || tool.Annotations == nil || tool.Annotations.ReadOnlyHint != readOnly ||
			!tool.Annotations.IdempotentHint || tool.Annotations.DestructiveHint == nil || *tool.Annotations.DestructiveHint ||
			tool.Annotations.OpenWorldHint == nil || *tool.Annotations.OpenWorldHint {
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
			ExpiresAt: time.Date(2026, time.August, 10, 0, 0, 0, time.UTC),
		},
	}
}

type fakeClient struct {
	calls          []string
	prepareResults []localapi.PrepareTaskResult
	prepareErrors  []error
	operation      application.OperationView
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

func (client *fakeClient) ListTasks(_ context.Context, operationID string) (application.TaskList, error) {
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
	return client.operation, nil
}
