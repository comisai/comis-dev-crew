package cli

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/comisai/comis-dev-crew/internal/domain"
	"github.com/comisai/comis-dev-crew/internal/localapi"
)

func resumeClient() *fakeClient {
	return &fakeClient{taskMutation: localapi.TaskMutationResult{
		TaskHandle: "task-0001", State: domain.TaskWorking, StateVersion: 13,
		SideEffect: localapi.SideEffectMutate,
	}}
}

func TestCLI_ResumeRoutesThroughTheSameCanonicalCommandAsTheTool(t *testing.T) {
	client := resumeClient()
	var output bytes.Buffer

	if code := Run(context.Background(),
		[]string{"task", "resume", "task-0001", "--operation", "operation-resume-0001"},
		&output, &output, testConfig(client),
	); code != ExitSuccess {
		t.Fatalf("Run(task resume) = %d: %s", code, output.String())
	}
	if len(client.calls) != 1 || !strings.HasSuffix(client.calls[0], "resume:task-0001") {
		t.Fatalf("resume did not route through one canonical command: %v", client.calls)
	}
}

// Resume names no worker. Continuing what was already running and choosing a
// different worker are different decisions: the second needs a reconciled brief,
// and a flag that blurred them would let an operator swap workers believing they
// had only unpaused one.
func TestCLI_ResumeRefusesAWorkerSelectionOrAnythingBeyondOneReference(t *testing.T) {
	for name, args := range map[string][]string{
		"no reference":     {"task", "resume"},
		"forged reference": {"task", "resume", "../../etc"},
		"worker selection": {"task", "resume", "task-0001", "--worker", "codex-reviewed"},
		"force":            {"task", "resume", "task-0001", "--force", "true"},
		"non-JSON format":  {"task", "resume", "task-0001", "--format", "table"},
	} {
		client := resumeClient()
		var output bytes.Buffer
		if code := Run(context.Background(), args, &output, &output, testConfig(client)); code == ExitSuccess {
			t.Errorf("%s: Run(%v) succeeded, want a refusal", name, args)
		}
		if len(client.calls) != 0 {
			t.Errorf("%s: a refused resume must not reach the service: %v", name, client.calls)
		}
	}
}

func TestCLI_ResumeAppearsInTheOperatorSurface(t *testing.T) {
	if !strings.Contains(usage, "task resume TASK") {
		t.Fatal("task resume is missing from the CLI usage text")
	}
}
