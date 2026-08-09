package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/comisai/comis-dev-crew/internal/application"
	"github.com/comisai/comis-dev-crew/internal/domain"
)

// ReplayMutation returns the original atomic task outcome for an identical
// operation subject before callers mint any new local identity. Commit methods
// repeat this check inside their write transaction to close the race window.
func (store *Store) ReplayMutation(
	ctx context.Context,
	operationID, command, subjectDigest string,
) (application.MutationResult, bool, error) {
	operation, err := store.GetOperation(ctx, operationID)
	if errors.Is(err, application.ErrNotFound) {
		return application.MutationResult{}, false, nil
	}
	if err != nil {
		return application.MutationResult{}, false, err
	}
	if operation.Command != command || operation.SubjectDigest != subjectDigest {
		return application.MutationResult{}, false, fmt.Errorf("operation altered replay: %w", application.ErrConflict)
	}
	if operation.ResultRef == "" {
		return application.MutationResult{}, false, errors.New("operation replay has no result reference")
	}
	result, err := store.mutationResult(ctx, store.db, operation)
	return result, err == nil, err
}

// CommitPreparedTask atomically creates one task and its completed replay
// outcome at a single global state version.
func (store *Store) CommitPreparedTask(ctx context.Context, mutation application.PreparedTaskMutation) (application.MutationResult, error) {
	if err := mutation.Task.Validate(); err != nil || mutation.Task.State != domain.TaskPrepared {
		return application.MutationResult{}, errors.New("commit prepared task: invalid prepared record")
	}
	if err := mutation.Preparation.Validate(mutation.At); err != nil || mutation.Preparation.ExternalRunRef != mutation.Task.Handle {
		return application.MutationResult{}, errors.New("commit prepared task: invalid managed-run preparation")
	}
	transaction, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return application.MutationResult{}, fmt.Errorf("begin prepared task mutation: %w", err)
	}
	defer func() { _ = transaction.Rollback() }()

	if replay, found, err := mutationReplay(ctx, transaction, mutation.OperationID, commandPrepareTask, mutation.SubjectDigest); err != nil {
		return application.MutationResult{}, err
	} else if found {
		return replayResult(ctx, transaction, replay)
	}
	stateVersion, err := nextMutationStateVersion(ctx, transaction)
	if err != nil {
		return application.MutationResult{}, err
	}
	task := mutation.Task
	task.StateVersion = stateVersion
	if err := task.Validate(); err != nil {
		return application.MutationResult{}, fmt.Errorf("validate versioned prepared task: %w", err)
	}
	operation := completedMutationOperation(
		mutation.OperationID, commandPrepareTask, mutation.SubjectDigest,
		task.Handle, stateVersion, mutation.At,
	)
	if err := insertTask(ctx, transaction, task); err != nil {
		return application.MutationResult{}, fmt.Errorf("insert prepared task: %w", err)
	}
	if err := insertManagedRunPreparation(ctx, transaction, task.Handle, mutation.Preparation, mutation.At); err != nil {
		return application.MutationResult{}, fmt.Errorf("insert managed-run preparation: %w", err)
	}
	if err := insertOperation(ctx, transaction, operation); err != nil {
		if isConstraintError(err) {
			return application.MutationResult{}, fmt.Errorf("insert prepare operation: %w", application.ErrConflict)
		}
		return application.MutationResult{}, fmt.Errorf("insert prepare operation: %w", err)
	}
	if err := transaction.Commit(); err != nil {
		return application.MutationResult{}, fmt.Errorf("commit prepared task mutation: %w", err)
	}
	preparation := mutation.Preparation
	return application.MutationResult{Task: task, Operation: operation, Preparation: &preparation}, nil
}

// CommitTaskBinding atomically records exact host authority, advances the task
// to ready, and persists its replay outcome at one state version.
func (store *Store) CommitTaskBinding(ctx context.Context, mutation application.TaskBindingMutation) (application.MutationResult, error) {
	transaction, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return application.MutationResult{}, fmt.Errorf("begin task binding mutation: %w", err)
	}
	defer func() { _ = transaction.Rollback() }()

	if replay, found, err := mutationReplay(ctx, transaction, mutation.OperationID, commandAcknowledgeBinding, mutation.SubjectDigest); err != nil {
		return application.MutationResult{}, err
	} else if found {
		return replayResult(ctx, transaction, replay)
	}
	task, err := getTask(ctx, transaction, mutation.TaskHandle)
	if err != nil {
		return application.MutationResult{}, err
	}
	bound, err := task.AcknowledgeBinding(mutation.Binding, mutation.At)
	if err != nil {
		return application.MutationResult{}, fmt.Errorf("apply task binding: %w", err)
	}
	stateVersion, err := nextMutationStateVersion(ctx, transaction)
	if err != nil {
		return application.MutationResult{}, err
	}
	bound.StateVersion = stateVersion
	if err := bound.Validate(); err != nil {
		return application.MutationResult{}, fmt.Errorf("validate bound task: %w", err)
	}
	operation := completedMutationOperation(
		mutation.OperationID, commandAcknowledgeBinding, mutation.SubjectDigest,
		bound.Handle, stateVersion, mutation.At,
	)
	const update = `UPDATE tasks
        SET managed_run_id = ?, workspace_lease_id = ?, state = ?, state_version = ?, updated_at = ?
        WHERE handle = ?`
	result, err := transaction.ExecContext(ctx, update,
		bound.ManagedRunID, bound.WorkspaceLeaseID, bound.State,
		bound.StateVersion, formatTime(bound.UpdatedAt), bound.Handle,
	)
	if err != nil {
		return application.MutationResult{}, fmt.Errorf("update bound task: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil || rows != 1 {
		return application.MutationResult{}, errors.New("update bound task: exact task was not updated")
	}
	if err := insertOperation(ctx, transaction, operation); err != nil {
		if isConstraintError(err) {
			return application.MutationResult{}, fmt.Errorf("insert binding operation: %w", application.ErrConflict)
		}
		return application.MutationResult{}, fmt.Errorf("insert binding operation: %w", err)
	}
	if err := transaction.Commit(); err != nil {
		return application.MutationResult{}, fmt.Errorf("commit task binding mutation: %w", err)
	}
	return application.MutationResult{Task: bound, Operation: operation}, nil
}

// CommitTaskStart atomically records launch intent and its replay outcome
// before a fixture worker is allowed to begin.
func (store *Store) CommitTaskStart(ctx context.Context, mutation application.TaskStartMutation) (application.MutationResult, error) {
	transaction, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return application.MutationResult{}, fmt.Errorf("begin task start mutation: %w", err)
	}
	defer func() { _ = transaction.Rollback() }()

	if replay, found, err := mutationReplay(ctx, transaction, mutation.OperationID, commandStartTask, mutation.SubjectDigest); err != nil {
		return application.MutationResult{}, err
	} else if found {
		return replayResult(ctx, transaction, replay)
	}
	task, err := getTask(ctx, transaction, mutation.TaskHandle)
	if err != nil {
		return application.MutationResult{}, err
	}
	started, err := task.ApplyTransition(domain.TransitionLaunchRequested, mutation.At)
	if err != nil {
		return application.MutationResult{}, fmt.Errorf("apply task start: %w", err)
	}
	stateVersion, err := nextMutationStateVersion(ctx, transaction)
	if err != nil {
		return application.MutationResult{}, err
	}
	started.StateVersion = stateVersion
	if err := updateTaskState(ctx, transaction, started); err != nil {
		return application.MutationResult{}, err
	}
	operation := completedMutationOperation(
		mutation.OperationID, commandStartTask, mutation.SubjectDigest,
		started.Handle, stateVersion, mutation.At,
	)
	if err := insertOperation(ctx, transaction, operation); err != nil {
		return application.MutationResult{}, fmt.Errorf("insert task start operation: %w", err)
	}
	if err := transaction.Commit(); err != nil {
		return application.MutationResult{}, fmt.Errorf("commit task start mutation: %w", err)
	}
	return application.MutationResult{Task: started, Operation: operation}, nil
}

const (
	commandPrepareTask        = "PrepareTask"
	commandAcknowledgeBinding = "AcknowledgeBinding"
	commandStartTask          = "StartTask"
)

func updateTaskState(ctx context.Context, transaction *sql.Tx, task domain.Task) error {
	if err := task.Validate(); err != nil {
		return fmt.Errorf("validate task state update: %w", err)
	}
	const update = `UPDATE tasks SET state = ?, state_version = ?, updated_at = ? WHERE handle = ?`
	result, err := transaction.ExecContext(ctx, update, task.State, task.StateVersion, formatTime(task.UpdatedAt), task.Handle)
	if err != nil {
		return fmt.Errorf("update task state: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil || rows != 1 {
		return errors.New("update task state: exact task was not updated")
	}
	return nil
}

func mutationReplay(
	ctx context.Context,
	transaction *sql.Tx,
	operationID, command, subjectDigest string,
) (domain.OperationRecord, bool, error) {
	existing, err := getOperation(ctx, transaction, operationID)
	if errors.Is(err, application.ErrNotFound) {
		return domain.OperationRecord{}, false, nil
	}
	if err != nil {
		return domain.OperationRecord{}, false, err
	}
	if existing.Command != command || existing.SubjectDigest != subjectDigest {
		return domain.OperationRecord{}, false, fmt.Errorf("operation altered replay: %w", application.ErrConflict)
	}
	if existing.ResultRef == "" {
		return domain.OperationRecord{}, false, errors.New("operation replay has no result reference")
	}
	return existing, true, nil
}

func replayResult(ctx context.Context, transaction *sql.Tx, operation domain.OperationRecord) (application.MutationResult, error) {
	return mutationResult(ctx, transaction, operation)
}

func (store *Store) mutationResult(ctx context.Context, source queryer, operation domain.OperationRecord) (application.MutationResult, error) {
	return mutationResult(ctx, source, operation)
}

func mutationResult(ctx context.Context, source queryer, operation domain.OperationRecord) (application.MutationResult, error) {
	task, err := getTask(ctx, source, operation.ResultRef)
	if err != nil {
		return application.MutationResult{}, fmt.Errorf("read operation replay task: %w", err)
	}
	result := application.MutationResult{Task: task, Operation: operation}
	if operation.Command != commandPrepareTask {
		return result, nil
	}
	preparation, err := getManagedRunPreparation(ctx, source, task)
	if err != nil {
		return application.MutationResult{}, fmt.Errorf("read operation replay preparation: %w", err)
	}
	result.Preparation = &preparation
	return result, nil
}

func insertManagedRunPreparation(
	ctx context.Context,
	target execer,
	taskHandle string,
	preparation application.ManagedRunPreparation,
	createdAt time.Time,
) error {
	const statement = `INSERT INTO task_preparations (
        task_handle, external_run_ref, registration_nonce, expires_at, created_at
    ) VALUES (?, ?, ?, ?, ?)`
	_, err := target.ExecContext(ctx, statement, taskHandle, preparation.ExternalRunRef,
		preparation.RegistrationNonce, formatTime(preparation.ExpiresAt), formatTime(createdAt))
	return err
}

func getManagedRunPreparation(ctx context.Context, source queryer, task domain.Task) (application.ManagedRunPreparation, error) {
	const query = `SELECT external_run_ref, registration_nonce, expires_at
        FROM task_preparations WHERE task_handle = ?`
	var preparation application.ManagedRunPreparation
	var expiresAt string
	if err := source.QueryRowContext(ctx, query, task.Handle).Scan(
		&preparation.ExternalRunRef, &preparation.RegistrationNonce, &expiresAt,
	); err != nil {
		return application.ManagedRunPreparation{}, err
	}
	var err error
	preparation.ExpiresAt, err = parseTime(expiresAt)
	if err != nil {
		return application.ManagedRunPreparation{}, err
	}
	if err := preparation.Validate(task.CreatedAt); err != nil || preparation.ExternalRunRef != task.Handle {
		return application.ManagedRunPreparation{}, errors.New("stored managed-run preparation is invalid")
	}
	return preparation, nil
}

func nextMutationStateVersion(ctx context.Context, transaction *sql.Tx) (int64, error) {
	current, err := currentStateVersion(ctx, transaction)
	if err != nil {
		return 0, err
	}
	if current == math.MaxInt64 {
		return 0, errors.New("state version is exhausted")
	}
	return current + 1, nil
}

func completedMutationOperation(id, command, digest, resultRef string, stateVersion int64, at time.Time) domain.OperationRecord {
	return domain.OperationRecord{
		SchemaVersion: 1, ID: id, Command: command, SubjectDigest: digest,
		Status: domain.OperationCompleted, ResultRef: resultRef, StateVersion: stateVersion,
		CreatedAt: at, UpdatedAt: at,
	}
}
