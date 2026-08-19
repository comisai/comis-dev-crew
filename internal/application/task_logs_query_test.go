package application

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/comisai/comis-dev-crew/internal/domain"
)

type taskLogStub struct {
	entries []TaskLogEntry
	err     error
	sources []TaskLogSource
	handles []string
}

func (stub *taskLogStub) ReadTaskLogs(
	_ context.Context,
	taskHandle string,
	source TaskLogSource,
	_ int64,
	_ int,
) ([]TaskLogEntry, error) {
	stub.handles = append(stub.handles, taskHandle)
	stub.sources = append(stub.sources, source)
	return stub.entries, stub.err
}

func logQueryFixture(t *testing.T, stub *taskLogStub) *Queries {
	t.Helper()
	queries, err := NewQueries(QueryConfig{
		Repository: &queryRepository{tasks: []domain.Task{diffTaskFixture()}, stateVersion: 3},
		TaskLogs:   stub,
		Clock:      func() time.Time { return time.Unix(1_800_000_000, 0).UTC() },
	})
	if err != nil {
		t.Fatalf("NewQueries() error = %v", err)
	}
	return queries
}

// Reading a task's history proves the task exists first, so an unknown handle is
// a not-found rather than an empty page that reads as "this task did nothing".
func TestReadTaskLogs_ProvesTheTaskBeforeReadingItsHistory(t *testing.T) {
	occurred := time.Unix(1_800_000_000, 0).UTC()
	stub := &taskLogStub{entries: []TaskLogEntry{
		{Sequence: 4, OccurredAt: occurred, Source: LogSourceWorker, Label: "progress", Detail: "did a thing"},
	}}
	queries := logQueryFixture(t, stub)

	page, err := queries.ReadTaskLogs(context.Background(), "task-0001", LogSourceWorker, 0, 50)
	if err != nil {
		t.Fatalf("ReadTaskLogs() error = %v", err)
	}
	if page.SchemaVersion != 1 || page.TaskHandle != "task-0001" || page.Source != LogSourceWorker {
		t.Fatalf("page = %#v", page)
	}
	if page.NextCursor != 4 || len(page.Entries) != 1 {
		t.Fatalf("page cursor/entries = %#v", page)
	}
	if len(stub.handles) != 1 || stub.handles[0] != "task-0001" {
		t.Errorf("store handles = %v", stub.handles)
	}

	if _, err := queries.ReadTaskLogs(context.Background(), "task-absent", LogSourceWorker, 0, 50); err == nil {
		t.Error("ReadTaskLogs(absent task) error = nil")
	}
}

// An unspecified source reads the worker's own account, which is what an
// operator asking "what has this task been doing" means.
func TestReadTaskLogs_DefaultsToTheWorkerAccount(t *testing.T) {
	stub := &taskLogStub{}
	queries := logQueryFixture(t, stub)

	if _, err := queries.ReadTaskLogs(context.Background(), "task-0001", "", 0, 50); err != nil {
		t.Fatalf("ReadTaskLogs(no source) error = %v", err)
	}
	if len(stub.sources) != 1 || stub.sources[0] != LogSourceWorker {
		t.Fatalf("store sources = %v, want the worker account", stub.sources)
	}
	if _, err := queries.ReadTaskLogs(context.Background(), "task-0001", TaskLogSource("terminal"), 0, 50); err == nil {
		t.Error("ReadTaskLogs(unknown source) error = nil")
	}
}

// A quiet page still hands back the cursor, and an empty page is an array.
func TestReadTaskLogs_HandsBackTheCursorAndAnArray(t *testing.T) {
	queries := logQueryFixture(t, &taskLogStub{})
	page, err := queries.ReadTaskLogs(context.Background(), "task-0001", LogSourceService, 7, 50)
	if err != nil {
		t.Fatalf("ReadTaskLogs(quiet) error = %v", err)
	}
	if page.NextCursor != 7 {
		t.Errorf("quiet cursor = %d, want it unchanged", page.NextCursor)
	}
	if page.Entries == nil {
		t.Error("a quiet page carries a null entry list")
	}
}

func TestReadTaskLogs_RefusesInvalidReferencesAndUnavailableReads(t *testing.T) {
	queries := logQueryFixture(t, &taskLogStub{})
	if _, err := queries.ReadTaskLogs(context.Background(), "not a handle", LogSourceWorker, 0, 10); err == nil {
		t.Error("ReadTaskLogs(invalid handle) error = nil")
	}
	if _, err := queries.ReadTaskLogs(context.Background(), "task-0001", LogSourceWorker, -1, 10); err == nil {
		t.Error("ReadTaskLogs(negative cursor) error = nil")
	}

	failing := logQueryFixture(t, &taskLogStub{err: errors.New("durable read failed")})
	if _, err := failing.ReadTaskLogs(context.Background(), "task-0001", LogSourceWorker, 0, 10); err == nil {
		t.Error("ReadTaskLogs(store failure) error = nil")
	}

	unconfigured, err := NewQueries(QueryConfig{
		Repository: &queryRepository{tasks: []domain.Task{diffTaskFixture()}, stateVersion: 3},
		Clock:      func() time.Time { return time.Unix(1_800_000_000, 0).UTC() },
	})
	if err != nil {
		t.Fatalf("NewQueries() error = %v", err)
	}
	if _, err := unconfigured.ReadTaskLogs(context.Background(), "task-0001", LogSourceWorker, 0, 10); err == nil {
		t.Error("ReadTaskLogs(no log store) error = nil")
	}
}
