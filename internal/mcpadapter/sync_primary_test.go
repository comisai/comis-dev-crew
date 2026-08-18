package mcpadapter

import (
	"context"
	"strings"
	"testing"

	"github.com/comisai/comis-dev-crew/internal/application"
	"github.com/comisai/comis-dev-crew/internal/localapi"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const syncPrimaryToolName = "sync_primary"

// Synchronization is the only way the primary checkout moves, and an agent must
// be able to ask for it: a worktree prepared from a stale base wastes the whole
// task. It is a mutation rather than a read, but not a destructive one — it
// refuses every posture it cannot advance safely.
func TestFacade_SyncPrimaryIsReachableAndNotDestructive(t *testing.T) {
	client := &fakeClient{syncReport: application.PrimarySyncReport{
		SchemaVersion: 1, StateVersion: 31, RepositoryID: "product-api", Branch: "main",
		Outcome: application.PrimarySyncUpdated,
	}}
	facade, err := New(Config{
		Client: client, ServiceInstanceID: "service-instance-0001", Version: "test",
		NewOperationID: func() (string, error) { return "reconcile-0001", nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	session := connectFacade(t, facade)

	tools, err := session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("ListTools() error = %v", err)
	}
	var found bool
	for _, tool := range tools.Tools {
		if tool.Name != syncPrimaryToolName {
			continue
		}
		found = true
		if tool.Annotations == nil || tool.Annotations.ReadOnlyHint ||
			tool.Annotations.DestructiveHint == nil || *tool.Annotations.DestructiveHint {
			t.Errorf("%s annotations = %#v", syncPrimaryToolName, tool.Annotations)
		}
		// A model must not reach for this expecting a reset or a merge.
		description := strings.ToLower(tool.Description)
		if !strings.Contains(description, "fast-forward") {
			t.Errorf("%s must state that it only fast-forwards: %s", syncPrimaryToolName, tool.Description)
		}
	}
	if !found {
		t.Fatalf("%s tool is absent", syncPrimaryToolName)
	}

	result, callErr := session.CallTool(context.Background(), &mcp.CallToolParams{
		Meta:      callMeta("sync-0001", "service-instance-0001"),
		Name:      syncPrimaryToolName,
		Arguments: map[string]any{"repositoryId": "product-api"},
	})
	if callErr != nil || result.IsError {
		t.Fatalf("CallTool(%s) = %#v, %v", syncPrimaryToolName, result, callErr)
	}
	if len(client.calls) != 1 || client.calls[0] != "sync:sync-0001:product-api" {
		t.Fatalf("sync did not route through one canonical command: %v", client.calls)
	}
}

// A refused synchronization is a successful call carrying the posture that
// refused. Reporting it as a tool error would tell an agent the service failed
// when it actually answered.
func TestFacade_SyncPrimaryReturnsARefusalAsAnAnswer(t *testing.T) {
	client := &fakeClient{syncReport: application.PrimarySyncReport{
		SchemaVersion: 1, StateVersion: 31, RepositoryID: "product-api", Branch: "main",
		Outcome: application.PrimarySyncRefused, Refusal: application.PrimarySyncRefusalDirty,
	}}
	facade, err := New(Config{
		Client: client, ServiceInstanceID: "service-instance-0001", Version: "test",
		NewOperationID: func() (string, error) { return "reconcile-0001", nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	session := connectFacade(t, facade)

	result, callErr := session.CallTool(context.Background(), &mcp.CallToolParams{
		Meta:      callMeta("sync-0001", "service-instance-0001"),
		Name:      syncPrimaryToolName,
		Arguments: map[string]any{"repositoryId": "product-api"},
	})
	if callErr != nil || result.IsError {
		t.Fatalf("CallTool(%s) = %#v, %v", syncPrimaryToolName, result, callErr)
	}
	visible := result.Content[0].(*mcp.TextContent).Text
	for _, required := range []string{"refused", "dirty_checkout"} {
		if !strings.Contains(visible, required) {
			t.Errorf("refusal did not name %q: %s", required, visible)
		}
	}
}

func (client *fakeClient) SyncPrimary(
	_ context.Context,
	operationID string,
	input localapi.SyncPrimaryInput,
) (application.PrimarySyncReport, error) {
	client.calls = append(client.calls, "sync:"+operationID+":"+input.RepositoryID)
	if len(client.syncErrors) == 0 {
		return client.syncReport, nil
	}
	failure := client.syncErrors[0]
	client.syncErrors = client.syncErrors[1:]
	return application.PrimarySyncReport{}, failure
}
