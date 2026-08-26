package application

import (
	"errors"
	"fmt"
	"sort"

	"github.com/comisai/comis-dev-crew/internal/domain"
)

func projectFleetCapacity(
	tasks []TaskObservation,
	limits *InitiativeSchedulingLimits,
) (FleetCapacitySnapshot, error) {
	if limits == nil {
		return FleetCapacitySnapshot{}, nil
	}
	repositories := make(map[string]int)
	profiles := make(map[string]int, len(limits.WorkerProfileLimits))
	for profileID := range limits.WorkerProfileLimits {
		profiles[profileID] = 0
	}
	hostUsed := 0
	for _, observation := range tasks {
		task := observation.Task
		if _, configured := limits.WorkerProfileLimits[task.WorkerProfileID]; !configured {
			return FleetCapacitySnapshot{}, fmt.Errorf(
				"project fleet capacity: task %q names an unconfigured worker profile", task.Handle,
			)
		}
		if _, observed := repositories[task.RepositoryID]; !observed {
			repositories[task.RepositoryID] = 0
		}
		if !taskConsumesWorker(task.State) {
			continue
		}
		hostUsed++
		repositories[task.RepositoryID]++
		profiles[task.WorkerProfileID]++
	}

	dimensions := []FleetCapacityDimension{
		newFleetCapacityDimension(CapacityHost, "", hostUsed, limits.MaxConcurrentTasks),
	}
	for _, repositoryID := range sortedCapacityIDs(repositories) {
		dimensions = append(dimensions, newFleetCapacityDimension(
			CapacityRepository, repositoryID, repositories[repositoryID],
			limits.MaxConcurrentTasksPerRepository,
		))
	}
	for _, profileID := range sortedCapacityIDs(profiles) {
		dimensions = append(dimensions, newFleetCapacityDimension(
			CapacityWorkerProfile, profileID, profiles[profileID], limits.WorkerProfileLimits[profileID],
		))
	}
	for _, dimension := range dimensions {
		if err := validateFleetCapacityDimension(dimension); err != nil {
			return FleetCapacitySnapshot{}, err
		}
	}
	return FleetCapacitySnapshot{Known: true, Dimensions: dimensions}, nil
}

func newFleetCapacityDimension(
	kind FleetCapacityKind,
	id string,
	used int,
	limit int,
) FleetCapacityDimension {
	available := limit - used
	if available < 0 {
		available = 0
	}
	return FleetCapacityDimension{
		Kind: kind, ID: id, Used: used, Limit: limit,
		Available: available, Saturated: used >= limit,
	}
}

func sortedCapacityIDs(values map[string]int) []string {
	ids := make([]string, 0, len(values))
	for id := range values {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

func validateFleetCapacityDimension(dimension FleetCapacityDimension) error {
	switch dimension.Kind {
	case CapacityHost:
		if dimension.ID != "" {
			return errors.New("fleet host capacity must not carry an ID")
		}
	case CapacityRepository, CapacityWorkerProfile:
		if err := domain.ValidateAuthorityReference("capacityId", dimension.ID); err != nil {
			return errors.New("fleet scoped capacity ID is invalid")
		}
	default:
		return errors.New("fleet capacity kind is invalid")
	}
	if dimension.Used < 0 || dimension.Limit < 1 || dimension.Available < 0 ||
		dimension.Available != max(dimension.Limit-dimension.Used, 0) ||
		dimension.Saturated != (dimension.Used >= dimension.Limit) {
		return errors.New("fleet capacity values are inconsistent")
	}
	return nil
}
