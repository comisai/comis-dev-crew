package service

import (
	"fmt"

	"github.com/comisai/comis-dev-crew/internal/application"
)

type backlogWorkflowStore interface {
	application.BacklogAdditionStore
	application.BacklogPromotionStore
}

func composeBacklogWorkflows(
	config Config,
	store backlogWorkflowStore,
	mutations *application.Mutations,
	clock application.Clock,
) (*application.BacklogAdditions, *application.BacklogPromotions, error) {
	if mutations == nil {
		return nil, nil, nil
	}
	additions, err := application.NewBacklogAdditions(application.BacklogAdditionConfig{
		Store: store,
		BacklogIDs: func(operationID string) (string, error) {
			return stableBacklogIdentity(config.ServiceInstanceID, operationID), nil
		},
		Clock: clock,
	})
	if err != nil {
		return nil, nil, fmt.Errorf("run service backlog addition coordinator: %w", err)
	}
	promotions, err := application.NewBacklogPromotions(application.BacklogPromotionConfig{
		Store: store, Tasks: mutations, Clock: clock,
	})
	if err != nil {
		return nil, nil, fmt.Errorf("run service backlog promotion coordinator: %w", err)
	}
	return additions, promotions, nil
}
