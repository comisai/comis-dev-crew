package mcpadapter

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

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
		encoded, err := json.Marshal(listed.InputSchema)
		if err != nil {
			t.Fatal(err)
		}
		schema := string(encoded)
		for _, forbidden := range []string{
			"sourceConversationRef", "serviceInstanceId", "taskHandle", "workspaceRoot",
			"registrationNonce", "managedRunId", "executionAttachmentId",
		} {
			if strings.Contains(schema, forbidden) {
				t.Fatalf("%s schema exposes %q: %s", listed.Name, forbidden, schema)
			}
		}
		if listed.Name == ToolPromoteBacklog &&
			(strings.Contains(schema, "repositoryId") || strings.Contains(schema, `"shape"`)) {
			t.Fatalf("backlog_promote schema can retarget the item: %s", schema)
		}
	}
	if seen != 2 {
		t.Fatalf("backlog mutation schemas found = %d, want 2", seen)
	}
}

type backlogMCPClient struct {
	*fakeClient
	addition     localapi.AddBacklogResult
	promotion    localapi.PromoteBacklogResult
	addInput     localapi.AddBacklogInput
	promoteInput localapi.PromoteBacklogInput
}

func (client *backlogMCPClient) AddBacklog(
	_ context.Context,
	operationID string,
	input localapi.AddBacklogInput,
) (localapi.AddBacklogResult, error) {
	client.addInput = input
	client.addition.OperationID = operationID
	return client.addition, nil
}

func (client *backlogMCPClient) PromoteBacklog(
	_ context.Context,
	operationID string,
	input localapi.PromoteBacklogInput,
) (localapi.PromoteBacklogResult, error) {
	client.promoteInput = input
	client.promotion.OperationID = operationID
	return client.promotion, nil
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
