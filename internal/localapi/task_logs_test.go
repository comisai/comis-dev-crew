package localapi

import (
	"context"
	"testing"
	"time"

	"github.com/comisai/comis-dev-crew/internal/application"
)

func taskLogQueriesFixture() *apiQueries {
	occurred := time.Unix(1_800_000_000, 0).UTC()
	return &apiQueries{logs: application.TaskLogPage{
		SchemaVersion: 1, CapturedAt: occurred, TaskHandle: "task-0001",
		Source: application.LogSourceWorker, NextCursor: 4,
		Entries: []application.TaskLogEntry{
			{Sequence: 4, OccurredAt: occurred, Source: application.LogSourceWorker,
				Label: "progress", Detail: "reworked the migration order"},
		},
	}}
}

func TestClient_ReadsOneTaskHistory(t *testing.T) {
	client := newDecisionClient(t, CallerOperatorCLI, taskLogQueriesFixture())

	page, err := client.ReadTaskLogs(context.Background(), "read-logs", ReadTaskLogsInput{
		TaskHandle: "task-0001", Source: application.LogSourceWorker,
	})
	if err != nil {
		t.Fatalf("ReadTaskLogs() error = %v", err)
	}
	if page.NextCursor != 4 || len(page.Entries) != 1 {
		t.Fatalf("page = %#v", page)
	}
}

// Raw private logs are operator-only by §20.3; the model surface gets bounded
// summaries, never a worker's own text.
func TestHandler_RefusesTaskLogsFromTheModelFacade(t *testing.T) {
	client := newDecisionClient(t, CallerMCPFacade, taskLogQueriesFixture())
	if _, err := client.ReadTaskLogs(context.Background(), "read-logs", ReadTaskLogsInput{
		TaskHandle: "task-0001",
	}); err == nil {
		t.Error("ReadTaskLogs(MCP facade) error = nil, want a refusal")
	}
}

func TestReadTaskLogsMethod_IsAValidOperatorOnlyRead(t *testing.T) {
	if !MethodReadTaskLogs.valid() || MethodReadTaskLogs.SideEffect() != SideEffectRead {
		t.Fatalf("ReadTaskLogs method = %q", MethodReadTaskLogs)
	}
	if !MethodReadTaskLogs.operatorOnly() {
		t.Error("private task logs are reachable from the model facade")
	}
}
