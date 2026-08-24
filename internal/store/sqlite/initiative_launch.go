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
	containing, found, err := initiativeForTask(ctx, transaction, task.Handle)
	if err != nil {
		return fmt.Errorf("authorize initiative task start membership: %w", err)
	}
	if !found {
		return nil
	}
	if limits == nil {
		return fmt.Errorf("authorize initiative task start: reviewed scheduling limits are unavailable: %w", application.ErrPrecondition)
	}
	if containing.State != domain.InitiativeActive {
		return fmt.Errorf("authorize initiative task start: initiative is not active: %w", application.ErrPrecondition)
	}
	usage, err := initiativeSchedulingUsage(ctx, transaction)
	if err != nil {
		return fmt.Errorf("authorize initiative task start capacity: %w", err)
	}
	if _, err := application.ScheduleInitiativesWithUsage(nil, nil, nil, *limits, usage); err != nil {
		return fmt.Errorf("authorize initiative task start schedule: %w", err)
	}
	available := limits.MaxConcurrentTasks - usage.Host
	if available < 1 {
		return fmt.Errorf("authorize initiative task start: %s: %w", application.ScheduleResourceQueued, application.ErrPrecondition)
	}
	initiatives, tasks, artifacts, err := initiativeSchedulingFleet(ctx, transaction, available)
	if err != nil {
		return fmt.Errorf("authorize initiative task start fleet: %w", err)
	}
	frontierContainsTarget := false
	for _, initiative := range initiatives {
		frontierContainsTarget = frontierContainsTarget || initiative.Handle == containing.Handle
	}
	if !frontierContainsTarget {
		return fmt.Errorf("authorize initiative task start: %s: %w", application.ScheduleResourceQueued, application.ErrPrecondition)
	}
	schedules, err := application.ScheduleInitiativesWithUsage(initiatives, tasks, artifacts, *limits, usage)
	if err != nil {
		return fmt.Errorf("authorize initiative task start schedule: %w", err)
	}
	for _, schedule := range schedules {
		if schedule.InitiativeHandle != containing.Handle {
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

func initiativeSchedulingFleet(
	ctx context.Context,
	source queryer,
	limit int,
) ([]domain.DevelopmentInitiative, []domain.Task, []domain.ComponentContractArtifact, error) {
	if limit < 1 || limit > 1024 {
		return nil, nil, nil, errors.New("initiative scheduling frontier is invalid")
	}
	rows, err := source.QueryContext(ctx, `SELECT i.handle FROM initiatives AS i
		WHERE i.state = ? AND EXISTS (
			SELECT 1 FROM initiative_members AS member
			JOIN tasks AS task ON task.handle = member.task_handle
			WHERE member.initiative_handle = i.handle AND task.state = ?
		)
		ORDER BY i.created_at, i.handle LIMIT ?`, domain.InitiativeActive, domain.TaskReady, limit)
	if err != nil {
		return nil, nil, nil, err
	}
	handles := make([]string, 0, limit)
	for rows.Next() {
		var handle string
		if err := rows.Scan(&handle); err != nil {
			_ = rows.Close()
			return nil, nil, nil, err
		}
		handles = append(handles, handle)
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return nil, nil, nil, err
	}
	initiatives := make([]domain.DevelopmentInitiative, 0, len(handles))
	for _, handle := range handles {
		initiative, err := getInitiative(ctx, source, handle)
		if err != nil {
			return nil, nil, nil, err
		}
		initiatives = append(initiatives, initiative)
	}
	tasks := make([]domain.Task, 0)
	artifacts := make([]domain.ComponentContractArtifact, 0)
	for _, initiative := range initiatives {
		for _, taskHandle := range initiativeTaskHandles(initiative) {
			task, err := getTask(ctx, source, taskHandle)
			if err != nil {
				return nil, nil, nil, err
			}
			tasks = append(tasks, task)
		}
		current, err := listInitiativeContractArtifactMetadata(ctx, source, initiative.Handle)
		if err != nil {
			return nil, nil, nil, err
		}
		artifacts = append(artifacts, current...)
	}
	return initiatives, tasks, artifacts, nil
}

func initiativeSchedulingUsage(
	ctx context.Context,
	source queryer,
) (application.InitiativeSchedulingUsage, error) {
	const query = `SELECT repository_id, worker_profile_id, COUNT(*)
		FROM tasks WHERE state IN (?, ?, ?, ?, ?, ?, ?)
		GROUP BY repository_id, worker_profile_id ORDER BY repository_id, worker_profile_id`
	rows, err := source.QueryContext(ctx, query,
		domain.TaskLaunching, domain.TaskWorking, domain.TaskAwaitingDecision, domain.TaskBlocked,
		domain.TaskPaused, domain.TaskReconciling, domain.TaskUnknown,
	)
	if err != nil {
		return application.InitiativeSchedulingUsage{}, err
	}
	defer rows.Close()
	usage := application.InitiativeSchedulingUsage{
		Repositories: make(map[string]int), WorkerProfiles: make(map[string]int),
	}
	for rows.Next() {
		var repositoryID, profileID string
		var used int
		if err := rows.Scan(&repositoryID, &profileID, &used); err != nil {
			return application.InitiativeSchedulingUsage{}, err
		}
		if domain.ValidateRepositoryID(repositoryID) != nil ||
			domain.ValidateAuthorityReference("workerProfileId", profileID) != nil || used < 1 {
			return application.InitiativeSchedulingUsage{}, errors.New("stored scheduling capacity is invalid")
		}
		usage.Host += used
		usage.Repositories[repositoryID] += used
		usage.WorkerProfiles[profileID] += used
	}
	if err := rows.Err(); err != nil {
		return application.InitiativeSchedulingUsage{}, err
	}
	return usage, nil
}
