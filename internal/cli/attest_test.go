package cli

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/comisai/comis-dev-crew/internal/domain"
	"github.com/comisai/comis-dev-crew/internal/localapi"
)

func attestClient() *fakeClient {
	return &fakeClient{taskMutation: localapi.TaskMutationResult{
		TaskHandle: "task-0001", State: domain.TaskWorking, StateVersion: 9,
		SideEffect: localapi.SideEffectMutate,
	}}
}

// The finding is typed, not inferred. An operator who ran the command without
// saying what they found has told the service nothing, and accepting that as
// "no decisions were open" is the silence this record exists to refuse.
func TestCLI_AttestRequiresAStatedFinding(t *testing.T) {
	client := attestClient()
	var output bytes.Buffer

	code := Run(context.Background(),
		[]string{"task", "attest", "task-0001", "--operation", "operation-attest-0001"},
		&output, &output, testConfig(client),
	)
	if code == 0 {
		t.Fatalf("Run(attest without a finding) succeeded: %s", output.String())
	}
	if len(client.calls) != 0 {
		t.Errorf("an unstated inventory reached the service: %v", client.calls)
	}
}

// Each key is a separate argument and validated on its own, so a shell cannot
// smuggle a list through one value and an invalid key is refused by name.
func TestCLI_AttestCarriesEachStatedKeyToTheService(t *testing.T) {
	client := attestClient()
	var output bytes.Buffer

	code := Run(context.Background(),
		[]string{
			"task", "attest", "task-0001", "--finding", "open_decisions",
			"--open-decision", "schema-choice", "--open-decision", "rollout-window",
			"--operation", "operation-attest-0001",
		},
		&output, &output, testConfig(client),
	)
	if code != 0 {
		t.Fatalf("Run(task attest) = %d: %s", code, output.String())
	}
	if len(client.calls) != 1 || !strings.HasSuffix(client.calls[0], "attest:task-0001:open_decisions:schema-choice,rollout-window") {
		t.Fatalf("attest did not route through one canonical command: %v", client.calls)
	}
}

func TestCLI_AttestRecordsAClearedInventory(t *testing.T) {
	client := attestClient()
	var output bytes.Buffer

	code := Run(context.Background(),
		[]string{
			"task", "attest", "task-0001", "--finding", "no_open_decisions",
			"--operation", "operation-attest-0001",
		},
		&output, &output, testConfig(client),
	)
	if code != 0 {
		t.Fatalf("Run(task attest cleared) = %d: %s", code, output.String())
	}
	if len(client.calls) != 1 || !strings.HasSuffix(client.calls[0], "attest:task-0001:no_open_decisions:") {
		t.Fatalf("cleared attestation calls = %v", client.calls)
	}
}

func TestCLI_AttestRefusesForgedReferencesAndUnknownOptions(t *testing.T) {
	for label, args := range map[string][]string{
		"no reference":      {"task", "attest"},
		"forged reference":  {"task", "attest", "../../etc", "--finding", "no_open_decisions"},
		"unknown finding":   {"task", "attest", "task-0001", "--finding", "probably-fine"},
		"forged key":        {"task", "attest", "task-0001", "--finding", "open_decisions", "--open-decision", "../../etc"},
		"repeated finding":  {"task", "attest", "task-0001", "--finding", "open_decisions", "--finding", "no_open_decisions"},
		"unknown option":    {"task", "attest", "task-0001", "--finding", "no_open_decisions", "--force", "true"},
		"non-JSON format":   {"task", "attest", "task-0001", "--finding", "no_open_decisions", "--format", "table"},
		"dangling argument": {"task", "attest", "task-0001", "--finding"},
	} {
		client := attestClient()
		var output bytes.Buffer
		if code := Run(context.Background(), args, &output, &output, testConfig(client)); code == 0 {
			t.Errorf("Run(%s) succeeded: %s", label, output.String())
		}
		if len(client.calls) != 0 {
			t.Errorf("%s reached the service: %v", label, client.calls)
		}
	}
}

func (client *fakeClient) AttestScoutDecisions(
	_ context.Context,
	operationID string,
	input localapi.AttestScoutDecisionsInput,
) (localapi.TaskMutationResult, error) {
	client.calls = append(
		client.calls,
		operationID+":attest:"+input.TaskHandle+":"+string(input.Finding)+":"+
			strings.Join(input.OpenDecisionKeys, ","),
	)
	return client.taskMutation, client.err
}
