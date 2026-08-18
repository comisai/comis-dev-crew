package cli

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/comisai/comis-dev-crew/internal/localapi"
)

const promotionContract = `{
  "acceptanceCriteria": ["The investigated change is implemented and proven."],
  "constraints": ["Preserve unrelated changes."],
  "validationProfile": "go-default",
  "deliveryMode": "pull_request",
  "workerProfileId": "codex-reviewed"
}`

func promoteConfig(t *testing.T, client *fakeClient, contract string) Config {
	t.Helper()
	config := testConfig(client)
	config.Stdin = strings.NewReader(contract)
	return config
}

func TestCLI_PromoteRoutesTheCommandLineScoutNotTheContractsIdeaOfOne(t *testing.T) {
	client := &fakeClient{prepared: localapi.PrepareTaskResult{
		SchemaVersion: 1, OperationID: "operation-promote-0001", TaskHandle: "task-ship-0001",
		State: "prepared", StateVersion: 1, SideEffect: localapi.SideEffectMutate,
	}}
	var output bytes.Buffer

	if code := Run(context.Background(),
		[]string{"task", "promote", "task-scout-0001", "--input", "-", "--operation", "operation-promote-0001"},
		&output, &output, promoteConfig(t, client, promotionContract),
	); code != ExitSuccess {
		t.Fatalf("Run(task promote) = %d: %s", code, output.String())
	}
	if len(client.calls) != 1 || !strings.HasSuffix(client.calls[0], "promote:task-scout-0001") {
		t.Fatalf("promotion did not carry the command-line scout: %v", client.calls)
	}
}

// An operator promoting a scout names it where they can see it. A contract that
// could also name one would let the file disagree with the command, and nothing
// afterwards would say which the service acted on.
func TestCLI_PromoteRefusesAContractThatNamesItsOwnScout(t *testing.T) {
	client := &fakeClient{}
	forged := `{"scoutTaskHandle":"task-scout-other","acceptanceCriteria":["x"],` +
		`"constraints":[],"validationProfile":"go-default",` +
		`"deliveryMode":"pull_request","workerProfileId":"codex-reviewed"}`
	var output bytes.Buffer

	code := Run(context.Background(),
		[]string{"task", "promote", "task-scout-0001", "--input", "-"},
		&output, &output, promoteConfig(t, client, forged),
	)

	if code == ExitSuccess {
		t.Fatalf("Run(forged contract) succeeded: %s", output.String())
	}
	if len(client.calls) != 0 {
		t.Errorf("a refused promotion must not reach the service: %v", client.calls)
	}
}

// The contract carries no repository or base revision at all: both are inherited
// from the scout so a promotion cannot aim the ship task at code the
// investigation never covered.
func TestCLI_PromoteRefusesARepositoryOrBaseRevisionInTheContract(t *testing.T) {
	for name, contract := range map[string]string{
		"repository": `{"repositoryId":"other-repo","acceptanceCriteria":["x"],"constraints":[],` +
			`"validationProfile":"go-default","deliveryMode":"pull_request","workerProfileId":"codex-reviewed"}`,
		"base revision": `{"baseRevision":"` + strings.Repeat("a", 40) + `","acceptanceCriteria":["x"],` +
			`"constraints":[],"validationProfile":"go-default","deliveryMode":"pull_request","workerProfileId":"codex-reviewed"}`,
	} {
		client := &fakeClient{}
		var output bytes.Buffer
		if code := Run(context.Background(),
			[]string{"task", "promote", "task-scout-0001", "--input", "-"},
			&output, &output, promoteConfig(t, client, contract),
		); code == ExitSuccess {
			t.Errorf("%s: Run() succeeded, want a refusal: %s", name, output.String())
		}
		if len(client.calls) != 0 {
			t.Errorf("%s: a refused promotion must not reach the service: %v", name, client.calls)
		}
	}
}

func TestCLI_PromoteRefusesAMissingScoutOrContract(t *testing.T) {
	for name, args := range map[string][]string{
		"no scout":        {"task", "promote"},
		"forged scout":    {"task", "promote", "../../etc", "--input", "-"},
		"no input":        {"task", "promote", "task-scout-0001"},
		"non-JSON format": {"task", "promote", "task-scout-0001", "--input", "-", "--format", "table"},
	} {
		client := &fakeClient{}
		var output bytes.Buffer
		if code := Run(context.Background(), args, &output, &output,
			promoteConfig(t, client, promotionContract)); code == ExitSuccess {
			t.Errorf("%s: Run(%v) succeeded, want a refusal", name, args)
		}
	}
}

func TestCLI_PromoteAppearsInTheOperatorSurface(t *testing.T) {
	if !strings.Contains(usage, "task promote SCOUT") {
		t.Fatal("task promote is missing from the CLI usage text")
	}
}
