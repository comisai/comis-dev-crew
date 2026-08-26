package application

import (
	"context"

	"github.com/comisai/comis-dev-crew/internal/domain"
)

// Fleet returns the canonical current fleet snapshot.
func (queries *Queries) Fleet(ctx context.Context) (FleetSnapshot, error) {
	tasks, stateVersion, err := queries.taskSnapshot(ctx)
	if err != nil {
		return FleetSnapshot{}, err
	}
	now := queries.now()
	capacity, err := projectFleetCapacity(tasks, queries.schedulingLimits)
	if err != nil {
		return FleetSnapshot{}, newSafeFailure(
			domain.ErrorInternal, false, "fleet capacity projection is inconsistent",
			"verify --max-concurrent-tasks, --max-concurrent-tasks-per-repository, and every --*-concurrency setting",
			err,
		)
	}
	projected, err := queries.projectTasks(ctx, tasks, now)
	if err != nil {
		return FleetSnapshot{}, err
	}
	completeness, serviceHealth, comisHealth, _ := queries.hostHealth()
	return FleetSnapshot{
		SchemaVersion: 1,
		CapturedAtMs:  now.UnixMilli(),
		StateVersion:  stateVersion,
		Completeness:  completeness,
		ServiceHealth: serviceHealth,
		ComisHealth:   comisHealth,
		Capacity:      capacity,
		Tasks:         projected,
	}, nil
}
