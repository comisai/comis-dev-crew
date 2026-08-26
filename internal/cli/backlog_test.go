package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/comisai/comis-dev-crew/internal/domain"
	"github.com/comisai/comis-dev-crew/internal/localapi"
)

const addBacklogContract = `{
  "repositoryId": "repo-primary",
  "shape": "ship",
  "requestedOutcome": "Implement the bounded request.",
  "dependsOn": [],
  "priority": "normal",
  "readiness": "ready",
  "sourceConversationRef": "conversation-operator-0001"
}`

const promoteBacklogContract = `{
  "baseRevision": "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
  "acceptanceCriteria": ["The implementation is verified."],
  "constraints": [],
  "validationProfile": "go-default",
  "deliveryMode": "pull_request",
  "workerProfileId": "codex-reviewed"
}`

func TestCLIBacklogMutationsUseStrictContractsAndJSONResults(t *testing.T) {
	client := fixtureClient()
	client.backlogAdded = localapi.AddBacklogResult{
		SchemaVersion: 1, OperationID: "operation-cli-backlog-add",
		Item:         domain.BacklogItem{SchemaVersion: 1, Handle: "backlog-cli-added", Readiness: domain.BacklogReady},
		StateVersion: 21, SideEffect: localapi.SideEffectMutate,
	}
	config := testConfig(client)
	config.Stdin = strings.NewReader(addBacklogContract)
	var stdout, stderr bytes.Buffer
	code := Run(context.Background(), []string{
		"backlog", "add", "--input", "-", "--operation", "operation-cli-backlog-add", "--format", "json",
	}, &stdout, &stderr, config)
	if code != ExitSuccess {
		t.Fatalf("Run(backlog add) = %d, stderr=%q", code, stderr.String())
	}
	var added localapi.AddBacklogResult
	if err := json.Unmarshal(stdout.Bytes(), &added); err != nil || added.Item.Handle != "backlog-cli-added" {
		t.Fatalf("backlog add JSON = %#v, %v; raw=%q", added, err, stdout.String())
	}
	if client.operationID != "operation-cli-backlog-add" || client.backlogAddInput.RepositoryID != "repo-primary" ||
		client.backlogAddInput.SourceConversationRef != "conversation-operator-0001" {
		t.Fatalf("backlog addition client input = %#v / %q", client.backlogAddInput, client.operationID)
	}

	client.calls = nil
	client.backlogPromoted = localapi.PromoteBacklogResult{
		SchemaVersion: 1, OperationID: "operation-cli-backlog-promote",
		BacklogHandle: "backlog-cli-added", Readiness: domain.BacklogPromoted,
		TaskHandle: "task-cli-backlog", State: domain.TaskPrepared,
		TaskStateVersion: 22, StateVersion: 23, SideEffect: localapi.SideEffectMutate,
	}
	config.Stdin = strings.NewReader(promoteBacklogContract)
	stdout.Reset()
	stderr.Reset()
	code = Run(context.Background(), []string{
		"backlog", "promote", "backlog-cli-added", "--input", "-",
		"--operation", "operation-cli-backlog-promote", "--format", "json",
	}, &stdout, &stderr, config)
	if code != ExitSuccess {
		t.Fatalf("Run(backlog promote) = %d, stderr=%q", code, stderr.String())
	}
	var promoted localapi.PromoteBacklogResult
	if err := json.Unmarshal(stdout.Bytes(), &promoted); err != nil ||
		promoted.TaskHandle != "task-cli-backlog" || promoted.StateVersion != 23 {
		t.Fatalf("backlog promote JSON = %#v, %v; raw=%q", promoted, err, stdout.String())
	}
	if client.operationID != "operation-cli-backlog-promote" ||
		client.backlogPromoteInput.BacklogHandle != "backlog-cli-added" ||
		client.backlogPromoteInput.BaseRevision != strings.Repeat("a", 40) {
		t.Fatalf("backlog promotion client input = %#v / %q", client.backlogPromoteInput, client.operationID)
	}
}

func TestCLIBacklogMutationsRejectAmbiguousOrBroadenedContractsBeforeConnecting(t *testing.T) {
	for name, test := range map[string]struct {
		args     []string
		contract string
	}{
		"missing add input": {args: []string{"backlog", "add"}},
		"missing promotion handle": {
			args: []string{"backlog", "promote", "--input", "-"}, contract: promoteBacklogContract,
		},
		"invalid promotion handle": {
			args: []string{"backlog", "promote", "../escape", "--input", "-"}, contract: promoteBacklogContract,
		},
		"addition host authority": {
			args:     []string{"backlog", "add", "--input", "-"},
			contract: strings.TrimSuffix(addBacklogContract, "}") + `,"workspaceRoot":"/forged"}`,
		},
		"promotion names itself": {
			args:     []string{"backlog", "promote", "backlog-cli-added", "--input", "-"},
			contract: strings.TrimSuffix(promoteBacklogContract, "}") + `,"backlogHandle":"backlog-other"}`,
		},
		"non JSON add output": {
			args: []string{"backlog", "add", "--input", "-", "--format", "table"}, contract: addBacklogContract,
		},
		"unknown subcommand": {args: []string{"backlog", "delete", "backlog-cli-added"}},
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
				t.Fatal("invalid backlog command connected to the service")
			}
		})
	}
}

func TestCLIBacklogMutationsAppearInOperatorUsage(t *testing.T) {
	for _, command := range []string{"backlog add --input", "backlog promote BACKLOG --input"} {
		if !strings.Contains(usage, command) {
			t.Fatalf("CLI usage is missing %q", command)
		}
	}
	for _, kind := range []commandKind{commandAddBacklog, commandPromoteBacklog} {
		if _, err := execute(context.Background(), fixtureClient(), "operation-backlog-missing-input", parsedCommand{
			kind: kind,
		}); err == nil {
			t.Fatalf("execute(backlog kind %d without input) error = nil", kind)
		}
	}
}
