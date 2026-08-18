package mcpadapter

import (
	"context"
	"strings"
	"testing"

	"github.com/comisai/comis-dev-crew/internal/domain"
	"github.com/comisai/comis-dev-crew/internal/localapi"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func verifyFacadeSession(t *testing.T, client *fakeClient) *mcp.ClientSession {
	t.Helper()
	facade, err := New(Config{
		Client: client, ServiceInstanceID: "service-instance-0001", Version: "test",
		NewOperationID: func() (string, error) { return "reconcile-0001", nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	return connectFacade(t, facade)
}

// The description must not let a model believe verify returns a verdict. It
// opens validation; the judgement lands later, as evidence. A model told
// otherwise would read the immediate result as a pass and report success.
func TestFacade_VerifyDeclaresItRunsTheReviewedProfileAndDoesNotDecide(t *testing.T) {
	client := &fakeClient{verifyResult: localapi.TaskMutationResult{
		TaskHandle: "task-0001", State: domain.TaskValidating, StateVersion: 15,
		SideEffect: localapi.SideEffectMutate,
	}}
	session := verifyFacadeSession(t, client)

	tools, err := session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("ListTools() error = %v", err)
	}
	var found bool
	for _, tool := range tools.Tools {
		if tool.Name != ToolVerifyTask {
			continue
		}
		found = true
		description := strings.ToLower(tool.Description)
		for _, required := range []string{"reviewed profile", "does not decide"} {
			if !strings.Contains(description, required) {
				t.Errorf("verify_task description omits %q: %s", required, tool.Description)
			}
		}
		if tool.Annotations == nil || tool.Annotations.ReadOnlyHint ||
			tool.Annotations.DestructiveHint == nil || *tool.Annotations.DestructiveHint {
			t.Errorf("verify_task annotations = %#v, want a non-destructive mutation", tool.Annotations)
		}
	}
	if !found {
		t.Fatal("verify_task tool is absent")
	}

	result, callErr := session.CallTool(context.Background(), &mcp.CallToolParams{
		Meta: callMeta("verify-0001", "service-instance-0001"),
		Name: ToolVerifyTask, Arguments: TaskInput{TaskHandle: "task-0001"},
	})
	if callErr != nil || result.IsError {
		t.Fatalf("CallTool(verify_task) = %#v, %v", result, callErr)
	}
	if len(client.calls) != 1 || client.calls[0] != "verify:verify-0001:task-0001" {
		t.Fatalf("verify did not route through one canonical command: %v", client.calls)
	}
}

func TestFacade_AnUncertainVerifyIsReconciledNotResent(t *testing.T) {
	client := &fakeClient{
		verifyErrors: []error{&domain.Failure{
			Code: domain.ErrorUnavailable, Retryable: true, Message: "send uncertain",
		}},
		verifyResult: localapi.TaskMutationResult{
			TaskHandle: "task-0001", State: domain.TaskValidating, StateVersion: 15,
			SideEffect: localapi.SideEffectMutate,
		},
		operation: fixtureOperation("verify-0001", "VerifyTask"),
	}
	session := verifyFacadeSession(t, client)

	result, callErr := session.CallTool(context.Background(), &mcp.CallToolParams{
		Meta: callMeta("verify-0001", "service-instance-0001"),
		Name: ToolVerifyTask, Arguments: TaskInput{TaskHandle: "task-0001"},
	})
	if callErr != nil || result.IsError {
		t.Fatalf("CallTool(verify_task) = %#v, %v", result, callErr)
	}
	var reconciled bool
	for _, call := range client.calls {
		if strings.HasPrefix(call, "operation:") {
			reconciled = true
		}
	}
	if !reconciled {
		t.Fatalf("an uncertain verify must be reconciled against its operation: %v", client.calls)
	}
}
