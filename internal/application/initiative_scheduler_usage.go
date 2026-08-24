package application

import (
	"errors"
	"fmt"

	"github.com/comisai/comis-dev-crew/internal/domain"
)

// InitiativeSchedulingUsage is the validated aggregate worker capacity already
// consumed when a durable scheduler transaction allocates ready initiative work.
type InitiativeSchedulingUsage struct {
	Host           int
	Repositories   map[string]int
	WorkerProfiles map[string]int
}

// ScheduleInitiativesWithUsage schedules a bounded active initiative set while
// applying an exact fleet-wide capacity aggregate supplied by the durable store.
func ScheduleInitiativesWithUsage(
	initiatives []domain.DevelopmentInitiative,
	tasks []domain.Task,
	artifacts []domain.ComponentContractArtifact,
	limits InitiativeSchedulingLimits,
	usage InitiativeSchedulingUsage,
) ([]InitiativeSchedule, error) {
	if err := validateSchedulingLimits(limits); err != nil {
		return nil, err
	}
	tasksByHandle, err := indexSchedulingTaskSet(tasks, limits)
	if err != nil {
		return nil, err
	}
	normalized, err := normalizeSchedulingUsage(usage, limits)
	if err != nil {
		return nil, err
	}
	return scheduleInitiatives(initiatives, tasksByHandle, artifacts, limits, normalized)
}

func indexSchedulingTaskSet(
	tasks []domain.Task,
	limits InitiativeSchedulingLimits,
) (map[string]domain.Task, error) {
	indexed := make(map[string]domain.Task, len(tasks))
	for _, task := range tasks {
		if err := task.Validate(); err != nil {
			return nil, fmt.Errorf("schedule task %q: %w", task.Handle, err)
		}
		if _, exists := indexed[task.Handle]; exists {
			return nil, errors.New("schedule initiatives: task handles must be unique")
		}
		if _, configured := limits.WorkerProfileLimits[task.WorkerProfileID]; !configured {
			return nil, errors.New("schedule initiatives: task worker profile has no concurrency limit")
		}
		indexed[task.Handle] = task
	}
	return indexed, nil
}

func normalizeSchedulingUsage(
	provided InitiativeSchedulingUsage,
	limits InitiativeSchedulingLimits,
) (schedulingUsage, error) {
	if provided.Host < 0 {
		return schedulingUsage{}, errors.New("schedule initiatives: fleet usage is invalid")
	}
	normalized := schedulingUsage{
		host: provided.Host, repositories: make(map[string]int, len(provided.Repositories)),
		profiles: make(map[string]int, len(limits.WorkerProfileLimits)),
	}
	repositoryTotal := 0
	for repositoryID, used := range provided.Repositories {
		if domain.ValidateRepositoryID(repositoryID) != nil || used < 1 {
			return schedulingUsage{}, errors.New("schedule initiatives: repository usage is invalid")
		}
		normalized.repositories[repositoryID] = used
		repositoryTotal += used
	}
	profileTotal := 0
	for profileID := range limits.WorkerProfileLimits {
		normalized.profiles[profileID] = 0
	}
	for profileID, used := range provided.WorkerProfiles {
		if _, configured := limits.WorkerProfileLimits[profileID]; !configured || used < 0 {
			return schedulingUsage{}, errors.New("schedule initiatives: worker profile usage is invalid")
		}
		normalized.profiles[profileID] = used
		profileTotal += used
	}
	if repositoryTotal != provided.Host || profileTotal != provided.Host {
		return schedulingUsage{}, errors.New("schedule initiatives: fleet usage totals differ")
	}
	return normalized, nil
}
