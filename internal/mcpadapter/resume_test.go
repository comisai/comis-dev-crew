package mcpadapter

import (
	"context"
	"strings"
	"testing"

	"github.com/comisai/comis-dev-crew/internal/domain"
	"github.com/comisai/comis-dev-crew/internal/localapi"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// A model reading only the tool list must learn that resume can be refused and
// what to do about it. Told nothing, it would retry the refusal instead of
// reaching for handback, and the developer's edit would sit unabsorbed.
func TestFacade_ResumeDeclaresItsDirtyWorktreeRefusalAndTheWayOut(t *testing.T) {
	client := &fakeClient{resumeResult: localapi.TaskMutationResult{
		TaskHandle: "task-0001", State: domain.TaskWorking, StateVersion: 13,
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
		if tool.Name != ToolResumeTask {
			continue
		}
		found = true
		description := strings.ToLower(tool.Description)
		for _, required := range []string{"uncommitted", "hand the work back"} {
			if !strings.Contains(description, required) {
				t.Errorf("resume_task description omits %q: %s", required, tool.Description)
			}
		}
		if tool.Annotations == nil || tool.Annotations.ReadOnlyHint ||
			tool.Annotations.DestructiveHint == nil || *tool.Annotations.DestructiveHint {
			t.Errorf("resume_task annotations = %#v, want a non-destructive mutation", tool.Annotations)
		}
	}
	if !found {
		t.Fatal("resume_task tool is absent")
	}

	result, callErr := session.CallTool(context.Background(), &mcp.CallToolParams{
		Meta: callMeta("resume-0001", "service-instance-0001"),
		Name: ToolResumeTask, Arguments: TaskInput{TaskHandle: "task-0001"},
	})
	if callErr != nil || result.IsError {
		t.Fatalf("CallTool(resume_task) = %#v, %v", result, callErr)
	}
	if len(client.calls) != 1 || client.calls[0] != "resume:resume-0001:task-0001" {
		t.Fatalf("resume did not route through one canonical command: %v", client.calls)
	}
}

func TestFacade_AnUncertainResumeIsReconciledNotResent(t *testing.T) {
	client := &fakeClient{
		resumeErrors: []error{&domain.Failure{
			Code: domain.ErrorUnavailable, Retryable: true, Message: "send uncertain",
		}},
		resumeResult: localapi.TaskMutationResult{
			TaskHandle: "task-0001", State: domain.TaskWorking, StateVersion: 13,
			SideEffect: localapi.SideEffectMutate,
		},
		operation: fixtureOperation("resume-0001", "ResumeTask"),
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
		Meta: callMeta("resume-0001", "service-instance-0001"),
		Name: ToolResumeTask, Arguments: TaskInput{TaskHandle: "task-0001"},
	})
	if callErr != nil || result.IsError {
		t.Fatalf("CallTool(resume_task) = %#v, %v", result, callErr)
	}
	var reconciled bool
	for _, call := range client.calls {
		if strings.HasPrefix(call, "operation:") {
			reconciled = true
		}
	}
	if !reconciled {
		t.Fatalf("an uncertain resume must be reconciled against its operation: %v", client.calls)
	}
}
