package application

import (
	"context"
	"reflect"
	"strings"
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

func TestFleetCapacityDimensionRejectsUnknownKindsAndInconsistentCounts(t *testing.T) {
	for _, dimension := range []FleetCapacityDimension{
		{Kind: FleetCapacityKind("invented"), Used: 1, Limit: 1, Saturated: true},
		{Kind: CapacityHost, ID: "unexpected", Used: 1, Limit: 1, Saturated: true},
		{Kind: CapacityRepository, ID: "../outside", Used: 1, Limit: 1, Saturated: true},
		{Kind: CapacityWorkerProfile, ID: "codex-reviewed", Used: 2, Limit: 1, Available: 1, Saturated: true},
	} {
		if err := validateFleetCapacityDimension(dimension); err == nil {
			t.Fatalf("validateFleetCapacityDimension(%#v) error = nil", dimension)
		}
	}
}

func TestFleetClampsOvercommittedCapacityWithoutHidingSaturation(t *testing.T) {
	tasks := make([]domain.Task, 0, 5)
	for _, handle := range []string{
		"task-overcommit-a", "task-overcommit-b", "task-overcommit-c", "task-overcommit-d", "task-overcommit-e",
	} {
		tasks = append(tasks, capacityQueryTask(t, handle, domain.TaskWorking, "codex-reviewed"))
	}
	queries, err := NewQueries(QueryConfig{
		Repository: &queryRepository{tasks: tasks, stateVersion: 12},
		SchedulingLimits: &InitiativeSchedulingLimits{
			MaxConcurrentTasks: 4, MaxConcurrentTasksPerRepository: 4,
			WorkerProfileLimits: map[string]int{"codex-reviewed": 4},
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
	for _, dimension := range fleet.Capacity.Dimensions {
		if dimension.Used != 5 || dimension.Available != 0 || !dimension.Saturated {
			t.Fatalf("overcommitted dimension = %#v, want used 5, available 0, saturated", dimension)
		}
	}
}

func TestFleetFailsSafelyWhenTaskProfileContradictsConfiguredLimits(t *testing.T) {
	queries, err := NewQueries(QueryConfig{
		Repository: &queryRepository{tasks: []domain.Task{
			capacityQueryTask(t, "task-unconfigured-profile", domain.TaskReady, "removed-profile"),
		}},
		SchedulingLimits: &InitiativeSchedulingLimits{
			MaxConcurrentTasks: 1, MaxConcurrentTasksPerRepository: 1,
			WorkerProfileLimits: map[string]int{"codex-reviewed": 1},
		},
		Clock: time.Now,
	})
	if err != nil {
		t.Fatalf("NewQueries() error = %v", err)
	}
	_, err = queries.Fleet(context.Background())
	if failureCode(err) != domain.ErrorInternal {
		t.Fatalf("Fleet() error = %v, want internal failure", err)
	}
	if strings.Contains(err.Error(), "removed-profile") {
		t.Fatalf("Fleet() leaked the private cause: %v", err)
	}
}

func TestNewQueriesRejectsInvalidCapacityPolicy(t *testing.T) {
	if _, err := NewQueries(QueryConfig{
		Repository: &queryRepository{}, SchedulingLimits: &InitiativeSchedulingLimits{}, Clock: time.Now,
	}); err == nil {
		t.Fatal("NewQueries(invalid scheduling limits) error = nil")
	}
}
