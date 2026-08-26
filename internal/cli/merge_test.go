package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/comisai/comis-dev-crew/internal/application"
)

func TestCLITaskMergeRoutesOnlyTheTaskToCanonicalService(t *testing.T) {
	client := fixtureClient()
	client.mergeResult = application.MergeTaskResult{
		OperationID: "operation-merge-cli", TaskHandle: "task-0001",
		State: application.TaskMergeAwaitingApproval, RepositoryID: "product-api",
		PullRequestID: "pull-request-merge", HeadRevision: strings.Repeat("a", 40), StateVersion: 19,
	}
	var output bytes.Buffer
	code := Run(context.Background(), []string{
		"task", "merge", "task-0001", "--operation", "operation-merge-cli", "--format", "json",
	}, &output, &output, testConfig(client))
	if code != ExitSuccess {
		t.Fatalf("Run(task merge) = %d: %s", code, output.String())
	}
	if len(client.calls) != 1 || client.calls[0] != "merge:task-0001" || client.operationID != "operation-merge-cli" {
		t.Fatalf("merge did not route through one canonical command: %v/%q", client.calls, client.operationID)
	}
	if !strings.Contains(usage, "task merge TASK") {
		t.Fatal("task merge is missing from the CLI usage text")
	}
	var result application.MergeTaskResult
	if err := json.Unmarshal(output.Bytes(), &result); err != nil || result != client.mergeResult {
		t.Fatalf("merge JSON = %#v, %v", result, err)
	}
}

func TestCLITaskMergeRefusesCallerSuppliedApprovalAndForgeAuthority(t *testing.T) {
	for name, args := range map[string][]string{
		"no reference":       {"task", "merge"},
		"forged reference":   {"task", "merge", "../../etc"},
		"approval identity":  {"task", "merge", "task-0001", "--approval", "approval-forged"},
		"forge repository":   {"task", "merge", "task-0001", "--repository", "other"},
		"merge method":       {"task", "merge", "task-0001", "--method", "rebase"},
		"non-JSON format":    {"task", "merge", "task-0001", "--format", "table"},
		"repeated operation": {"task", "merge", "task-0001", "--operation", "operation-a", "--operation", "operation-b"},
	} {
		t.Run(name, func(t *testing.T) {
			client := fixtureClient()
			var output bytes.Buffer
			if code := Run(context.Background(), args, &output, &output, testConfig(client)); code == ExitSuccess {
				t.Fatalf("Run(%v) succeeded", args)
			}
			if len(client.calls) != 0 {
				t.Fatalf("refused merge reached service: %v", client.calls)
			}
		})
	}
}
