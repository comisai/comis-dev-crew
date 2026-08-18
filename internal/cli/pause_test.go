package cli

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/comisai/comis-dev-crew/internal/domain"
	"github.com/comisai/comis-dev-crew/internal/localapi"
)

func pauseClient() *fakeClient {
	return &fakeClient{taskMutation: localapi.TaskMutationResult{
		TaskHandle: "task-0001", State: domain.TaskWorking, StateVersion: 9,
		SideEffect: localapi.SideEffectMutate,
	}}
}

// The CLI and the MCP tool are two adapters over one command. If the CLI
// reached a different handler, the two surfaces could disagree about what a
// pause did — which is the duplication the single-command rule exists to stop.
func TestCLI_PauseRoutesThroughTheSameCanonicalCommandAsTheTool(t *testing.T) {
	client := pauseClient()
	var output bytes.Buffer

	if code := Run(context.Background(),
		[]string{"task", "pause", "task-0001", "--operation", "operation-pause-0001"},
		&output, &output, testConfig(client),
	); code != ExitSuccess {
		t.Fatalf("Run(task pause) = %d: %s", code, output.String())
	}
	if len(client.calls) != 1 || !strings.HasSuffix(client.calls[0], "pause:task-0001") {
		t.Fatalf("pause did not route through one canonical command: %v", client.calls)
	}
}

// Pause takes a task and nothing else. An instruction flag would make one
// command mean two things to the worker, and an operator reading the transcript
// afterwards could not tell which had been asked for.
func TestCLI_PauseRefusesAnythingBeyondOneTaskReference(t *testing.T) {
	for name, args := range map[string][]string{
		"no reference":     {"task", "pause"},
		"forged reference": {"task", "pause", "../../etc"},
		"instruction text": {"task", "pause", "task-0001", "--message", "stop now"},
		"interrupt":        {"task", "pause", "task-0001", "--interrupt", "true"},
		"non-JSON format":  {"task", "pause", "task-0001", "--format", "table"},
		"repeated option":  {"task", "pause", "task-0001", "--operation", "operation-pause-0001", "--operation", "operation-pause-0002"},
	} {
		client := pauseClient()
		var output bytes.Buffer
		if code := Run(context.Background(), args, &output, &output, testConfig(client)); code == ExitSuccess {
			t.Errorf("%s: Run(%v) succeeded, want a refusal", name, args)
		}
		if len(client.calls) != 0 {
			t.Errorf("%s: a refused pause must not reach the service: %v", name, client.calls)
		}
	}
}

// The usage text is the operator's only index of what exists. A command absent
// from it is a command nobody finds.
func TestCLI_PauseAppearsInTheOperatorSurface(t *testing.T) {
	if !strings.Contains(usage, "task pause TASK") {
		t.Fatal("task pause is missing from the CLI usage text")
	}
}
