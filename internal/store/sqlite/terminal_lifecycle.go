package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/comisai/comis-dev-crew/internal/application"
	"github.com/comisai/comis-dev-crew/internal/domain"
)

type storedTerminalBinding struct {
	taskHandle        string
	managedRunID      string
	workspaceLeaseID  string
	terminalSessionID string
	latestTransition  application.TerminalTransition
	runningObserved   bool
	updatedAt         time.Time
}

// CommitTerminalEvent atomically cross-binds an authenticated Comis event to
// one task. Terminal liveness is necessary but insufficient launch evidence.
func (store *Store) CommitTerminalEvent(ctx context.Context, mutation application.TerminalEventMutation) (application.MutationResult, error) {
	if err := validateTerminalEventMutation(mutation); err != nil {
		return application.MutationResult{}, err
	}
	transaction, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return application.MutationResult{}, fmt.Errorf("begin terminal event mutation: %w", err)
	}
	defer func() { _ = transaction.Rollback() }()
	if replay, found, err := mutationReplay(ctx, transaction, mutation.OperationID, commandRecordTerminalEvent, mutation.SubjectDigest); err != nil {
		return application.MutationResult{}, commitReplayConflict(transaction, err)
	} else if found {
		return replayResult(ctx, transaction, replay)
	}
	task, err := getTaskByBinding(ctx, transaction, mutation.ManagedRunID, mutation.WorkspaceLeaseID)
	if err != nil {
		return application.MutationResult{}, err
	}
	if task.State == domain.TaskReady || mutation.At.Before(task.UpdatedAt) {
		return application.MutationResult{}, fmt.Errorf("terminal event task posture: %w", application.ErrPrecondition)
	}
	binding, found, err := findTerminalBinding(ctx, transaction, task.Handle)
	if err != nil {
		return application.MutationResult{}, err
	}
	if found && (binding.managedRunID != mutation.ManagedRunID || binding.workspaceLeaseID != mutation.WorkspaceLeaseID ||
		binding.terminalSessionID != mutation.TerminalSessionID) {
		return application.MutationResult{}, fmt.Errorf("terminal event binding differs: %w", application.ErrPrecondition)
	}
	if !found {
		binding = storedTerminalBinding{
			taskHandle: task.Handle, managedRunID: mutation.ManagedRunID,
			workspaceLeaseID: mutation.WorkspaceLeaseID, terminalSessionID: mutation.TerminalSessionID,
		}
	}
	binding.latestTransition = mutation.Transition
	binding.runningObserved = binding.runningObserved || mutation.Transition == application.TerminalRunning
	binding.updatedAt = mutation.At

	updated := task
	if terminalUnavailable(mutation.Transition) && terminalLossChangesTask(task.State) {
		updated, err = task.ApplyTransition(domain.TransitionTerminalUnavailable, mutation.At)
		if err != nil {
			return application.MutationResult{}, fmt.Errorf("apply terminal loss: %w", err)
		}
	} else if task.State == domain.TaskLaunching && binding.runningObserved {
		acknowledged, err := hasLaunchAcknowledgement(ctx, transaction, task.Handle)
		if err != nil {
			return application.MutationResult{}, err
		}
		if acknowledged {
			updated, err = task.ApplyTransition(domain.TransitionWorkerAcknowledged, mutation.At)
			if err != nil {
				return application.MutationResult{}, fmt.Errorf("apply joined launch evidence: %w", err)
			}
		}
	}
	if updated.State == task.State {
		updated.UpdatedAt = mutation.At
	}
	stateVersion, err := nextMutationStateVersion(ctx, transaction)
	if err != nil {
		return application.MutationResult{}, err
	}
	updated.StateVersion = stateVersion
	operation := completedMutationOperation(
		mutation.OperationID, commandRecordTerminalEvent, mutation.SubjectDigest,
		updated.Handle, stateVersion, mutation.At,
	)
	if err := updateTaskState(ctx, transaction, updated); err != nil {
		return application.MutationResult{}, err
	}
	if err := insertOperation(ctx, transaction, operation); err != nil {
		return application.MutationResult{}, terminalConstraintFailure("insert terminal event operation", err)
	}
	if err := putTerminalBinding(ctx, transaction, binding, found); err != nil {
		return application.MutationResult{}, err
	}
	const insertEvent = `INSERT INTO task_terminal_events (
        operation_id, task_handle, managed_run_id, workspace_lease_id,
        terminal_session_id, transition, observed_at
    ) VALUES (?, ?, ?, ?, ?, ?, ?)`
	if _, err := transaction.ExecContext(ctx, insertEvent, mutation.OperationID, task.Handle,
		mutation.ManagedRunID, mutation.WorkspaceLeaseID, mutation.TerminalSessionID,
		mutation.Transition, formatTime(mutation.At)); err != nil {
		return application.MutationResult{}, terminalConstraintFailure("insert terminal event", err)
	}
	if err := transaction.Commit(); err != nil {
		return application.MutationResult{}, fmt.Errorf("commit terminal event mutation: %w", err)
	}
	return application.MutationResult{Task: updated, Operation: operation}, nil
}

// CommitWorkerLaunchAcknowledgement records one exact protected wrapper echo.
// Production profiles also require durable terminal-running evidence.
func (store *Store) CommitWorkerLaunchAcknowledgement(ctx context.Context, mutation application.WorkerLaunchAcknowledgementMutation) (application.MutationResult, error) {
	if err := validateWorkerLaunchAcknowledgementMutation(mutation); err != nil {
		return application.MutationResult{}, err
	}
	transaction, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return application.MutationResult{}, fmt.Errorf("begin worker launch acknowledgement: %w", err)
	}
	defer func() { _ = transaction.Rollback() }()
	if replay, found, err := mutationReplay(ctx, transaction, mutation.OperationID, commandAcknowledgeWorkerLaunch, mutation.SubjectDigest); err != nil {
		return application.MutationResult{}, commitReplayConflict(transaction, err)
	} else if found {
		return replayResult(ctx, transaction, replay)
	}
	acknowledgement := mutation.Acknowledgement
	task, err := getTask(ctx, transaction, acknowledgement.TaskHandle)
	if err != nil {
		return application.MutationResult{}, err
	}
	preparation, err := getManagedRunPreparation(ctx, transaction, task)
	if err != nil {
		return application.MutationResult{}, fmt.Errorf("read launch workspace: %w", err)
	}
	if task.State != domain.TaskLaunching || mutation.At.Before(task.UpdatedAt) ||
		task.ManagedRunID != acknowledgement.ManagedRunID || task.WorkspaceLeaseID != acknowledgement.WorkspaceLeaseID ||
		task.BriefRevision != acknowledgement.BriefRevision || task.BriefRevisionHash != acknowledgement.BriefRevisionHash ||
		preparation.RequestedWorkspaceRoot != acknowledgement.WorkingDirectory {
		return application.MutationResult{}, fmt.Errorf("worker launch acknowledgement differs: %w", application.ErrPrecondition)
	}
	binding, terminalFound, err := findTerminalBinding(ctx, transaction, task.Handle)
	if err != nil {
		return application.MutationResult{}, err
	}
	terminalReady := terminalFound && binding.runningObserved
	if terminalFound && (binding.managedRunID != task.ManagedRunID || binding.workspaceLeaseID != task.WorkspaceLeaseID) {
		return application.MutationResult{}, fmt.Errorf("worker launch terminal binding differs: %w", application.ErrPrecondition)
	}
	// The deterministic fixture launches no terminal process; this explicit
	// profile exception keeps it useful without weakening production profiles.
	terminalReady = terminalReady || task.WorkerProfileID == "fixture-worker"
	updated := task
	if terminalReady {
		updated, err = task.ApplyTransition(domain.TransitionWorkerAcknowledged, mutation.At)
		if err != nil {
			return application.MutationResult{}, fmt.Errorf("apply worker launch acknowledgement: %w", err)
		}
	} else {
		updated.UpdatedAt = mutation.At
	}
	stateVersion, err := nextMutationStateVersion(ctx, transaction)
	if err != nil {
		return application.MutationResult{}, err
	}
	updated.StateVersion = stateVersion
	operation := completedMutationOperation(
		mutation.OperationID, commandAcknowledgeWorkerLaunch, mutation.SubjectDigest,
		updated.Handle, stateVersion, mutation.At,
	)
	if err := updateTaskState(ctx, transaction, updated); err != nil {
		return application.MutationResult{}, err
	}
	if err := insertOperation(ctx, transaction, operation); err != nil {
		return application.MutationResult{}, terminalConstraintFailure("insert launch acknowledgement operation", err)
	}
	const insert = `INSERT INTO task_launch_acknowledgements (
        operation_id, task_handle, managed_run_id, workspace_lease_id,
        working_directory, brief_revision, brief_revision_hash, acknowledged_at
    ) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`
	if _, err := transaction.ExecContext(ctx, insert, mutation.OperationID, task.Handle,
		acknowledgement.ManagedRunID, acknowledgement.WorkspaceLeaseID,
		acknowledgement.WorkingDirectory, acknowledgement.BriefRevision,
		acknowledgement.BriefRevisionHash, formatTime(mutation.At)); err != nil {
		return application.MutationResult{}, terminalConstraintFailure("insert launch acknowledgement", err)
	}
	if err := transaction.Commit(); err != nil {
		return application.MutationResult{}, fmt.Errorf("commit worker launch acknowledgement: %w", err)
	}
	return application.MutationResult{Task: updated, Operation: operation}, nil
}

func validateTerminalEventMutation(mutation application.TerminalEventMutation) error {
	if err := domain.ValidateOperationID(mutation.OperationID); err != nil ||
		domain.ValidateAuthorityReference("managedRunId", mutation.ManagedRunID) != nil ||
		domain.ValidateAuthorityReference("workspaceLeaseId", mutation.WorkspaceLeaseID) != nil ||
		domain.ValidateAuthorityReference("terminalSessionId", mutation.TerminalSessionID) != nil ||
		!mutation.Transition.Valid() ||
		mutation.At.IsZero() || mutation.At.Location() != time.UTC {
		return errors.New("commit terminal event: invalid mutation")
	}
	return nil
}

func validateWorkerLaunchAcknowledgementMutation(mutation application.WorkerLaunchAcknowledgementMutation) error {
	if err := domain.ValidateOperationID(mutation.OperationID); err != nil || mutation.Acknowledgement.Validate() != nil ||
		mutation.At.IsZero() || mutation.At.Location() != time.UTC {
		return errors.New("commit worker launch acknowledgement: invalid mutation")
	}
	return nil
}

func getTaskByBinding(ctx context.Context, source queryer, managedRunID, workspaceLeaseID string) (domain.Task, error) {
	const query = `SELECT
		handle, schema_version, service_instance_id, managed_run_id,
		workspace_lease_id, execution_attachment_id, attachment_target_name,
		state, shape, repository_id, base_revision,
        brief_revision, brief_revision_hash, acceptance_criteria_json,
        constraints_json, validation_profile, delivery_mode, worker_profile_id,
        report_cursor, state_version, created_at, updated_at
    FROM tasks WHERE managed_run_id = ? AND workspace_lease_id = ?`
	rows, err := source.QueryContext(ctx, query, managedRunID, workspaceLeaseID)
	if err != nil {
		return domain.Task{}, fmt.Errorf("get task by terminal binding: %w", err)
	}
	defer rows.Close()
	var matches []domain.Task
	for rows.Next() {
		task, err := scanTask(rows)
		if err != nil {
			return domain.Task{}, fmt.Errorf("get task by terminal binding: %w", err)
		}
		matches = append(matches, task)
	}
	if err := rows.Err(); err != nil {
		return domain.Task{}, fmt.Errorf("get task by terminal binding: %w", err)
	}
	if len(matches) != 1 {
		return domain.Task{}, fmt.Errorf("get task by terminal binding is absent or ambiguous: %w", application.ErrPrecondition)
	}
	return matches[0], nil
}

func findTerminalBinding(ctx context.Context, source queryer, taskHandle string) (storedTerminalBinding, bool, error) {
	const query = `SELECT task_handle, managed_run_id, workspace_lease_id,
        terminal_session_id, latest_transition, running_observed, updated_at
        FROM task_terminal_bindings WHERE task_handle = ?`
	var binding storedTerminalBinding
	var updatedAt string
	if err := source.QueryRowContext(ctx, query, taskHandle).Scan(
		&binding.taskHandle, &binding.managedRunID, &binding.workspaceLeaseID,
		&binding.terminalSessionID, &binding.latestTransition, &binding.runningObserved, &updatedAt,
	); errors.Is(err, sql.ErrNoRows) {
		return storedTerminalBinding{}, false, nil
	} else if err != nil {
		return storedTerminalBinding{}, false, fmt.Errorf("read terminal binding: %w", err)
	}
	parsed, err := parseTime(updatedAt)
	if err != nil {
		return storedTerminalBinding{}, false, fmt.Errorf("parse terminal binding time: %w", err)
	}
	binding.updatedAt = parsed
	return binding, true, nil
}

func putTerminalBinding(ctx context.Context, target execer, binding storedTerminalBinding, exists bool) error {
	if !exists {
		const insert = `INSERT INTO task_terminal_bindings (
            task_handle, managed_run_id, workspace_lease_id, terminal_session_id,
            latest_transition, running_observed, updated_at
        ) VALUES (?, ?, ?, ?, ?, ?, ?)`
		if _, err := target.ExecContext(ctx, insert, binding.taskHandle, binding.managedRunID,
			binding.workspaceLeaseID, binding.terminalSessionID, binding.latestTransition,
			binding.runningObserved, formatTime(binding.updatedAt)); err != nil {
			return terminalConstraintFailure("insert terminal binding", err)
		}
		return nil
	}
	const update = `UPDATE task_terminal_bindings SET latest_transition = ?,
        running_observed = ?, updated_at = ? WHERE task_handle = ? AND terminal_session_id = ?`
	result, err := target.ExecContext(ctx, update, binding.latestTransition, binding.runningObserved,
		formatTime(binding.updatedAt), binding.taskHandle, binding.terminalSessionID)
	if err != nil {
		return fmt.Errorf("update terminal binding: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil || rows != 1 {
		return fmt.Errorf("update terminal binding: %w", application.ErrPrecondition)
	}
	return nil
}

func hasLaunchAcknowledgement(ctx context.Context, source queryer, taskHandle string) (bool, error) {
	var count int
	if err := source.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM task_launch_acknowledgements WHERE task_handle = ?", taskHandle,
	).Scan(&count); err != nil {
		return false, fmt.Errorf("inspect launch acknowledgement: %w", err)
	}
	return count == 1, nil
}

func terminalUnavailable(transition application.TerminalTransition) bool {
	return transition == application.TerminalExited || transition == application.TerminalLost || transition == application.TerminalReleased
}

func terminalLossChangesTask(state domain.TaskState) bool {
	switch state {
	case domain.TaskLaunching, domain.TaskWorking, domain.TaskAwaitingDecision, domain.TaskBlocked:
		return true
	default:
		return false
	}
}

func terminalConstraintFailure(action string, err error) error {
	if isConstraintError(err) {
		return fmt.Errorf("%s: %w", action, application.ErrConflict)
	}
	return fmt.Errorf("%s: %w", action, err)
}
