package mcpadapter

import (
	"context"
	"strings"
	"testing"

	"github.com/comisai/comis-dev-crew/internal/domain"
	"github.com/comisai/comis-dev-crew/internal/localapi"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func steerSession(t *testing.T, client *fakeClient) *mcp.ClientSession {
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

// The description must say the worker reads it on its next report and that the
// task does not change state. A model told otherwise would treat a successful
// steer as the instruction already having taken effect.
func TestFacade_SteerDeclaresWhenTheWorkerActuallySeesIt(t *testing.T) {
	client := &fakeClient{steerResult: localapi.TaskMutationResult{
		TaskHandle: "task-0001", State: domain.TaskWorking, StateVersion: 19,
		SideEffect: localapi.SideEffectMutate,
	}}
	session := steerSession(t, client)

	tools, err := session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("ListTools() error = %v", err)
	}
	var found bool
	for _, tool := range tools.Tools {
		if tool.Name != ToolSteerTask {
			continue
		}
		found = true
		description := strings.ToLower(tool.Description)
		for _, required := range []string{"next report", "does not change state"} {
			if !strings.Contains(description, required) {
				t.Errorf("steer_task description omits %q: %s", required, tool.Description)
			}
		}
	}
	if !found {
		t.Fatal("steer_task tool is absent")
	}

	result, callErr := session.CallTool(context.Background(), &mcp.CallToolParams{
		Meta:      callMeta("steer-0001", "service-instance-0001"),
		Name:      ToolSteerTask,
		Arguments: SteerTaskInput{TaskHandle: "task-0001", Instruction: "Prefer the existing parser."},
	})
	if callErr != nil || result.IsError {
		t.Fatalf("CallTool(steer_task) = %#v, %v", result, callErr)
	}
	if len(client.calls) != 1 ||
		client.calls[0] != "steer:steer-0001:task-0001:Prefer the existing parser." {
		t.Fatalf("steer did not route through one canonical command: %v", client.calls)
	}
}

// An uncertain steer is reconciled, never re-sent. Re-sending would queue the
// same words twice and the worker would act on them twice — the one failure the
// instruction path exists to avoid.
func TestFacade_AnUncertainSteerIsReconciledNotResent(t *testing.T) {
	client := &fakeClient{
		steerErrors: []error{&domain.Failure{
			Code: domain.ErrorUnavailable, Retryable: true, Message: "send uncertain",
		}},
		steerResult: localapi.TaskMutationResult{
			TaskHandle: "task-0001", State: domain.TaskWorking, StateVersion: 19,
			SideEffect: localapi.SideEffectMutate,
		},
		operation: fixtureOperation("steer-0001", "SteerTask"),
	}
	session := steerSession(t, client)

	result, callErr := session.CallTool(context.Background(), &mcp.CallToolParams{
		Meta:      callMeta("steer-0001", "service-instance-0001"),
		Name:      ToolSteerTask,
		Arguments: SteerTaskInput{TaskHandle: "task-0001", Instruction: "Prefer the existing parser."},
	})
	if callErr != nil || result.IsError {
		t.Fatalf("CallTool(steer_task) = %#v, %v", result, callErr)
	}
	var reconciled bool
	for _, call := range client.calls {
		if strings.HasPrefix(call, "operation:") {
			reconciled = true
		}
	}
	if !reconciled {
		t.Fatalf("an uncertain steer must be reconciled against its operation: %v", client.calls)
	}
}
