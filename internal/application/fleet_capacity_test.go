package application

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/comisai/comis-dev-crew/internal/domain"
)

func TestFleetNamesEverySaturatedConcurrencyDimension(t *testing.T) {
	tasks := []domain.Task{
		capacityQueryTask(t, "task-capacity-a", domain.TaskWorking, "codex-reviewed"),
		capacityQueryTask(t, "task-capacity-b", domain.TaskUnknown, "codex-reviewed"),
		capacityQueryTask(t, "task-capacity-c", domain.TaskPaused, "claude-reviewed"),
		capacityQueryTask(t, "task-capacity-d", domain.TaskAwaitingDecision, "claude-reviewed"),
		capacityQueryTask(t, "task-capacity-ready", domain.TaskReady, "claude-reviewed"),
	}
	queries, err := NewQueries(QueryConfig{
		Repository: &queryRepository{tasks: tasks, stateVersion: 11},
		SchedulingLimits: &InitiativeSchedulingLimits{
			MaxConcurrentTasks: 4, MaxConcurrentTasksPerRepository: 4,
			WorkerProfileLimits: map[string]int{"codex-reviewed": 2, "claude-reviewed": 2},
		},
		Clock: time.Now,
	})
	if err != nil {
		t.Fatalf("NewQueries() error = %v", err)
	}

	fleet, err := queries.Fleet(context.Background())
	if err != nil {
		t.Fatalf("Fleet() error = %v", err)
	}
	want := FleetCapacitySnapshot{
		Known: true,
		Dimensions: []FleetCapacityDimension{
			{Kind: CapacityHost, Used: 4, Limit: 4, Available: 0, Saturated: true},
			{Kind: CapacityRepository, ID: "product-api", Used: 4, Limit: 4, Available: 0, Saturated: true},
			{Kind: CapacityWorkerProfile, ID: "claude-reviewed", Used: 2, Limit: 2, Available: 0, Saturated: true},
			{Kind: CapacityWorkerProfile, ID: "codex-reviewed", Used: 2, Limit: 2, Available: 0, Saturated: true},
		},
	}
	if !reflect.DeepEqual(fleet.Capacity, want) {
		t.Fatalf("Fleet().Capacity = %#v, want %#v", fleet.Capacity, want)
	}
}

func capacityQueryTask(t *testing.T, handle string, state domain.TaskState, profileID string) domain.Task {
	t.Helper()
	task := queryTask(handle, state, 11)
	task.WorkerProfileID = profileID
	pinned, err := task.PinBriefRevision()
	if err != nil {
		t.Fatalf("PinBriefRevision() error = %v", err)
	}
	return pinned
}
