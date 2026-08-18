package mcpadapter

import (
	"context"
	"strings"
	"testing"

	"github.com/comisai/comis-dev-crew/internal/domain"
	"github.com/comisai/comis-dev-crew/internal/localapi"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Replacement preserves the work. A model reading the tool list must not take it
// for a way to start over, or it would reach for replace when the intent was to
// discard — and the two are not interchangeable.
func TestFacade_ReplaceDeclaresThatItPreservesTheWork(t *testing.T) {
	client := &fakeClient{replaceResult: localapi.TaskMutationResult{
		TaskHandle: "task-0001", State: domain.TaskReady, StateVersion: 17,
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
		if tool.Name != ToolReplaceWorker {
			continue
		}
		found = true
		description := strings.ToLower(tool.Description)
		for _, required := range []string{"preserved", "reviewed"} {
			if !strings.Contains(description, required) {
				t.Errorf("replace_worker description omits %q: %s", required, tool.Description)
			}
		}
		if tool.Annotations == nil || tool.Annotations.DestructiveHint == nil ||
			*tool.Annotations.DestructiveHint {
			t.Errorf("replace_worker annotations = %#v, want a non-destructive mutation", tool.Annotations)
		}
	}
	if !found {
		t.Fatal("replace_worker tool is absent")
	}

	result, callErr := session.CallTool(context.Background(), &mcp.CallToolParams{
		Meta:      callMeta("replace-0001", "service-instance-0001"),
		Name:      ToolReplaceWorker,
		Arguments: ReplaceWorkerInput{TaskHandle: "task-0001", WorkerProfileID: "claude-reviewed"},
	})
	if callErr != nil || result.IsError {
		t.Fatalf("CallTool(replace_worker) = %#v, %v", result, callErr)
	}
	if len(client.calls) != 1 || client.calls[0] != "replace:replace-0001:task-0001:claude-reviewed" {
		t.Fatalf("replace did not route through one canonical command: %v", client.calls)
	}
}

func TestFacade_AnUncertainReplacementIsReconciledNotResent(t *testing.T) {
	client := &fakeClient{
		replaceErrors: []error{&domain.Failure{
			Code: domain.ErrorUnavailable, Retryable: true, Message: "send uncertain",
		}},
		replaceResult: localapi.TaskMutationResult{
			TaskHandle: "task-0001", State: domain.TaskReady, StateVersion: 17,
			SideEffect: localapi.SideEffectMutate,
		},
		operation: fixtureOperation("replace-0001", "ReplaceWorker"),
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
		Meta:      callMeta("replace-0001", "service-instance-0001"),
		Name:      ToolReplaceWorker,
		Arguments: ReplaceWorkerInput{TaskHandle: "task-0001", WorkerProfileID: "claude-reviewed"},
	})
	if callErr != nil || result.IsError {
		t.Fatalf("CallTool(replace_worker) = %#v, %v", result, callErr)
	}
	var reconciled bool
	for _, call := range client.calls {
		if strings.HasPrefix(call, "operation:") {
			reconciled = true
		}
	}
	if !reconciled {
		t.Fatalf("an uncertain replacement must be reconciled against its operation: %v", client.calls)
	}
}
