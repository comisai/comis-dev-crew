package cli

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

func TestCLITaskMergeRoutesOnlyTheTaskToCanonicalService(t *testing.T) {
	client := fixtureClient()
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
}
