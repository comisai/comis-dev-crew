package service

import (
	"errors"

	"github.com/comisai/comis-dev-crew/internal/application"
)

func schedulingLimitsForConfig(config Config) (*application.InitiativeSchedulingLimits, error) {
	if config.MaxConcurrentTasks == 0 && config.MaxConcurrentTasksPerRepository == 0 {
		return nil, nil
	}
	if config.WorkerProfileCatalog == nil {
		return nil, errors.New("worker profile catalog is unavailable")
	}
	profiles := config.WorkerProfileCatalog()
	profileLimits := make(map[string]int, len(profiles))
	for _, profile := range profiles {
		if profile.ProfileID == "" || profile.ConcurrencyLimit < 1 {
			return nil, errors.New("worker profile concurrency is invalid")
		}
		if _, duplicate := profileLimits[profile.ProfileID]; duplicate {
			return nil, errors.New("worker profile concurrency is duplicated")
		}
		profileLimits[profile.ProfileID] = profile.ConcurrencyLimit
	}
	limits := &application.InitiativeSchedulingLimits{
		MaxConcurrentTasks:              config.MaxConcurrentTasks,
		MaxConcurrentTasksPerRepository: config.MaxConcurrentTasksPerRepository,
		WorkerProfileLimits:             profileLimits,
	}
	return limits, nil
}
