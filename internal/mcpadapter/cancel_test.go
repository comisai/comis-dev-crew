package mcpadapter

import (
	"context"
	"strings"
	"testing"

	"github.com/comisai/comis-dev-crew/internal/domain"
	"github.com/comisai/comis-dev-crew/internal/localapi"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Cancel is destructive: it ends work an operator asked for and asking again
// does not undo it. It is not removal, and the description must say so — a model
// that read "cancel" as "clean up" would propose it where cleanup's evidence
// gate belongs.
func TestFacade_CancelIsDestructiveButNotRemoval(t *testing.T) {
	client := &fakeClient{cancelResult: localapi.TaskMutationResult{
		TaskHandle: "task-0001", State: domain.TaskCancelled, StateVersion: 11,
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
		if tool.Name != ToolCancelTask {
			continue
		}
		found = true
		if tool.Annotations == nil || tool.Annotations.DestructiveHint == nil ||
			!*tool.Annotations.DestructiveHint || !tool.Annotations.IdempotentHint {
			t.Errorf("cancel_task annotations = %#v", tool.Annotations)
		}
		description := strings.ToLower(tool.Description)
		if !strings.Contains(description, "preserv") {
			t.Errorf("cancel_task must state that it preserves work: %s", tool.Description)
		}
	}
	if !found {
		t.Fatal("cancel_task tool is absent")
	}

	result, callErr := session.CallTool(context.Background(), &mcp.CallToolParams{
		Meta: callMeta("cancel-0001", "service-instance-0001"),
		Name: ToolCancelTask, Arguments: TaskInput{TaskHandle: "task-0001"},
	})
	if callErr != nil || result.IsError {
		t.Fatalf("CallTool(cancel_task) = %#v, %v", result, callErr)
	}
	if len(client.calls) != 1 || client.calls[0] != "cancel:cancel-0001:task-0001" {
		t.Fatalf("cancel did not route through one canonical command: %v", client.calls)
	}
}

func TestFacade_AnUncertainCancelIsReconciledNotResent(t *testing.T) {
	client := &fakeClient{
		cancelErrors: []error{&domain.Failure{
			Code: domain.ErrorUnavailable, Retryable: true, Message: "send uncertain",
		}},
		cancelResult: localapi.TaskMutationResult{
			TaskHandle: "task-0001", State: domain.TaskCancelled, StateVersion: 11,
			SideEffect: localapi.SideEffectMutate,
		},
		operation: fixtureOperation("cancel-0001", "CancelTask"),
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
		Meta: callMeta("cancel-0001", "service-instance-0001"),
		Name: ToolCancelTask, Arguments: TaskInput{TaskHandle: "task-0001"},
	})
	if callErr != nil || result.IsError {
		t.Fatalf("CallTool(cancel_task) = %#v, %v", result, callErr)
	}
	var reconciled bool
	for _, call := range client.calls {
		if strings.HasPrefix(call, "operation:") {
			reconciled = true
		}
	}
	if !reconciled {
		t.Fatalf("an uncertain cancel must be reconciled against its operation: %v", client.calls)
	}
}
