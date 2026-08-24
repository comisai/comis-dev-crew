package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/comisai/comis-dev-crew/internal/application"
	"github.com/comisai/comis-dev-crew/internal/domain"
)

// authorizeInitiativeTaskStart recomputes the fleet schedule inside the same
// transaction that will move ready to launching. No read-side schedule can be
// replayed as authority after a dependency or capacity fact changes.
func authorizeInitiativeTaskStart(
	ctx context.Context,
	transaction *sql.Tx,
	task domain.Task,
	limits *application.InitiativeSchedulingLimits,
) error {
	initiatives, err := listInitiatives(ctx, transaction)
	if err != nil {
		return fmt.Errorf("authorize initiative task start: %w", err)
	}
	initiativeHandle := ""
	for _, initiative := range initiatives {
		if !initiative.ContainsTask(task.Handle) {
			continue
		}
		if initiativeHandle != "" {
			return errors.New("authorize initiative task start: task belongs to multiple initiatives")
		}
		initiativeHandle = initiative.Handle
	}
	if initiativeHandle == "" {
		return nil
	}
	if limits == nil {
		return fmt.Errorf("authorize initiative task start: reviewed scheduling limits are unavailable: %w", application.ErrPrecondition)
	}
	tasks, err := listTasks(ctx, transaction)
	if err != nil {
		return fmt.Errorf("authorize initiative task start fleet: %w", err)
	}
	artifacts, err := listInitiativeContractArtifactMetadata(ctx, transaction, "")
	if err != nil {
		return fmt.Errorf("authorize initiative task start artifacts: %w", err)
	}
	schedules, err := application.ScheduleInitiatives(initiatives, tasks, artifacts, *limits)
	if err != nil {
		return fmt.Errorf("authorize initiative task start schedule: %w", err)
	}
	for _, schedule := range schedules {
		if schedule.InitiativeHandle != initiativeHandle {
			continue
		}
		for _, decision := range schedule.Tasks {
			if decision.TaskHandle != task.Handle {
				continue
			}
			if decision.Launchable {
				return nil
			}
			if decision.Reason != "" {
				return fmt.Errorf("authorize initiative task start: %s: %w", decision.Reason, application.ErrPrecondition)
			}
			return fmt.Errorf("authorize initiative task start: initiative is not launchable: %w", application.ErrPrecondition)
		}
	}
	return errors.New("authorize initiative task start: scheduler omitted the initiative member")
}
