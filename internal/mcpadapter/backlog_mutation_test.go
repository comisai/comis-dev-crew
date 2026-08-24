package mcpadapter

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/comisai/comis-dev-crew/internal/application"
	"github.com/comisai/comis-dev-crew/internal/comiswire"
	"github.com/comisai/comis-dev-crew/internal/domain"
	"github.com/comisai/comis-dev-crew/internal/localapi"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestFacadeBacklogMutationToolsPreserveProvenanceAndPrivateAuthority(t *testing.T) {
	client := &backlogMCPClient{
		fakeClient: &fakeClient{}, addition: backlogMCPAddition(), promotion: backlogMCPPromotion(),
	}
	facade, err := New(Config{
		Client: client, ServiceInstanceID: "service-instance-0001", Version: "test",
		NewOperationID: func() (string, error) { return "reconcile-backlog-0001", nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	session := connectFacade(t, facade)
	tools, err := session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	wantTools := map[string]bool{ToolAddBacklog: false, ToolPromoteBacklog: false}
	for _, listed := range tools.Tools {
		if _, wanted := wantTools[listed.Name]; !wanted {
			continue
		}
		if listed.Annotations == nil || listed.Annotations.ReadOnlyHint ||
			listed.Annotations.DestructiveHint == nil || *listed.Annotations.DestructiveHint {
			t.Fatalf("backlog tool %q annotations = %#v", listed.Name, listed.Annotations)
		}
		delete(wantTools, listed.Name)
	}
	if len(wantTools) != 0 {
		t.Fatalf("backlog mutation tools are absent: %#v", wantTools)
	}
	added, err := session.CallTool(context.Background(), &mcp.CallToolParams{
		Meta: callMeta("operation-mcp-backlog-add", "service-instance-0001"), Name: ToolAddBacklog,
		Arguments: AddBacklogInput{
			RepositoryID: "repo-primary", Shape: domain.ShapeShip,
			RequestedOutcome: "Implement the bounded request.", DependsOn: []string{},
			Priority: domain.BacklogPriorityNormal, Readiness: domain.BacklogReady,
		},
	})
	if err != nil || added.IsError {
		t.Fatalf("CallTool(backlog_add) = %#v, %v", added, err)
	}
	visibleAddition, err := json.Marshal(added.StructuredContent)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(visibleAddition), "conversation") ||
		client.addInput.SourceConversationRef != "conversation-0001" {
		t.Fatalf("backlog addition provenance visible/input = %s / %#v", visibleAddition, client.addInput)
	}
	promoted, err := session.CallTool(context.Background(), &mcp.CallToolParams{
		Meta: callMeta("operation-mcp-backlog-promote", "service-instance-0001"), Name: ToolPromoteBacklog,
		Arguments: PromoteBacklogInput{
			BacklogHandle: "backlog-added", BaseRevision: strings.Repeat("a", 40),
			AcceptanceCriteria: []string{"The implementation is verified."}, Constraints: []string{},
			ValidationProfile: "go-default", DeliveryMode: domain.DeliveryPullRequest,
			WorkerProfileID: "codex-reviewed",
		},
	})
	if err != nil || promoted.IsError {
		t.Fatalf("CallTool(backlog_promote) = %#v, %v", promoted, err)
	}
	visiblePromotion, err := json.Marshal(promoted.StructuredContent)
	if err != nil {
		t.Fatal(err)
	}
	for _, private := range []string{"registration-nonce", "/approved/worktrees", "/approved/runtime", "managedRun"} {
		if strings.Contains(string(visiblePromotion), private) {
			t.Fatalf("visible backlog promotion leaked %q: %s", private, visiblePromotion)
		}
	}
	extension, err := json.Marshal(promoted.Meta[ManagedRunResultMetaKey])
	if err != nil || comiswire.ValidatePayload(comiswire.PayloadMCPManagedRunResult, extension) != nil {
		t.Fatalf("backlog managed-run extension = %s, %v", extension, err)
	}
	if client.promoteInput.BacklogHandle != "backlog-added" ||
		client.promoteInput.WorkerProfileID != "codex-reviewed" {
		t.Fatalf("canonical promotion input = %#v", client.promoteInput)
	}
}

func TestFacadeBacklogSchemasExcludeProvenanceAndHostAuthority(t *testing.T) {
	facade, err := New(Config{
		Client:            &backlogMCPClient{fakeClient: &fakeClient{}},
		ServiceInstanceID: "service-instance-0001", Version: "test",
		NewOperationID: func() (string, error) { return "reconcile-backlog-0001", nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	tools, err := connectFacade(t, facade).ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	seen := 0
	for _, listed := range tools.Tools {
		if listed.Name != ToolAddBacklog && listed.Name != ToolPromoteBacklog {
			continue
		}
		seen++
		semantics := inspectSchemaSemantics(t, listed.InputSchema)
		root := semantics.objectAt(t)
		if listed.Name == ToolAddBacklog {
			requireSchemaFields(t, root,
				"repositoryId", "shape", "requestedOutcome", "dependsOn", "priority", "readiness")
		} else {
			requireSchemaFields(t, root,
				"backlogHandle", "baseRevision", "acceptanceCriteria", "constraints",
				"validationProfile", "deliveryMode", "workerProfileId")
		}
		forbidSchemaFields(t, semantics,
			"sourceConversationRef", "serviceInstanceId", "taskHandle", "workspaceRoot",
			"registrationNonce", "managedRunId", "executionAttachmentId",
		)
		if listed.Name == ToolPromoteBacklog {
			forbidSchemaFields(t, semantics, "repositoryId", "shape")
		}
	}
	if seen != 2 {
		t.Fatalf("backlog mutation schemas found = %d, want 2", seen)
	}
}

func TestFacadeBacklogReconciliationRequiresExactCompletedOperation(t *testing.T) {
	client := &backlogMCPClient{
		fakeClient: &fakeClient{}, addition: backlogMCPAddition(), promotion: backlogMCPPromotion(),
	}
	facade, err := New(Config{
		Client: client, ServiceInstanceID: "service-instance-0001", Version: "test",
		NewOperationID: func() (string, error) { return "reconcile-backlog-0001", nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	original := errors.New("local outcome uncertain")
	addInput := localapi.AddBacklogInput{RepositoryID: "repo-primary", SourceConversationRef: "conversation-0001"}
	promoteInput := localapi.PromoteBacklogInput{BacklogHandle: "backlog-added"}
	client.operation = application.OperationView{
		OperationID: "operation-mcp-backlog-add", Command: "AddBacklog", Status: domain.OperationCompleted,
	}
	added, err := facade.reconcileBacklogAddition(
		context.Background(), "operation-mcp-backlog-add", addInput, original,
	)
	if err != nil || added.Item.Handle != "backlog-added" || client.addInput.SourceConversationRef != "conversation-0001" {
		t.Fatalf("reconcileBacklogAddition(completed) = %#v, %v", added, err)
	}
	client.operation = application.OperationView{
		OperationID: "operation-mcp-backlog-promote", Command: "PromoteBacklog", Status: domain.OperationCompleted,
	}
	promoted, err := facade.reconcileBacklogPromotion(
		context.Background(), "operation-mcp-backlog-promote", promoteInput, original,
	)
	if err != nil || promoted.TaskHandle != "task-backlog-promoted" {
		t.Fatalf("reconcileBacklogPromotion(completed) = %#v, %v", promoted, err)
	}
	client.operation.Command = "PrepareTask"
	if _, err := facade.reconcileBacklogPromotion(
		context.Background(), "operation-mcp-backlog-promote", promoteInput, original,
	); !errors.Is(err, original) {
		t.Fatalf("reconcileBacklogPromotion(mismatched) error = %v, want original", err)
	}
	client.operation = application.OperationView{
		OperationID: "operation-mcp-backlog-add", Command: "AddBacklog",
		Status: domain.OperationRejected, ErrorCode: domain.ErrorConflict,
	}
	if _, err := facade.reconcileBacklogAddition(
		context.Background(), "operation-mcp-backlog-add", addInput, original,
	); errors.Is(err, original) {
		t.Fatalf("reconcileBacklogAddition(rejected) error = %v, want safe rejection", err)
	}
	client.operation.Status = domain.OperationAccepted
	client.operation.ErrorCode = ""
	if _, err := facade.reconcileBacklogAddition(
		context.Background(), "operation-mcp-backlog-add", addInput, original,
	); !errors.Is(err, original) {
		t.Fatalf("reconcileBacklogAddition(accepted) error = %v, want original", err)
	}
	client.operation.Status = "invented"
	if _, err := facade.reconcileBacklogAddition(
		context.Background(), "operation-mcp-backlog-add", addInput, original,
	); err == nil || errors.Is(err, original) {
		t.Fatalf("reconcileBacklogAddition(invented) error = %v", err)
	}
	if _, err := facade.reconcileBacklogAddition(
		//lint:ignore SA1012 This boundary test proves the helper preserves the original result without a context.
		nil, "operation-mcp-backlog-add", addInput, original,
	); !errors.Is(err, original) {
		t.Fatalf("reconcileBacklogAddition(nil context) error = %v, want original", err)
	}
	if _, err := facade.reconcileBacklogPromotion(
		//lint:ignore SA1012 This boundary test proves the helper preserves the original result without a context.
		nil, "operation-mcp-backlog-promote", promoteInput, original,
	); !errors.Is(err, original) {
		t.Fatalf("reconcileBacklogPromotion(nil context) error = %v, want original", err)
	}
	client.operation = application.OperationView{
		OperationID: "operation-mcp-backlog-promote", Command: "PromoteBacklog",
		Status: domain.OperationRejected, ErrorCode: domain.ErrorPrecondition,
	}
	if _, err := facade.reconcileBacklogPromotion(
		context.Background(), "operation-mcp-backlog-promote", promoteInput, original,
	); errors.Is(err, original) {
		t.Fatalf("reconcileBacklogPromotion(rejected) error = %v, want safe rejection", err)
	}
	client.operation.Status = domain.OperationUnknown
	client.operation.ErrorCode = ""
	if _, err := facade.reconcileBacklogPromotion(
		context.Background(), "operation-mcp-backlog-promote", promoteInput, original,
	); !errors.Is(err, original) {
		t.Fatalf("reconcileBacklogPromotion(unknown) error = %v, want original", err)
	}
	client.operation.Status = "invented"
	if _, err := facade.reconcileBacklogPromotion(
		context.Background(), "operation-mcp-backlog-promote", promoteInput, original,
	); err == nil || errors.Is(err, original) {
		t.Fatalf("reconcileBacklogPromotion(invented) error = %v", err)
	}
}

func TestFacadeBacklogMutationHandlersFailClosedOnInvalidLocalResults(t *testing.T) {
	client := &backlogMCPClient{fakeClient: &fakeClient{}}
	facade, err := New(Config{
		Client: client, ServiceInstanceID: "service-instance-0001", Version: "test",
		NewOperationID: func() (string, error) { return "reconcile-backlog-0001", nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	addInput := AddBacklogInput{RepositoryID: "repo-primary", Shape: domain.ShapeShip}
	promoteInput := PromoteBacklogInput{BacklogHandle: "backlog-added"}
	if _, _, err := facade.addBacklog(context.Background(), nil, addInput); err == nil {
		t.Fatal("addBacklog(missing authorization) error = nil")
	}
	if _, _, err := facade.promoteBacklog(context.Background(), nil, promoteInput); err == nil {
		t.Fatal("promoteBacklog(missing authorization) error = nil")
	}
	addRequest := &mcp.CallToolRequest{Params: &mcp.CallToolParamsRaw{
		Meta: callMeta("operation-mcp-backlog-add", "service-instance-0001"),
	}}
	promoteRequest := &mcp.CallToolRequest{Params: &mcp.CallToolParamsRaw{
		Meta: callMeta("operation-mcp-backlog-promote", "service-instance-0001"),
	}}
	client.addErr = errors.New("addition client failed")
	if _, _, err := facade.addBacklog(context.Background(), addRequest, addInput); !errors.Is(err, client.addErr) {
		t.Fatalf("addBacklog(client failure) error = %v", err)
	}
	client.addErr = nil
	if _, _, err := facade.addBacklog(context.Background(), addRequest, addInput); err == nil {
		t.Fatal("addBacklog(empty result) error = nil")
	}
	client.promoteErr = errors.New("promotion client failed")
	if _, _, err := facade.promoteBacklog(context.Background(), promoteRequest, promoteInput); !errors.Is(err, client.promoteErr) {
		t.Fatalf("promoteBacklog(client failure) error = %v", err)
	}
	client.promoteErr = nil
	if _, _, err := facade.promoteBacklog(context.Background(), promoteRequest, promoteInput); err == nil {
		t.Fatal("promoteBacklog(empty result) error = nil")
	}
	client.promotion = backlogMCPPromotion()
	client.promotion.ManagedRun.RequestedAttachment.SourcePath = ""
	if _, _, err := facade.promoteBacklog(context.Background(), promoteRequest, PromoteBacklogInput{
		BacklogHandle: "backlog-added",
	}); err == nil {
		t.Fatal("promoteBacklog(invalid private metadata) error = nil")
	}
}

type backlogMCPClient struct {
	*fakeClient
	addition     localapi.AddBacklogResult
	promotion    localapi.PromoteBacklogResult
	addInput     localapi.AddBacklogInput
	promoteInput localapi.PromoteBacklogInput
	addErr       error
	promoteErr   error
}

func (client *backlogMCPClient) AddBacklog(
	_ context.Context,
	operationID string,
	input localapi.AddBacklogInput,
) (localapi.AddBacklogResult, error) {
	client.addInput = input
	client.addition.OperationID = operationID
	return client.addition, client.addErr
}

func (client *backlogMCPClient) PromoteBacklog(
	_ context.Context,
	operationID string,
	input localapi.PromoteBacklogInput,
) (localapi.PromoteBacklogResult, error) {
	client.promoteInput = input
	client.promotion.OperationID = operationID
	return client.promotion, client.promoteErr
}

func backlogMCPAddition() localapi.AddBacklogResult {
	return localapi.AddBacklogResult{
		SchemaVersion: 1, Item: domain.BacklogItem{
			SchemaVersion: 1, Handle: "backlog-added", RepositoryID: "repo-primary", Shape: domain.ShapeShip,
			RequestedOutcome: "Implement the bounded request.", DependsOn: []string{},
			Priority: domain.BacklogPriorityNormal, Readiness: domain.BacklogReady,
			SourceConversationRef: "conversation-0001",
		},
		StateVersion: 14, SideEffect: localapi.SideEffectMutate,
	}
}

func backlogMCPPromotion() localapi.PromoteBacklogResult {
	managedRun := preparedResult().ManagedRun
	managedRun.ExternalRunRef = "task-backlog-promoted"
	return localapi.PromoteBacklogResult{
		SchemaVersion: 1, BacklogHandle: "backlog-added", Readiness: domain.BacklogPromoted,
		TaskHandle: "task-backlog-promoted", State: domain.TaskPrepared,
		TaskStateVersion: 15, StateVersion: 16, SideEffect: localapi.SideEffectMutate,
		ManagedRun: managedRun,
	}
}
