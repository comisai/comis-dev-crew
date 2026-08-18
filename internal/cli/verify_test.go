package cli

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/comisai/comis-dev-crew/internal/domain"
	"github.com/comisai/comis-dev-crew/internal/localapi"
)

func verifyClient() *fakeClient {
	return &fakeClient{taskMutation: localapi.TaskMutationResult{
		TaskHandle: "task-0001", State: domain.TaskValidating, StateVersion: 15,
		SideEffect: localapi.SideEffectMutate,
	}}
}

func TestCLI_VerifyRoutesThroughTheSameCanonicalCommandAsTheTool(t *testing.T) {
	client := verifyClient()
	var output bytes.Buffer

	if code := Run(context.Background(),
		[]string{"task", "verify", "task-0001", "--operation", "operation-verify-0001"},
		&output, &output, testConfig(client),
	); code != ExitSuccess {
		t.Fatalf("Run(task verify) = %d: %s", code, output.String())
	}
	if len(client.calls) != 1 || !strings.HasSuffix(client.calls[0], "verify:task-0001") {
		t.Fatalf("verify did not route through one canonical command: %v", client.calls)
	}
}

// Verify selects no profile and no checks. A caller able to choose either could
// validate against an easier bar than the one the task was accepted under, and
// the resulting evidence would claim a standard the run never met.
func TestCLI_VerifyRefusesToChooseItsOwnChecks(t *testing.T) {
	for name, args := range map[string][]string{
		"no reference":      {"task", "verify"},
		"forged reference":  {"task", "verify", "../../etc"},
		"profile selection": {"task", "verify", "task-0001", "--profile", "go-lenient"},
		"check selection":   {"task", "verify", "task-0001", "--checks", "lint"},
		"skip":              {"task", "verify", "task-0001", "--skip", "tests"},
		"non-JSON format":   {"task", "verify", "task-0001", "--format", "table"},
	} {
		client := verifyClient()
		var output bytes.Buffer
		if code := Run(context.Background(), args, &output, &output, testConfig(client)); code == ExitSuccess {
			t.Errorf("%s: Run(%v) succeeded, want a refusal", name, args)
		}
		if len(client.calls) != 0 {
			t.Errorf("%s: a refused verify must not reach the service: %v", name, client.calls)
		}
	}
}

func TestCLI_VerifyAppearsInTheOperatorSurface(t *testing.T) {
	if !strings.Contains(usage, "task verify TASK") {
		t.Fatal("task verify is missing from the CLI usage text")
	}
}
