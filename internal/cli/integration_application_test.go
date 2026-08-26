package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/comisai/comis-dev-crew/internal/application"
	"github.com/comisai/comis-dev-crew/internal/localapi"
)

const integrationCLIContract = `{
  "integrationTaskHandle":"task-integration",
  "candidateTaskHandle":"task-candidate",
  "candidateHead":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
  "expectedIntegrationHead":"cccccccccccccccccccccccccccccccccccccccc"
}`

func TestCLIInitiativeIntegrationUsesStrictContractAndJSONResult(t *testing.T) {
	client := fixtureClient()
	client.integrationResult = localapi.ApplyIntegrationCandidateResult{
		SchemaVersion: 1, OperationID: "operation-integration-cli",
		InitiativeHandle: "initiative-cli", IntegrationTaskHandle: "task-integration",
		CandidateTaskHandle: "task-candidate", RepositoryID: "repo-primary",
		CandidateHead: strings.Repeat("b", 40), EvidenceDigest: strings.Repeat("e", 64),
		Strategy: application.IntegrationMerge, Outcome: application.IntegrationApplied,
		PreviousHead: strings.Repeat("c", 40), ResultingHead: strings.Repeat("d", 40),
		StateVersion: 51, CompletedAtMs: time.Date(2026, time.August, 20, 16, 0, 0, 0, time.UTC).UnixMilli(),
		SideEffect: localapi.SideEffectMutate,
	}
	config := testConfig(client)
	config.Stdin = strings.NewReader(integrationCLIContract)
	var stdout, stderr bytes.Buffer
	code := Run(context.Background(), []string{
		"initiative", "integrate", "initiative-cli", "--input", "-",
		"--operation", "operation-integration-cli", "--format", "json",
	}, &stdout, &stderr, config)
	if code != ExitSuccess {
		t.Fatalf("Run(initiative integrate) = %d, stderr=%q", code, stderr.String())
	}
	var result localapi.ApplyIntegrationCandidateResult
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil || result.StateVersion != 51 ||
		result.Outcome != application.IntegrationApplied {
		t.Fatalf("integration JSON = %#v, %v; raw=%q", result, err, stdout.String())
	}
	if client.operationID != "operation-integration-cli" ||
		client.integrationInput.InitiativeHandle != "initiative-cli" ||
		client.integrationInput.IntegrationTaskHandle != "task-integration" ||
		client.integrationInput.CandidateHead != strings.Repeat("b", 40) {
		t.Fatalf("integration client input = %#v / %q", client.integrationInput, client.operationID)
	}
}

func TestCLIInitiativeIntegrationRejectsAuthorityBroadeningBeforeConnecting(t *testing.T) {
	for name, test := range map[string]struct {
		args     []string
		contract string
	}{
		"missing initiative": {args: []string{"initiative", "integrate"}},
		"invalid initiative": {
			args: []string{"initiative", "integrate", "../escape", "--input", "-"}, contract: integrationCLIContract,
		},
		"missing input": {args: []string{"initiative", "integrate", "initiative-cli"}},
		"host path": {
			args:     []string{"initiative", "integrate", "initiative-cli", "--input", "-"},
			contract: strings.TrimSuffix(integrationCLIContract, "}") + `,"worktreePath":"/forged"}`,
		},
		"strategy selection": {
			args:     []string{"initiative", "integrate", "initiative-cli", "--input", "-"},
			contract: strings.TrimSuffix(integrationCLIContract, "}") + `,"strategy":"merge"}`,
		},
		"contract names initiative": {
			args:     []string{"initiative", "integrate", "initiative-cli", "--input", "-"},
			contract: strings.TrimSuffix(integrationCLIContract, "}") + `,"initiativeHandle":"initiative-other"}`,
		},
		"non JSON output": {
			args:     []string{"initiative", "integrate", "initiative-cli", "--input", "-", "--format", "table"},
			contract: integrationCLIContract,
		},
		"duplicate input": {
			args:     []string{"initiative", "integrate", "initiative-cli", "--input", "-", "--input", "-"},
			contract: integrationCLIContract,
		},
	} {
		t.Run(name, func(t *testing.T) {
			factoryCalled := false
			config := testConfig(fixtureClient())
			config.Stdin = strings.NewReader(test.contract)
			config.NewClient = func(string) (ReadClient, error) {
				factoryCalled = true
				return fixtureClient(), nil
			}
			var output bytes.Buffer
			if code := Run(context.Background(), test.args, &output, &output, config); code != ExitUsage {
				t.Fatalf("Run(%v) = %d, output=%q", test.args, code, output.String())
			}
			if factoryCalled {
				t.Fatal("invalid integration command connected to service")
			}
		})
	}
}

func TestCLIIntegrationExecutionRequiresDecodedContractAndUsageDocumentsCommand(t *testing.T) {
	if _, err := execute(context.Background(), fixtureClient(), "operation-integration-cli", parsedCommand{
		kind: commandApplyIntegration, reference: "initiative-cli",
	}); err == nil {
		t.Fatal("execute(integration without input) error = nil")
	}
	if !strings.Contains(usage, "initiative integrate INITIATIVE --input") {
		t.Fatal("operator usage omits initiative integration command")
	}
}
