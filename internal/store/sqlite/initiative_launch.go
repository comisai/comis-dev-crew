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
	if usage.Host >= limits.MaxConcurrentTasks {
		return fmt.Errorf("authorize initiative task start: %s: %w", application.ScheduleResourceQueued, application.ErrPrecondition)
	}
	launchable, reason, err := initiativeSchedulingFrontier(ctx, transaction, task, containing, *limits, usage)
	if err != nil {
		return fmt.Errorf("authorize initiative task start fleet: %w", err)
	}
	if launchable {
		return nil
	}
	if reason != "" {
		return fmt.Errorf("authorize initiative task start: %s: %w", reason, application.ErrPrecondition)
	}
	return fmt.Errorf("authorize initiative task start: initiative is not launchable: %w", application.ErrPrecondition)
}

const maximumInitiativeSchedulingFrontier = 1024
const initiativeSchedulingPageSize = 64

type initiativeLaunchCandidate struct {
	task  domain.Task
	round int
}

func initiativeSchedulingFrontier(
	ctx context.Context,
	source queryer,
	target domain.Task,
	containing domain.DevelopmentInitiative,
	limits application.InitiativeSchedulingLimits,
	usage application.InitiativeSchedulingUsage,
) (bool, application.InitiativeScheduleReason, error) {
	targetReason := application.InitiativeScheduleReason("")
	selected := 0
	available := limits.MaxConcurrentTasks - usage.Host
	for round := 0; round < domain.MaximumInitiativeMembers; round++ {
		cursorAt, cursorHandle := "", ""
		eligible := 0
		for {
			page, err := initiativeSchedulingPage(ctx, source, cursorAt, cursorHandle, initiativeSchedulingPageSize)
			if err != nil {
				return false, "", err
			}
			if len(page) == 0 {
				break
			}
			for _, item := range page {
				initiative, err := getInitiative(ctx, source, item.handle)
				if err != nil {
					return false, "", err
				}
				candidates, reason, err := initiativeSchedulingCandidates(
					ctx, source, initiative, target.Handle, limits, usage,
				)
				if err != nil {
					return false, "", err
				}
				if initiative.Handle == containing.Handle {
					targetReason = reason
				}
				for _, candidate := range candidates {
					if candidate.round != round {
						continue
					}
					if usage.Repositories[candidate.task.RepositoryID] >= limits.MaxConcurrentTasksPerRepository ||
						usage.WorkerProfiles[candidate.task.WorkerProfileID] >= limits.WorkerProfileLimits[candidate.task.WorkerProfileID] {
						if candidate.task.Handle == target.Handle {
							return false, application.ScheduleResourceQueued, nil
						}
						continue
					}
					eligible++
					if eligible > maximumInitiativeSchedulingFrontier {
						return false, application.ScheduleResourceQueued, nil
					}
					usage.Host++
					usage.Repositories[candidate.task.RepositoryID]++
					usage.WorkerProfiles[candidate.task.WorkerProfileID]++
					selected++
					if candidate.task.Handle == target.Handle {
						return true, application.ScheduleResourceQueued, nil
					}
					if selected == available {
						return false, application.ScheduleResourceQueued, nil
					}
				}
				cursorAt, cursorHandle = item.createdAt, item.handle
			}
			if len(page) < initiativeSchedulingPageSize {
				break
			}
		}
	}
	return false, targetReason, nil
}

type initiativeSchedulingPageItem struct {
	handle    string
	createdAt string
}

func initiativeSchedulingPage(
	ctx context.Context,
	source queryer,
	afterCreatedAt string,
	afterHandle string,
	limit int,
) ([]initiativeSchedulingPageItem, error) {
	rows, err := source.QueryContext(ctx, `SELECT handle, created_at FROM initiatives
		WHERE state = ? AND (created_at, handle) > (?, ?)
		ORDER BY created_at, handle LIMIT ?`, domain.InitiativeActive,
		afterCreatedAt, afterHandle, limit)
	if err != nil {
		return nil, err
	}
	items := make([]initiativeSchedulingPageItem, 0, limit)
	for rows.Next() {
		var item initiativeSchedulingPageItem
		if err := rows.Scan(&item.handle, &item.createdAt); err != nil {
			_ = rows.Close()
			return nil, err
		}
		items = append(items, item)
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return nil, err
	}
	return items, nil
}

func initiativeSchedulingCandidates(
	ctx context.Context,
	source queryer,
	initiative domain.DevelopmentInitiative,
	targetHandle string,
	limits application.InitiativeSchedulingLimits,
	usage application.InitiativeSchedulingUsage,
) ([]initiativeLaunchCandidate, application.InitiativeScheduleReason, error) {
	tasks := make([]domain.Task, 0, domain.MaximumInitiativeMembers)
	byHandle := make(map[string]domain.Task, domain.MaximumInitiativeMembers)
	round := 0
	for _, taskHandle := range initiativeTaskHandles(initiative) {
		task, err := getTask(ctx, source, taskHandle)
		if err != nil {
			return nil, "", err
		}
		tasks = append(tasks, task)
		byHandle[task.Handle] = task
		if task.State != domain.TaskPrepared && task.State != domain.TaskReady {
			round++
		}
	}
	artifacts, err := listInitiativeContractArtifactMetadata(ctx, source, initiative.Handle)
	if err != nil {
		return nil, "", err
	}
	schedules, err := application.ScheduleInitiativesWithUsage(
		[]domain.DevelopmentInitiative{initiative}, tasks, artifacts, limits, usage)
	if err != nil || len(schedules) != 1 {
		return nil, "", errors.Join(err, errors.New("initiative scheduling decision is unavailable"))
	}
	candidates := make([]initiativeLaunchCandidate, 0, len(schedules[0].Tasks))
	targetReason := application.InitiativeScheduleReason("")
	for _, decision := range schedules[0].Tasks {
		if decision.TaskHandle == targetHandle {
			targetReason = decision.Reason
		}
		if decision.State != domain.TaskReady || !decision.Launchable && decision.Reason != application.ScheduleResourceQueued {
			continue
		}
		candidate, found := byHandle[decision.TaskHandle]
		if !found {
			return nil, "", errors.New("initiative scheduling task is unavailable")
		}
		candidates = append(candidates, initiativeLaunchCandidate{
			task: candidate, round: round + len(candidates),
		})
	}
	return candidates, targetReason, nil
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
