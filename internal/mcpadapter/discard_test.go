package mcpadapter

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/comisai/comis-dev-crew/internal/domain"
	"github.com/comisai/comis-dev-crew/internal/localapi"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// The public tool name is written out rather than referenced through its
// constant: it is the wire identity an agent selects, and a rename would
// silently retarget every caller if the test moved with the constant.
const discardToolName = "discard_task"

// Discard removes the worktree of a task that never delivered, so it is the one
// mutation with nothing to fall back on. It reaches every other adapter — the
// canonical handler, the local client, and the operator CLI — and its absence
// here is an adapter-parity defect, not a deliberate authority boundary: the
// design reserves that asymmetry for custody, private logs and process control.
func TestFacade_DiscardIsReachableAndDestructive(t *testing.T) {
	client := &fakeClient{discardResult: localapi.TaskMutationResult{
		TaskHandle: "task-0001", State: domain.TaskCleaned, StateVersion: 12,
		SideEffect: localapi.SideEffectMutate,
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
		if tool.Name != discardToolName {
			continue
		}
		found = true
		if tool.Annotations == nil || tool.Annotations.DestructiveHint == nil ||
			!*tool.Annotations.DestructiveHint || tool.Annotations.ReadOnlyHint {
			t.Errorf("%s annotations = %#v", discardToolName, tool.Annotations)
		}
		// A model choosing between cancel, cleanup and discard has only these
		// descriptions to separate them. Discard is the only one that destroys
		// work, and saying so is what keeps it from being read as a tidier
		// cleanup.
		description := strings.ToLower(tool.Description)
		for _, required := range []string{"remove", "never delivered"} {
			if !strings.Contains(description, required) {
				t.Errorf("%s description must state %q: %s", discardToolName, required, tool.Description)
			}
		}
	}
	if !found {
		t.Fatalf("%s tool is absent", discardToolName)
	}
}

// The acknowledgement is the only gate a discard has. It must be a stated
// argument, so removing work is something a caller says rather than something
// implied by naming the tool at all.
func TestFacade_DiscardCarriesTheAcknowledgementItWasGiven(t *testing.T) {
	client := &fakeClient{discardResult: localapi.TaskMutationResult{
		TaskHandle: "task-0001", State: domain.TaskCleaned, StateVersion: 12,
		SideEffect: localapi.SideEffectMutate,
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
		t.Fatal(err)
	}
	for _, tool := range tools.Tools {
		if tool.Name != discardToolName {
			continue
		}
		schema, marshalErr := json.Marshal(tool.InputSchema)
		if marshalErr != nil {
			t.Fatal(marshalErr)
		}
		if !strings.Contains(string(schema), "acknowledged") {
			t.Fatalf("%s schema omits the acknowledgement: %s", discardToolName, schema)
		}
	}

	for _, acknowledged := range []bool{true, false} {
		client.calls = nil
		result, callErr := session.CallTool(context.Background(), &mcp.CallToolParams{
			Meta: callMeta("discard-0001", "service-instance-0001"),
			Name: discardToolName,
			Arguments: map[string]any{
				"taskHandle": "task-0001", "acknowledged": acknowledged,
			},
		})
		if callErr != nil || result.IsError {
			t.Fatalf("CallTool(%s, acknowledged=%v) = %#v, %v", discardToolName, acknowledged, result, callErr)
		}
		want := "discard:discard-0001:task-0001:" + boolText(acknowledged)
		if len(client.calls) != 1 || client.calls[0] != want {
			t.Fatalf("discard calls = %v, want %q", client.calls, want)
		}
	}
}

// A discard whose outcome is unknown must be reconciled against its stable
// operation, never resent: the second attempt would be asking to remove work
// that the first may already have removed.
func TestFacade_AnUncertainDiscardIsReconciledNotResent(t *testing.T) {
	client := &fakeClient{
		discardErrors: []error{&domain.Failure{
			Code: domain.ErrorUnavailable, Retryable: true, Message: "send uncertain",
		}},
		discardResult: localapi.TaskMutationResult{
			TaskHandle: "task-0001", State: domain.TaskCleaned, StateVersion: 12,
			SideEffect: localapi.SideEffectMutate,
		},
		operation: fixtureOperation("discard-0001", "DiscardTask"),
	}
	facade, err := New(Config{
		Client: client, ServiceInstanceID: "service-instance-0001", Version: "test",
		NewOperationID: func() (string, error) { return "reconcile-0001", nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	session := connectFacade(t, facade)

	result, callErr := session.CallTool(context.Background(), &mcp.CallToolParams{
		Meta: callMeta("discard-0001", "service-instance-0001"),
		Name: discardToolName,
		Arguments: map[string]any{
			"taskHandle": "task-0001", "acknowledged": true,
		},
	})
	if callErr != nil || result.IsError {
		t.Fatalf("CallTool(%s) = %#v, %v", discardToolName, result, callErr)
	}
	var reconciled bool
	for _, call := range client.calls {
		if strings.HasPrefix(call, "operation:") {
			reconciled = true
		}
	}
	if !reconciled {
		t.Fatalf("an uncertain discard must be reconciled against its operation: %v", client.calls)
	}
}

func boolText(value bool) string {
	if value {
		return "true"
	}
	return "false"
}

func (client *fakeClient) DiscardTask(
	_ context.Context,
	operationID string,
	input localapi.DiscardTaskInput,
) (localapi.TaskMutationResult, error) {
	client.calls = append(
		client.calls,
		"discard:"+operationID+":"+input.TaskHandle+":"+boolText(input.Acknowledged),
	)
	if len(client.discardErrors) == 0 {
		return client.discardResult, nil
	}
	failure := client.discardErrors[0]
	client.discardErrors = client.discardErrors[1:]
	if failure == nil {
		return client.discardResult, nil
	}
	return localapi.TaskMutationResult{}, failure
}
