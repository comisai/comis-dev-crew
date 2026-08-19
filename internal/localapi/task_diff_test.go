package localapi

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/comisai/comis-dev-crew/internal/application"
)

func taskDiffQueriesFixture() *apiQueries {
	return &apiQueries{diff: application.TaskDiffView{
		SchemaVersion: 1, StateVersion: 3, CapturedAt: time.Unix(1_800_000_000, 0).UTC(),
		TaskHandle: "task-0001", RepositoryID: "product-api",
		BaseRevision: strings.Repeat("a", 40), HeadRevision: strings.Repeat("b", 40),
		Committed:       []application.TaskFileChange{{Path: "internal/thing.go", Added: 12, Deleted: 3}},
		CommittedTotals: application.TaskDiffTotals{Files: 1, Added: 12, Deleted: 3},
	}}
}

// The operator console reads what a task changed over the owner-only endpoint.
func TestClient_ReadsWhatOneTaskChanged(t *testing.T) {
	client := newDecisionClient(t, CallerOperatorCLI, taskDiffQueriesFixture())

	view, err := client.DiffTask(context.Background(), "read-diff", "task-0001")
	if err != nil {
		t.Fatalf("DiffTask() error = %v", err)
	}
	if view.TaskHandle != "task-0001" || len(view.Committed) != 1 ||
		view.Committed[0].Path != "internal/thing.go" {
		t.Fatalf("diff view = %#v", view)
	}
	if view.CommittedTotals.Added != 12 {
		t.Fatalf("committed totals = %#v", view.CommittedTotals)
	}
}

// A diff is private task detail, so the authority boundary lives in the handler
// rather than only in the set of tools the model facade exposes.
func TestHandler_RefusesTaskDiffFromTheModelFacade(t *testing.T) {
	client := newDecisionClient(t, CallerMCPFacade, taskDiffQueriesFixture())
	if _, err := client.DiffTask(context.Background(), "read-diff", "task-0001"); err == nil {
		t.Error("DiffTask(MCP facade) error = nil, want a refusal")
	}
}

func TestDiffTaskMethod_IsAValidOperatorOnlyRead(t *testing.T) {
	if !MethodDiffTask.valid() || MethodDiffTask.SideEffect() != SideEffectRead {
		t.Fatalf("DiffTask method = %q, side effect = %q", MethodDiffTask, MethodDiffTask.SideEffect())
	}
	if methodAllowed(CallerMCPFacade, MethodDiffTask) {
		t.Error("DiffTask is reachable from the model facade")
	}
	if !methodAllowed(CallerOperatorCLI, MethodDiffTask) {
		t.Error("DiffTask is unreachable from the operator console")
	}
}
