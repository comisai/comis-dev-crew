package application

import (
	"context"
	"testing"
	"time"

	"github.com/comisai/comis-dev-crew/internal/domain"
)

// Scoping the list by state returns only that state's work, and an unknown state
// is refused rather than matching nothing — which an operator would read as "no
// such work" instead of "no such state".
func TestQueries_ListTasksScopesByState(t *testing.T) {
	now := time.Date(2026, time.August, 19, 12, 0, 0, 0, time.UTC)
	working := queryTask("task-0001", domain.TaskWorking, 4)
	blocked := queryTask("task-0002", domain.TaskBlocked, 4)
	repository := &queryRepository{
		tasks: []domain.Task{working, blocked}, operation: queryOperation("op-0001", 5), stateVersion: 5,
	}
	queries, err := NewQueries(QueryConfig{Repository: repository, Clock: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}

	all, err := queries.ListTasks(context.Background(), "")
	if err != nil || len(all.Tasks) != 2 {
		t.Fatalf("unscoped list = %d tasks, %v", len(all.Tasks), err)
	}

	scoped, err := queries.ListTasks(context.Background(), domain.TaskWorking)
	if err != nil {
		t.Fatalf("ListTasks(working) error = %v", err)
	}
	if len(scoped.Tasks) != 1 || scoped.Tasks[0].TaskHandle != working.Handle {
		t.Fatalf("scoped list = %#v", scoped.Tasks)
	}

	empty, err := queries.ListTasks(context.Background(), domain.TaskCleaned)
	if err != nil {
		t.Fatalf("ListTasks(cleaned) error = %v", err)
	}
	if len(empty.Tasks) != 0 {
		t.Fatalf("a state with no work returned %d tasks", len(empty.Tasks))
	}

	if _, err := queries.ListTasks(context.Background(), domain.TaskState("nearly-done")); err == nil {
		t.Fatal("ListTasks(unknown state) error = nil")
	}
}
