package service

import (
	"errors"
	"fmt"

	"github.com/comisai/comis-dev-crew/internal/application"
)

func composeTaskMerges(
	config Config,
	store application.TaskMergeStore,
	control ComisControl,
	clock application.Clock,
) (*application.MergeCoordinator, error) {
	if config.mergePullRequests == nil {
		if config.mergeOperatorEnabled {
			return nil, errors.New("run service: merge authority has no forge adapter")
		}
		return nil, nil
	}
	approvals, ok := control.(application.MergeApprovalConsumer)
	if !ok {
		return nil, errors.New("run service: merge authority requires authenticated approval consumption")
	}
	coordinator, err := application.NewMergeCoordinator(application.MergeCoordinatorConfig{
		Store: store, Approvals: approvals, Forge: config.mergePullRequests,
		MergeMethod: config.mergeMethod, Clock: clock, OperatorEnabled: config.mergeOperatorEnabled,
	})
	if err != nil {
		return nil, fmt.Errorf("run service merge coordinator: %w", err)
	}
	return coordinator, nil
}
