package cli

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/comisai/comis-dev-crew/internal/domain"
	"github.com/comisai/comis-dev-crew/internal/localapi"
)

func replaceClient() *fakeClient {
	return &fakeClient{taskMutation: localapi.TaskMutationResult{
		TaskHandle: "task-0001", State: domain.TaskReady, StateVersion: 17,
		SideEffect: localapi.SideEffectMutate,
	}}
}

func TestCLI_ReplaceCarriesBothTheTaskAndTheProfileTakingOver(t *testing.T) {
	client := replaceClient()
	var output bytes.Buffer

	if code := Run(context.Background(),
		[]string{"task", "replace", "task-0001", "--worker", "claude-reviewed",
			"--operation", "operation-replace-0001"},
		&output, &output, testConfig(client),
	); code != ExitSuccess {
		t.Fatalf("Run(task replace) = %d: %s", code, output.String())
	}
	if len(client.calls) != 1 || !strings.HasSuffix(client.calls[0], "replace:task-0001:claude-reviewed") {
		t.Fatalf("replace did not carry both the task and the profile: %v", client.calls)
	}
}

// The profile is required. A replacement with none would have to pick a worker
// on the operator's behalf, and choosing who continues the work is exactly the
// decision this command exists to make explicit.
func TestCLI_ReplaceRefusesToChooseAWorkerItself(t *testing.T) {
	for name, args := range map[string][]string{
		"no profile":       {"task", "replace", "task-0001"},
		"empty profile":    {"task", "replace", "task-0001", "--worker", ""},
		"forged profile":   {"task", "replace", "task-0001", "--worker", "../../bin/sh"},
		"no reference":     {"task", "replace"},
		"forged reference": {"task", "replace", "../../etc", "--worker", "claude-reviewed"},
		"non-JSON format":  {"task", "replace", "task-0001", "--worker", "claude-reviewed", "--format", "table"},
	} {
		client := replaceClient()
		var output bytes.Buffer
		if code := Run(context.Background(), args, &output, &output, testConfig(client)); code == ExitSuccess {
			t.Errorf("%s: Run(%v) succeeded, want a refusal", name, args)
		}
		if len(client.calls) != 0 {
			t.Errorf("%s: a refused replacement must not reach the service: %v", name, client.calls)
		}
	}
}

func TestCLI_ReplaceAppearsInTheOperatorSurface(t *testing.T) {
	if !strings.Contains(usage, "task replace TASK --worker PROFILE") {
		t.Fatal("task replace is missing from the CLI usage text")
	}
}
