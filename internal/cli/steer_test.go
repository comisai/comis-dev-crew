package cli

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/comisai/comis-dev-crew/internal/domain"
	"github.com/comisai/comis-dev-crew/internal/localapi"
)

func steerClient() *fakeClient {
	return &fakeClient{taskMutation: localapi.TaskMutationResult{
		TaskHandle: "task-0001", State: domain.TaskWorking, StateVersion: 19,
		SideEffect: localapi.SideEffectMutate,
	}}
}

func TestCLI_SteerCarriesTheInstructionExactlyAsTyped(t *testing.T) {
	client := steerClient()
	var output bytes.Buffer

	if code := Run(context.Background(),
		[]string{"task", "steer", "task-0001", "--instruction", "Prefer the existing parser.",
			"--operation", "operation-steer-0001"},
		&output, &output, testConfig(client),
	); code != ExitSuccess {
		t.Fatalf("Run(task steer) = %d: %s", code, output.String())
	}
	if len(client.calls) != 1 ||
		!strings.HasSuffix(client.calls[0], "steer:task-0001:Prefer the existing parser.") {
		t.Fatalf("steer did not carry the instruction as typed: %v", client.calls)
	}
}

// The instruction is plain text a human wrote. Control characters would let it
// smuggle terminal escape sequences into whatever renders it later, so the
// boundary refuses them before the service ever sees them.
func TestCLI_SteerRefusesAnInstructionThatIsNotPlainText(t *testing.T) {
	for name, args := range map[string][]string{
		"no instruction":  {"task", "steer", "task-0001"},
		"empty":           {"task", "steer", "task-0001", "--instruction", ""},
		"escape sequence": {"task", "steer", "task-0001", "--instruction", "clear \x1b[2J now"},
		"newline":         {"task", "steer", "task-0001", "--instruction", "one\ntwo"},
		"no reference":    {"task", "steer"},
		"non-JSON format": {"task", "steer", "task-0001", "--instruction", "Do it.", "--format", "table"},
	} {
		client := steerClient()
		var output bytes.Buffer
		if code := Run(context.Background(), args, &output, &output, testConfig(client)); code == ExitSuccess {
			t.Errorf("%s: Run(%v) succeeded, want a refusal", name, args)
		}
		if len(client.calls) != 0 {
			t.Errorf("%s: a refused steer must not reach the service: %v", name, client.calls)
		}
	}
}

func TestCLI_SteerAppearsInTheOperatorSurface(t *testing.T) {
	if !strings.Contains(usage, "task steer TASK --instruction TEXT") {
		t.Fatal("task steer is missing from the CLI usage text")
	}
}
