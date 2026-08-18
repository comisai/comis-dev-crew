package mcpadapter

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/comisai/comis-dev-crew/internal/application"
	"github.com/comisai/comis-dev-crew/internal/domain"
	"github.com/comisai/comis-dev-crew/internal/localapi"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func pauseFacade(t *testing.T, client *fakeClient) *mcp.ClientSession {
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

// Pause is a mutate tool, not a destructive one. It asks a worker to stop
// cleanly and preserves everything; annotating it destructive would put it
// behind the same confirmation as cleanup and discourage the one intervention
// that makes a worktree safe to hand to a developer.
func TestFacade_PauseIsAMutationThatPreservesWork(t *testing.T) {
	client := &fakeClient{pauseResult: localapi.TaskMutationResult{
		TaskHandle: "task-0001", State: domain.TaskWorking, StateVersion: 9,
		SideEffect: localapi.SideEffectMutate,
	}}
	session := pauseFacade(t, client)

	tools, err := session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("ListTools() error = %v", err)
	}
	var found bool
	for _, tool := range tools.Tools {
		if tool.Name != ToolPauseTask {
			continue
		}
		found = true
		if tool.Annotations == nil || tool.Annotations.ReadOnlyHint ||
			tool.Annotations.DestructiveHint == nil || *tool.Annotations.DestructiveHint {
			t.Errorf("pause_task annotations = %#v, want a non-destructive mutation", tool.Annotations)
		}
		// The description must not promise the task is paused on return; the
		// worker settles and reports, and a model told otherwise would hand a
		// developer a worktree still being written to.
		if !strings.Contains(strings.ToLower(tool.Description), "safe boundary") {
			t.Errorf("pause_task description omits what it actually does: %s", tool.Description)
		}
	}
	if !found {
		t.Fatal("pause_task tool is absent")
	}
}

func TestFacade_PauseRoutesThroughTheCanonicalCommand(t *testing.T) {
	client := &fakeClient{pauseResult: localapi.TaskMutationResult{
		TaskHandle: "task-0001", State: domain.TaskWorking, StateVersion: 9,
		SideEffect: localapi.SideEffectMutate,
	}}
	session := pauseFacade(t, client)

	result, callErr := session.CallTool(context.Background(), &mcp.CallToolParams{
		Meta: callMeta("pause-0001", "service-instance-0001"),
		Name: ToolPauseTask, Arguments: TaskInput{TaskHandle: "task-0001"},
	})
	if callErr != nil || result.IsError {
		t.Fatalf("CallTool(pause_task) = %#v, %v", result, callErr)
	}
	if len(client.calls) != 1 || client.calls[0] != "pause:pause-0001:task-0001" {
		t.Fatalf("pause did not route through one canonical command: %v", client.calls)
	}
}

// An uncertain send is resolved by asking what the recorded operation did, not
// by sending again. Re-sending would report the second attempt's outcome as if
// it were the first, which is exactly what makes an uncertain send
// indistinguishable from a refused one.
func TestFacade_AnUncertainPauseIsReconciledNotResent(t *testing.T) {
	client := &fakeClient{
		pauseErrors: []error{&domain.Failure{
			Code: domain.ErrorUnavailable, Retryable: true, Message: "send uncertain",
		}},
		pauseResult: localapi.TaskMutationResult{
			TaskHandle: "task-0001", State: domain.TaskWorking, StateVersion: 9,
			SideEffect: localapi.SideEffectMutate,
		},
		operation: fixtureOperation("pause-0001", "PauseTask"),
	}
	session := pauseFacade(t, client)

	result, callErr := session.CallTool(context.Background(), &mcp.CallToolParams{
		Meta: callMeta("pause-0001", "service-instance-0001"),
		Name: ToolPauseTask, Arguments: TaskInput{TaskHandle: "task-0001"},
	})
	if callErr != nil || result.IsError {
		t.Fatalf("CallTool(pause_task) = %#v, %v", result, callErr)
	}
	var reconciled bool
	for _, call := range client.calls {
		if strings.HasPrefix(call, "operation:") {
			reconciled = true
		}
	}
	if !reconciled {
		t.Fatalf("an uncertain pause must be reconciled against its operation: %v", client.calls)
	}
}

// A pause whose operation records a different command must not be reported as
// this pause's outcome: reconciliation reads one exact operation, and a mismatch
// means the send is still uncertain.
func TestFacade_APauseReconciledAgainstAForeignOperationStaysUncertain(t *testing.T) {
	original := &domain.Failure{Code: domain.ErrorUnavailable, Retryable: true, Message: "send uncertain"}
	client := &fakeClient{
		pauseErrors: []error{original},
		operation:   fixtureOperation("pause-0001", "CleanupTask"),
	}
	session := pauseFacade(t, client)

	result, callErr := session.CallTool(context.Background(), &mcp.CallToolParams{
		Meta: callMeta("pause-0001", "service-instance-0001"),
		Name: ToolPauseTask, Arguments: TaskInput{TaskHandle: "task-0001"},
	})
	if callErr == nil && (result == nil || !result.IsError) {
		t.Fatalf("CallTool(pause_task) = %#v, %v, want the original uncertainty", result, callErr)
	}
	if errors.Is(callErr, context.Canceled) {
		t.Fatal("an uncertain pause must not be reported as a cancellation")
	}
}

func fixtureOperation(operationID, command string) application.OperationView {
	return application.OperationView{
		SchemaVersion: 1, OperationID: operationID, Command: command,
		Status: domain.OperationCompleted, StateVersion: 7,
	}
}
