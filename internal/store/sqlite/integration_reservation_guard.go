package sqlite

import (
	"context"
	"fmt"

	"github.com/comisai/comis-dev-crew/internal/application"
	"github.com/comisai/comis-dev-crew/internal/domain"
)

func refuseAnyReservedIntegrationTaskMutation(ctx context.Context, source queryer, taskHandle string) error {
	var reserved bool
	if err := source.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM integration_applications
		WHERE status = 'reserved' AND (integration_task_handle = ? OR candidate_task_handle = ?))`,
		taskHandle, taskHandle,
	).Scan(&reserved); err != nil {
		return fmt.Errorf("inspect task integration reservation: %w", err)
	}
	if reserved {
		return fmt.Errorf("task has a reserved integration application: %w", application.ErrPrecondition)
	}
	return nil
}

func refuseReservedIntegrationTaskTransition(
	ctx context.Context,
	source queryer,
	taskHandle string,
	previous domain.TaskState,
	next domain.TaskState,
) error {
	if previous == next {
		return nil
	}
	if integrationOwnerMutationState(previous) && !integrationOwnerMutationState(next) {
		reserved, err := reservedIntegrationTaskRole(ctx, source, true, taskHandle)
		if err != nil {
			return err
		}
		if reserved {
			return fmt.Errorf("integration owner has a reserved application: %w", application.ErrPrecondition)
		}
	}
	if integrationCandidateMutationState(previous) && !integrationCandidateMutationState(next) {
		reserved, err := reservedIntegrationTaskRole(ctx, source, false, taskHandle)
		if err != nil {
			return err
		}
		if reserved {
			return fmt.Errorf("integration candidate has a reserved application: %w", application.ErrPrecondition)
		}
	}
	return nil
}

func reservedIntegrationTaskRole(ctx context.Context, source queryer, owner bool, taskHandle string) (bool, error) {
	query := `SELECT EXISTS(SELECT 1 FROM integration_applications
		WHERE status = 'reserved' AND candidate_task_handle = ?)`
	if owner {
		query = `SELECT EXISTS(SELECT 1 FROM integration_applications
			WHERE status = 'reserved' AND integration_task_handle = ?)`
	}
	var reserved bool
	if err := source.QueryRowContext(ctx, query, taskHandle).Scan(&reserved); err != nil {
		return false, fmt.Errorf("inspect task integration reservation: %w", err)
	}
	return reserved, nil
}

func refuseReservedIntegrationInitiativeTransition(
	ctx context.Context,
	source queryer,
	initiativeHandle string,
	previous domain.InitiativeState,
	next domain.InitiativeState,
) error {
	if !integrationInitiativeMutationState(previous) || integrationInitiativeMutationState(next) {
		return nil
	}
	var reserved bool
	if err := source.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM integration_applications
		WHERE status = 'reserved' AND initiative_handle = ?)`, initiativeHandle).Scan(&reserved); err != nil {
		return fmt.Errorf("inspect initiative integration reservation: %w", err)
	}
	if reserved {
		return fmt.Errorf("initiative has a reserved integration application: %w", application.ErrPrecondition)
	}
	return nil
}

func integrationOwnerMutationState(state domain.TaskState) bool {
	switch state {
	case domain.TaskReady, domain.TaskWorking, domain.TaskAwaitingDecision, domain.TaskBlocked:
		return true
	default:
		return false
	}
}

func integrationCandidateMutationState(state domain.TaskState) bool {
	return state == domain.TaskCandidateComplete || state == domain.TaskDelivered
}

func integrationInitiativeMutationState(state domain.InitiativeState) bool {
	return state == domain.InitiativeActive || state == domain.InitiativeIntegrating
}
