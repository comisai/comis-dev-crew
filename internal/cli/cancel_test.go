package cli

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/comisai/comis-dev-crew/internal/domain"
	"github.com/comisai/comis-dev-crew/internal/localapi"
)

func cancelClient() *fakeClient {
	return &fakeClient{taskMutation: localapi.TaskMutationResult{
		TaskHandle: "task-0001", State: domain.TaskCancelled, StateVersion: 11,
		SideEffect: localapi.SideEffectMutate,
	}}
}

func TestCLI_CancelRoutesThroughTheSameCanonicalCommandAsTheTool(t *testing.T) {
	client := cancelClient()
	var output bytes.Buffer

	if code := Run(context.Background(),
		[]string{"task", "cancel", "task-0001", "--operation", "operation-cancel-0001"},
		&output, &output, testConfig(client),
	); code != ExitSuccess {
		t.Fatalf("Run(task cancel) = %d: %s", code, output.String())
	}
	if len(client.calls) != 1 || !strings.HasSuffix(client.calls[0], "cancel:task-0001") {
		t.Fatalf("cancel did not route through one canonical command: %v", client.calls)
	}
}

// Cancel names no disposition. A flag that could also remove the worktree would
// put "stop" and "throw away" behind one command, and an operator stopping work
// under uncertainty is exactly who must not be able to discard it by accident.
func TestCLI_CancelRefusesADispositionOrAnythingBeyondOneReference(t *testing.T) {
	for name, args := range map[string][]string{
		"no reference":     {"task", "cancel"},
		"forged reference": {"task", "cancel", "../../etc"},
		"disposition":      {"task", "cancel", "task-0001", "--discard", "true"},
		"non-JSON format":  {"task", "cancel", "task-0001", "--format", "table"},
		"repeated option":  {"task", "cancel", "task-0001", "--operation", "operation-cancel-0001", "--operation", "operation-cancel-0002"},
	} {
		client := cancelClient()
		var output bytes.Buffer
		if code := Run(context.Background(), args, &output, &output, testConfig(client)); code == ExitSuccess {
			t.Errorf("%s: Run(%v) succeeded, want a refusal", name, args)
		}
		if len(client.calls) != 0 {
			t.Errorf("%s: a refused cancel must not reach the service: %v", name, client.calls)
		}
	}
}

func TestCLI_CancelAppearsInTheOperatorSurface(t *testing.T) {
	if !strings.Contains(usage, "task cancel TASK") {
		t.Fatal("task cancel is missing from the CLI usage text")
	}
}
