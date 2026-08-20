package service

import (
	"fmt"

	"github.com/comisai/comis-dev-crew/internal/application"
	"github.com/comisai/comis-dev-crew/internal/store/sqlite"
)

// composeInitiativeMutations reuses the exact reviewed preparation dependencies
// selected for standalone tasks. An unconfigured read-only service exposes
// neither mutation surface.
func composeInitiativeMutations(
	config Config,
	store *sqlite.Store,
	clock application.Clock,
	mutationsConfigured bool,
) (*application.InitiativeMutations, error) {
	if !mutationsConfigured {
		return nil, nil
	}
	mutations, err := application.NewInitiativeMutations(application.InitiativeMutationConfig{
		Store: store, Repositories: config.Repositories,
		WorkerProfiles: config.WorkerProfiles, ValidationProfiles: config.ValidationProfiles,
		Workspaces: config.Workspaces, RuntimeAttachments: config.RuntimeAttachments,
		TaskIDs: config.TaskIDs, RegistrationNonces: config.RegistrationNonces,
		PreparationTTL: config.PreparationTTL, Clock: clock,
	})
	if err != nil {
		return nil, fmt.Errorf("run service initiative mutation coordinator: %w", err)
	}
	return mutations, nil
}
