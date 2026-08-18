package cli

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/comisai/comis-dev-crew/internal/domain"
	"github.com/comisai/comis-dev-crew/internal/localapi"
)

func discardClient() *fakeClient {
	return &fakeClient{taskMutation: localapi.TaskMutationResult{
		TaskHandle: "task-0001", State: domain.TaskCleaned, StateVersion: 21,
		SideEffect: localapi.SideEffectMutate,
	}}
}

// The acknowledgement is the only gate a discard has. Cleanup can prove removal
// is safe by pointing at delivered work; a discard has nothing to point at, so
// the operator typing it is what stands between them and irreversible deletion.
func TestCLI_DiscardRefusesWithoutAnExplicitAcknowledgement(t *testing.T) {
	client := discardClient()
	var output bytes.Buffer

	code := Run(context.Background(),
		[]string{"task", "discard", "task-0001", "--operation", "operation-discard-0001"},
		&output, &output, testConfig(client))

	if code == ExitSuccess {
		t.Fatalf("Run(discard without --yes) succeeded: %s", output.String())
	}
	if len(client.calls) != 0 {
		t.Errorf("an unacknowledged discard must not reach the service: %v", client.calls)
	}
}

func TestCLI_DiscardReachesTheServiceOnceAcknowledged(t *testing.T) {
	client := discardClient()
	var output bytes.Buffer

	if code := Run(context.Background(),
		[]string{"task", "discard", "task-0001", "--yes", "--operation", "operation-discard-0001"},
		&output, &output, testConfig(client),
	); code != ExitSuccess {
		t.Fatalf("Run(task discard --yes) = %d: %s", code, output.String())
	}
	if len(client.calls) != 1 || !strings.HasSuffix(client.calls[0], "discard:task-0001") {
		t.Fatalf("discard did not route through one canonical command: %v", client.calls)
	}
}

func TestCLI_DiscardRefusesAForgedReferenceOrUnknownOption(t *testing.T) {
	for name, args := range map[string][]string{
		"no reference":     {"task", "discard"},
		"forged reference": {"task", "discard", "../../etc", "--yes"},
		"repeated flag":    {"task", "discard", "task-0001", "--yes", "--yes"},
		"unknown option":   {"task", "discard", "task-0001", "--yes", "--force", "true"},
		"non-JSON format":  {"task", "discard", "task-0001", "--yes", "--format", "table"},
	} {
		client := discardClient()
		var output bytes.Buffer
		if code := Run(context.Background(), args, &output, &output, testConfig(client)); code == ExitSuccess {
			t.Errorf("%s: Run(%v) succeeded, want a refusal", name, args)
		}
		if len(client.calls) != 0 {
			t.Errorf("%s: a refused discard must not reach the service: %v", name, client.calls)
		}
	}
}

func TestCLI_DiscardAppearsInTheOperatorSurface(t *testing.T) {
	if !strings.Contains(usage, "task discard TASK --yes") {
		t.Fatal("task discard is missing from the CLI usage text")
	}
}
