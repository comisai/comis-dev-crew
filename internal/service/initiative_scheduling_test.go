package service

import (
	"testing"

	"github.com/comisai/comis-dev-crew/internal/application"
)

func TestSchedulingLimitsUseEveryReviewedWorkerProfile(t *testing.T) {
	limits, err := schedulingLimitsForConfig(Config{
		MaxConcurrentTasks: 4, MaxConcurrentTasksPerRepository: 3,
		WorkerProfileCatalog: func() []application.WorkerProfileSummary {
			return []application.WorkerProfileSummary{
				{ProfileID: "codex-reviewed", ConcurrencyLimit: 2},
				{ProfileID: "claude-reviewed", ConcurrencyLimit: 1},
			}
		},
	})
	if err != nil {
		t.Fatalf("schedulingLimitsForConfig() error = %v", err)
	}
	if limits.MaxConcurrentTasks != 4 || limits.MaxConcurrentTasksPerRepository != 3 ||
		limits.WorkerProfileLimits["codex-reviewed"] != 2 ||
		limits.WorkerProfileLimits["claude-reviewed"] != 1 {
		t.Fatalf("scheduling limits = %#v", limits)
	}
}

func TestSchedulingLimitsFailClosedOnMissingOrAmbiguousProfiles(t *testing.T) {
	if _, err := schedulingLimitsForConfig(Config{
		MaxConcurrentTasks: 1, MaxConcurrentTasksPerRepository: 1,
	}); err == nil {
		t.Fatal("schedulingLimitsForConfig(missing catalog) error = nil")
	}
	if _, err := schedulingLimitsForConfig(Config{
		MaxConcurrentTasks: 1, MaxConcurrentTasksPerRepository: 1,
		WorkerProfileCatalog: func() []application.WorkerProfileSummary {
			return []application.WorkerProfileSummary{
				{ProfileID: "codex-reviewed", ConcurrencyLimit: 1},
				{ProfileID: "codex-reviewed", ConcurrencyLimit: 1},
			}
		},
	}); err == nil {
		t.Fatal("schedulingLimitsForConfig(duplicate profile) error = nil")
	}
}
