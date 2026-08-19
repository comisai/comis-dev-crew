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

// The instruction is plain text a human wrote. Control characters would let it
// smuggle terminal escape sequences into whatever renders it later, so the
// boundary refuses them before the service ever sees them.
func TestCLI_SteerRefusesAnInstructionThatIsNotPlainText(t *testing.T) {
	for name, testCase := range map[string]struct {
		args     []string
		contract string
	}{
		"no contract":     {[]string{"task", "steer", "task-0001"}, ``},
		"empty":           {[]string{"task", "steer", "task-0001", "--input", "-"}, `{"schemaVersion":1,"instruction":""}`},
		"escape sequence": {[]string{"task", "steer", "task-0001", "--input", "-"}, "{\"schemaVersion\":1,\"instruction\":\"clear \\u001b[2J now\"}"},
		"newline":         {[]string{"task", "steer", "task-0001", "--input", "-"}, `{"schemaVersion":1,"instruction":"one\ntwo"}`},
		"no reference":    {[]string{"task", "steer"}, ``},
		"wrong version":   {[]string{"task", "steer", "task-0001", "--input", "-"}, `{"schemaVersion":2,"instruction":"Do it."}`},
		"unknown field":   {[]string{"task", "steer", "task-0001", "--input", "-"}, `{"schemaVersion":1,"instruction":"Do it.","force":true}`},
		"non-JSON format": {[]string{"task", "steer", "task-0001", "--input", "-", "--format", "table"}, `{"schemaVersion":1,"instruction":"Do it."}`},
	} {
		client := steerClient()
		var output bytes.Buffer
		config := testConfig(client)
		config.Stdin = strings.NewReader(testCase.contract)
		if code := Run(context.Background(), testCase.args, &output, &output, config); code == ExitSuccess {
			t.Errorf("%s: Run(%v) succeeded, want a refusal", name, testCase.args)
		}
		if len(client.calls) != 0 {
			t.Errorf("%s: a refused steer must not reach the service: %v", name, client.calls)
		}
	}
}

func TestCLI_SteerAppearsInTheOperatorSurface(t *testing.T) {
	if !strings.Contains(usage, "task steer TASK --input FILE|-") {
		t.Fatal("task steer is missing from the CLI usage text")
	}
}

// A steering instruction is worker-visible text, so it arrives as a bounded
// contract from a file or standard input rather than on the command line.
// Argv is visible in process listings and shell history, and an instruction
// long enough to be useful does not belong there.
func TestCLI_SteersFromABoundedContract(t *testing.T) {
	client := steerClient()
	var output bytes.Buffer
	config := testConfig(client)
	config.Stdin = strings.NewReader(`{"schemaVersion":1,"instruction":"Prefer the existing parser."}`)

	args := []string{"task", "steer", "task-0001", "--input", "-", "--operation", "operation-steer-0001"}
	if code := Run(context.Background(), args, &output, &output, config); code != 0 {
		t.Fatalf("Run(task steer --input) = %d: %s", code, output.String())
	}
	if len(client.calls) != 1 ||
		!strings.HasSuffix(client.calls[0], "steer:task-0001:Prefer the existing parser.") {
		t.Fatalf("client calls = %v", client.calls)
	}
}

func TestCLI_RefusesSteeringWithoutAContract(t *testing.T) {
	client := steerClient()
	var output bytes.Buffer
	config := testConfig(client)
	config.Stdin = strings.NewReader("")
	args := []string{"task", "steer", "task-0001"}
	if code := Run(context.Background(), args, &output, &output, config); code != ExitUsage {
		t.Fatalf("Run(task steer without a contract) = %d, want %d", code, ExitUsage)
	}
	if len(client.calls) != 0 {
		t.Fatalf("a steer without a contract reached the service: %v", client.calls)
	}
}
