package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/comisai/comis-dev-crew/internal/application"
	"github.com/comisai/comis-dev-crew/internal/domain"
)

const initiativeLaunchFactsMigration = `
CREATE TABLE initiative_launch_facts (
    task_handle TEXT PRIMARY KEY,
    initiative_handle TEXT NOT NULL,
    initiative_created_at TEXT NOT NULL,
    scheduling_round INTEGER NOT NULL,
    repository_id TEXT NOT NULL,
    worker_profile_id TEXT NOT NULL,
    FOREIGN KEY(task_handle) REFERENCES tasks(handle) ON DELETE CASCADE,
    FOREIGN KEY(initiative_handle) REFERENCES initiatives(handle) ON DELETE CASCADE
);
CREATE INDEX initiative_launch_facts_priority_idx
ON initiative_launch_facts(scheduling_round, initiative_created_at, initiative_handle, task_handle);
CREATE INDEX initiative_launch_facts_profile_priority_idx
ON initiative_launch_facts(worker_profile_id, scheduling_round, initiative_created_at, initiative_handle, task_handle);
CREATE INDEX initiative_launch_facts_repository_priority_idx
ON initiative_launch_facts(repository_id, scheduling_round, initiative_created_at, initiative_handle, task_handle);
`

func (store *Store) applyInitiativeLaunchFactsMigration(ctx context.Context) error {
	var applied int
	if err := store.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM schema_migrations WHERE version = 49").Scan(&applied); err != nil {
		return fmt.Errorf("inspect SQLite migration 49: %w", err)
	}
	if applied == 1 {
		return nil
	}
	transaction, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin SQLite migration 49: %w", err)
	}
	defer func() { _ = transaction.Rollback() }()
	if _, err := transaction.ExecContext(ctx, initiativeLaunchFactsMigration); err != nil {
		return fmt.Errorf("apply SQLite migration 49: %w", err)
	}
	if err := backfillInitiativeLaunchFacts(ctx, transaction); err != nil {
		return fmt.Errorf("backfill migration 49 launch facts: %w", err)
	}
	if _, err := transaction.ExecContext(ctx, `INSERT INTO schema_migrations(version, applied_at)
		VALUES (49, strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))`); err != nil {
		return fmt.Errorf("record SQLite migration 49: %w", err)
	}
	if err := transaction.Commit(); err != nil {
		return fmt.Errorf("commit SQLite migration 49: %w", err)
	}
	return nil
}

func backfillInitiativeLaunchFacts(ctx context.Context, transaction *sql.Tx) error {
	afterCreatedAt, afterHandle := "", ""
	for {
		page, err := initiativeSchedulingPage(
			ctx, transaction, afterCreatedAt, afterHandle, initiativeSchedulingPageSize,
		)
		if err != nil {
			return err
		}
		for _, item := range page {
			initiative, err := getInitiative(ctx, transaction, item.handle)
			if err != nil {
				return err
			}
			if err := refreshInitiativeLaunchFacts(ctx, transaction, initiative); err != nil {
				return err
			}
			afterCreatedAt, afterHandle = item.createdAt, item.handle
		}
		if len(page) < initiativeSchedulingPageSize {
			return nil
		}
	}
}

func refreshInitiativeLaunchFactsIfComplete(
	ctx context.Context,
	target queryExecer,
	initiative domain.DevelopmentInitiative,
) error {
	if initiative.State != domain.InitiativeActive {
		_, err := target.ExecContext(ctx, `DELETE FROM initiative_launch_facts WHERE initiative_handle = ?`, initiative.Handle)
		return err
	}
	var available int
	if err := target.QueryRowContext(ctx, `SELECT COUNT(*) FROM initiative_members AS member
		JOIN tasks AS task ON task.handle = member.task_handle
		WHERE member.initiative_handle = ?`, initiative.Handle).Scan(&available); err != nil {
		return err
	}
	if available != len(initiativeTaskHandles(initiative)) {
		_, err := target.ExecContext(ctx, `DELETE FROM initiative_launch_facts WHERE initiative_handle = ?`, initiative.Handle)
		return err
	}
	var artifacts int
	if err := target.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM initiative_contract_artifacts WHERE initiative_handle = ?`, initiative.Handle,
	).Scan(&artifacts); err != nil {
		return err
	}
	if artifacts != len(initiative.ContractArtifacts) {
		_, err := target.ExecContext(ctx, `DELETE FROM initiative_launch_facts WHERE initiative_handle = ?`, initiative.Handle)
		return err
	}
	return refreshInitiativeLaunchFacts(ctx, target, initiative)
}

func refreshInitiativeLaunchFacts(
	ctx context.Context,
	target queryExecer,
	initiative domain.DevelopmentInitiative,
) error {
	if _, err := target.ExecContext(ctx,
		`DELETE FROM initiative_launch_facts WHERE initiative_handle = ?`, initiative.Handle,
	); err != nil {
		return fmt.Errorf("clear initiative launch facts: %w", err)
	}
	if initiative.State != domain.InitiativeActive {
		return nil
	}
	tasks := make([]domain.Task, 0, domain.MaximumInitiativeMembers)
	byHandle := make(map[string]domain.Task, domain.MaximumInitiativeMembers)
	profiles := make(map[string]int)
	for _, handle := range initiativeTaskHandles(initiative) {
		var memberships int
		if err := target.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM initiative_members WHERE task_handle = ?`, handle,
		).Scan(&memberships); err != nil {
			return fmt.Errorf("inspect initiative launch membership: %w", err)
		}
		if memberships != 1 {
			return errors.New("initiative launch task membership is ambiguous")
		}
		task, err := getTask(ctx, target, handle)
		if err != nil {
			return fmt.Errorf("read initiative launch task: %w", err)
		}
		tasks = append(tasks, task)
		byHandle[handle] = task
		profiles[task.WorkerProfileID] = maximumInitiativeSchedulingFrontier
	}
	artifacts, err := listInitiativeContractArtifactMetadata(ctx, target, initiative.Handle)
	if err != nil {
		return fmt.Errorf("read initiative launch artifacts: %w", err)
	}
	limits := application.InitiativeSchedulingLimits{
		MaxConcurrentTasks:              maximumInitiativeSchedulingFrontier,
		MaxConcurrentTasksPerRepository: maximumInitiativeSchedulingFrontier,
		WorkerProfileLimits:             profiles,
	}
	schedules, err := application.ScheduleInitiativesWithUsage(
		[]domain.DevelopmentInitiative{initiative}, tasks, artifacts, limits,
		application.InitiativeSchedulingUsage{
			Repositories: make(map[string]int), WorkerProfiles: make(map[string]int),
		},
	)
	if err != nil || len(schedules) != 1 {
		return errors.Join(err, errors.New("initiative launch schedule is unavailable"))
	}
	round := 0
	for _, task := range tasks {
		if task.State != domain.TaskPrepared && task.State != domain.TaskReady {
			round++
		}
	}
	candidateIndex := 0
	for _, decision := range schedules[0].Tasks {
		if decision.State != domain.TaskReady || !decision.Launchable {
			continue
		}
		task, found := byHandle[decision.TaskHandle]
		if !found {
			return errors.New("initiative launch candidate task is unavailable")
		}
		if _, err := target.ExecContext(ctx, `INSERT INTO initiative_launch_facts (
			task_handle, initiative_handle, initiative_created_at, scheduling_round,
			repository_id, worker_profile_id
		) VALUES (?, ?, ?, ?, ?, ?)`,
			task.Handle, initiative.Handle, formatTime(initiative.CreatedAt), round+candidateIndex,
			task.RepositoryID, task.WorkerProfileID,
		); err != nil {
			return fmt.Errorf("insert initiative launch fact: %w", err)
		}
		candidateIndex++
	}
	return nil
}

func refreshInitiativeLaunchFactsForTask(ctx context.Context, target queryExecer, taskHandle string) error {
	initiative, found, err := initiativeForTask(ctx, target, taskHandle)
	if err != nil {
		return err
	}
	if !found {
		return nil
	}
	return refreshInitiativeLaunchFacts(ctx, target, initiative)
}

type initiativeLaunchFact struct {
	taskHandle       string
	initiativeHandle string
	createdAt        string
	round            int
	repositoryID     string
	workerProfileID  string
}

func scanInitiativeLaunchFact(row rowScanner) (initiativeLaunchFact, error) {
	var fact initiativeLaunchFact
	if err := row.Scan(
		&fact.taskHandle, &fact.initiativeHandle, &fact.createdAt, &fact.round,
		&fact.repositoryID, &fact.workerProfileID,
	); err != nil {
		return initiativeLaunchFact{}, err
	}
	if domain.ValidateTaskHandle(fact.taskHandle) != nil ||
		domain.ValidateTaskHandle(fact.initiativeHandle) != nil ||
		domain.ValidateRepositoryID(fact.repositoryID) != nil ||
		domain.ValidateAuthorityReference("workerProfileId", fact.workerProfileID) != nil ||
		fact.round < 0 || fact.round >= domain.MaximumInitiativeMembers {
		return initiativeLaunchFact{}, errors.New("stored initiative launch fact is invalid")
	}
	if _, err := parseTime(fact.createdAt); err != nil {
		return initiativeLaunchFact{}, errors.New("stored initiative launch fact time is invalid")
	}
	return fact, nil
}

func initiativeLaunchFactForTask(
	ctx context.Context,
	source queryer,
	task domain.Task,
) (initiativeLaunchFact, bool, error) {
	fact, err := scanInitiativeLaunchFact(source.QueryRowContext(ctx, `SELECT
		task_handle, initiative_handle, initiative_created_at, scheduling_round,
		repository_id, worker_profile_id FROM initiative_launch_facts WHERE task_handle = ?`, task.Handle))
	if errors.Is(err, sql.ErrNoRows) {
		return initiativeLaunchFact{}, false, nil
	}
	if err != nil {
		return initiativeLaunchFact{}, false, err
	}
	if fact.repositoryID != task.RepositoryID || fact.workerProfileID != task.WorkerProfileID {
		return initiativeLaunchFact{}, false, errors.New("initiative launch fact task authority differs")
	}
	return fact, true, nil
}

func initiativeLaunchFactPage(
	ctx context.Context,
	source queryer,
	round int,
	afterCreatedAt string,
	afterInitiativeHandle string,
	afterTaskHandle string,
	limits application.InitiativeSchedulingLimits,
	usage application.InitiativeSchedulingUsage,
	limit int,
) ([]initiativeLaunchFact, error) {
	profiles := make([]string, 0, len(limits.WorkerProfileLimits))
	for profileID, capacity := range limits.WorkerProfileLimits {
		if usage.WorkerProfiles[profileID] < capacity {
			profiles = append(profiles, profileID)
		}
	}
	if len(profiles) == 0 {
		return nil, nil
	}
	sort.Strings(profiles)
	cappedRepositories := make([]string, 0, len(usage.Repositories))
	for repositoryID, used := range usage.Repositories {
		if used >= limits.MaxConcurrentTasksPerRepository {
			cappedRepositories = append(cappedRepositories, repositoryID)
		}
	}
	sort.Strings(cappedRepositories)
	query := strings.Builder{}
	query.WriteString(`SELECT task_handle, initiative_handle, initiative_created_at, scheduling_round,
		repository_id, worker_profile_id FROM initiative_launch_facts
		WHERE scheduling_round = ? AND (initiative_created_at, initiative_handle, task_handle) > (?, ?, ?)
		AND worker_profile_id IN (`)
	args := []any{round, afterCreatedAt, afterInitiativeHandle, afterTaskHandle}
	for index, profileID := range profiles {
		if index > 0 {
			query.WriteByte(',')
		}
		query.WriteByte('?')
		args = append(args, profileID)
	}
	query.WriteByte(')')
	if len(cappedRepositories) > 0 {
		query.WriteString(` AND repository_id NOT IN (`)
		for index, repositoryID := range cappedRepositories {
			if index > 0 {
				query.WriteByte(',')
			}
			query.WriteByte('?')
			args = append(args, repositoryID)
		}
		query.WriteByte(')')
	}
	query.WriteString(` ORDER BY initiative_created_at, initiative_handle, task_handle LIMIT ?`)
	args = append(args, limit)
	rows, err := source.QueryContext(ctx, query.String(), args...)
	if err != nil {
		return nil, err
	}
	facts := make([]initiativeLaunchFact, 0, limit)
	for rows.Next() {
		fact, err := scanInitiativeLaunchFact(rows)
		if err != nil {
			_ = rows.Close()
			return nil, err
		}
		if fact.round != round {
			_ = rows.Close()
			return nil, errors.New("stored initiative launch fact round differs")
		}
		facts = append(facts, fact)
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return nil, err
	}
	return facts, nil
}
