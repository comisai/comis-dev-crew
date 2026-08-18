package sqlite

import (
	"context"
	"crypto/subtle"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/comisai/comis-dev-crew/internal/application"
	"github.com/comisai/comis-dev-crew/internal/domain"
)

// ReplayMutation returns the original atomic outcome for an identical operation subject.
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
		if auditErr := insertReplayConflict(ctx, store.db, operation, command, subjectDigest); auditErr != nil {
			return application.MutationResult{}, false, fmt.Errorf("audit operation altered replay: %w", auditErr)
		}
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
		return application.MutationResult{}, commitReplayConflict(transaction, err)
	} else if found {
		return replayResult(ctx, transaction, replay)
	}
	if err := consumeTaskPreparationIntent(ctx, transaction, mutation); err != nil {
		return application.MutationResult{}, err
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

// CommitManagedRunActivation atomically validates the private preparation,
// records exact host authority, and advances the task to ready.
func (store *Store) CommitManagedRunActivation(ctx context.Context, mutation application.ManagedRunActivationMutation) (application.MutationResult, error) {
	transaction, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return application.MutationResult{}, fmt.Errorf("begin managed-run activation: %w", err)
	}
	defer func() { _ = transaction.Rollback() }()

	if replay, found, err := mutationReplay(ctx, transaction, mutation.OperationID, commandActivateManagedRun, mutation.SubjectDigest); err != nil {
		return application.MutationResult{}, commitReplayConflict(transaction, err)
	} else if found {
		return replayResult(ctx, transaction, replay)
	}
	task, err := getTask(ctx, transaction, mutation.ExternalRunRef)
	if err != nil {
		return application.MutationResult{}, err
	}
	preparation, err := getManagedRunPreparation(ctx, transaction, task)
	if err != nil {
		return application.MutationResult{}, fmt.Errorf("read managed-run activation preparation: %w", err)
	}
	if task.ServiceInstanceID != mutation.ServiceInstanceID || preparation.ExternalRunRef != mutation.ExternalRunRef ||
		subtle.ConstantTimeCompare([]byte(preparation.RegistrationNonce), []byte(mutation.RegistrationNonce)) != 1 ||
		preparation.State != application.PreparationOpen || mutation.At.Location() != time.UTC ||
		!mutation.At.Before(preparation.ExpiresAt) {
		return application.MutationResult{}, fmt.Errorf("managed-run activation join: %w", application.ErrPrecondition)
	}
	requestedWorkspace := preparation.RequestedWorkspaceRoot != ""
	hasWorkspaceLease := mutation.Binding.WorkspaceLeaseID != ""
	if requestedWorkspace != hasWorkspaceLease {
		return application.MutationResult{}, fmt.Errorf("managed-run activation lease invariant: %w", application.ErrInvalidInput)
	}
	if !requestedWorkspace {
		return application.MutationResult{}, fmt.Errorf("managed-run activation requires a DevCrew workspace: %w", application.ErrPrecondition)
	}
	if preparation.RequestedAttachment.Validate() != nil ||
		domain.ValidateAuthorityReference("executionAttachmentId", mutation.ExecutionAttachmentID) != nil ||
		domain.ValidateAttachmentTargetName(mutation.AttachmentTargetName) != nil {
		return application.MutationResult{}, fmt.Errorf("managed-run activation attachment invariant: %w", application.ErrInvalidInput)
	}
	if task.State != domain.TaskPrepared || task.ManagedRunID != "" || task.WorkspaceLeaseID != "" ||
		task.ExecutionAttachmentID != "" || task.AttachmentTargetName != "" {
		return application.MutationResult{}, fmt.Errorf("managed-run activation task state: %w", application.ErrPrecondition)
	}
	bound, err := task.AcknowledgeBinding(mutation.Binding, mutation.At)
	if err != nil {
		return application.MutationResult{}, fmt.Errorf("apply managed-run activation: %w", err)
	}
	bound.ExecutionAttachmentID = mutation.ExecutionAttachmentID
	bound.AttachmentTargetName = mutation.AttachmentTargetName
	stateVersion, err := nextMutationStateVersion(ctx, transaction)
	if err != nil {
		return application.MutationResult{}, err
	}
	bound.StateVersion = stateVersion
	if err := bound.Validate(); err != nil {
		return application.MutationResult{}, fmt.Errorf("validate bound task: %w", err)
	}
	operation := completedMutationOperation(
		mutation.OperationID, commandActivateManagedRun, mutation.SubjectDigest,
		bound.Handle, stateVersion, mutation.At,
	)
	const update = `UPDATE tasks
		SET managed_run_id = ?, workspace_lease_id = ?, execution_attachment_id = ?, attachment_target_name = ?,
		state = ?, state_version = ?, updated_at = ?
        WHERE handle = ?`
	result, err := transaction.ExecContext(ctx, update,
		bound.ManagedRunID, bound.WorkspaceLeaseID, bound.ExecutionAttachmentID, bound.AttachmentTargetName, bound.State,
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
			return application.MutationResult{}, fmt.Errorf("insert activation operation: %w", application.ErrConflict)
		}
		return application.MutationResult{}, fmt.Errorf("insert activation operation: %w", err)
	}
	if err := transaction.Commit(); err != nil {
		return application.MutationResult{}, fmt.Errorf("commit managed-run activation: %w", err)
	}
	return application.MutationResult{Task: bound, Operation: operation}, nil
}

// CommitManagedRunAbandon closes one exact unbound preparation and records the
// preserve or reversible-cleanup task posture at one state version.
func (store *Store) CommitManagedRunAbandon(ctx context.Context, mutation application.ManagedRunAbandonMutation) (application.MutationResult, error) {
	var closed application.ManagedRunPreparation
	result, err := commitTaskMutation(ctx, store, taskMutationSpec{
		Command:     commandAbandonManagedRun,
		OperationID: mutation.OperationID, SubjectDigest: mutation.SubjectDigest,
		At: mutation.At, Label: "managed-run abandon",
	}, func(ctx context.Context, transaction *sql.Tx) (domain.Task, error) {
		task, err := getTask(ctx, transaction, mutation.ExternalRunRef)
		if err != nil {
			return domain.Task{}, err
		}
		preparation, err := getManagedRunPreparation(ctx, transaction, task)
		if err != nil {
			return domain.Task{}, fmt.Errorf("read managed-run abandon preparation: %w", err)
		}
		if task.ServiceInstanceID != mutation.ServiceInstanceID || preparation.ExternalRunRef != mutation.ExternalRunRef ||
			subtle.ConstantTimeCompare([]byte(preparation.RegistrationNonce), []byte(mutation.RegistrationNonce)) != 1 ||
			preparation.State != application.PreparationOpen || mutation.At.Location() != time.UTC {
			return domain.Task{}, fmt.Errorf("managed-run abandon join: %w", application.ErrPrecondition)
		}
		transition := domain.TransitionPreparationPreserved
		if mutation.Disposition == application.AbandonDispositionReapSafe {
			transition = domain.TransitionPreparationAbandoned
		} else if mutation.Disposition != application.AbandonDispositionPreserve {
			return domain.Task{}, fmt.Errorf("managed-run abandon disposition: %w", application.ErrInvalidInput)
		}
		updated, err := task.ApplyTransition(transition, mutation.At)
		if err != nil {
			return domain.Task{}, fmt.Errorf("apply managed-run abandon: %w", err)
		}
		closedAt := mutation.At
		preparation.State = application.PreparationAbandoned
		preparation.AbandonReason = mutation.Reason
		preparation.Disposition = mutation.Disposition
		preparation.ClosedAt = &closedAt
		// The preparation is written against the task's post-transition identity,
		// so it is persisted here rather than after the shared skeleton commits.
		if err := updateManagedRunPreparation(ctx, transaction, updated, preparation); err != nil {
			return domain.Task{}, err
		}
		closed = preparation
		return updated, nil
	})
	if err != nil {
		return application.MutationResult{}, err
	}
	result.Preparation = &closed
	return result, nil
}

// CommitTaskStart atomically records launch intent and its replay outcome
// before a worker is allowed to begin.
func (store *Store) CommitTaskStart(ctx context.Context, mutation application.TaskStartMutation) (application.MutationResult, error) {
	transaction, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return application.MutationResult{}, fmt.Errorf("begin task start mutation: %w", err)
	}
	defer func() { _ = transaction.Rollback() }()

	if replay, found, err := mutationReplay(ctx, transaction, mutation.OperationID, commandStartTask, mutation.SubjectDigest); err != nil {
		return application.MutationResult{}, commitReplayConflict(transaction, err)
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
	commandPrepareTask             = "PrepareTask"
	commandActivateManagedRun      = "ActivateManagedRun"
	commandAbandonManagedRun       = "AbandonManagedRun"
	commandCancelManagedRun        = "CancelManagedRun"
	commandStartTask               = "StartTask"
	commandRecordTerminalEvent     = "RecordTerminalEvent"
	commandAcknowledgeWorkerLaunch = "AcknowledgeWorkerLaunch"
	commandPauseTask               = "PauseTask"
	commandCancelTask              = "CancelTask"
	commandResumeTask              = "ResumeTask"
	commandVerifyTask              = "VerifyTask"
	commandReplaceWorker           = "ReplaceWorker"
	commandSteerTask               = "SteerTask"
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
		if err := insertReplayConflict(ctx, transaction, existing, command, subjectDigest); err != nil {
			return domain.OperationRecord{}, false, fmt.Errorf("audit operation altered replay: %w", err)
		}
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
	if operation.Command != commandPrepareTask && operation.Command != commandAbandonManagedRun {
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
		task_handle, external_run_ref, registration_nonce, expires_at, created_at,
		requested_workspace_root, requested_attachment_kind, requested_attachment_source_path,
		requested_attachment_relay_identity,
		state, abandon_reason, disposition, closed_at
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`
	_, err := target.ExecContext(ctx, statement, taskHandle, preparation.ExternalRunRef,
		preparation.RegistrationNonce, formatTime(preparation.ExpiresAt), formatTime(createdAt),
		preparation.RequestedWorkspaceRoot, preparation.RequestedAttachment.Kind,
		preparation.RequestedAttachment.SourcePath, preparation.RequestedAttachment.RelayIdentity,
		preparation.State, preparation.AbandonReason,
		preparation.Disposition, nil)
	return err
}

func getManagedRunPreparation(ctx context.Context, source queryer, task domain.Task) (application.ManagedRunPreparation, error) {
	const query = `SELECT external_run_ref, registration_nonce, expires_at,
		requested_workspace_root, requested_attachment_kind, requested_attachment_source_path,
		requested_attachment_relay_identity,
		state, abandon_reason, disposition, closed_at,
		EXISTS(SELECT 1 FROM runtime_relay_identity_upgrades u WHERE u.task_handle = task_preparations.task_handle)
        FROM task_preparations WHERE task_handle = ?`
	var preparation application.ManagedRunPreparation
	var expiresAt string
	var closedAt sql.NullString
	var relayUpgradePending bool
	if err := source.QueryRowContext(ctx, query, task.Handle).Scan(
		&preparation.ExternalRunRef, &preparation.RegistrationNonce, &expiresAt,
		&preparation.RequestedWorkspaceRoot, &preparation.RequestedAttachment.Kind,
		&preparation.RequestedAttachment.SourcePath, &preparation.RequestedAttachment.RelayIdentity,
		&preparation.State, &preparation.AbandonReason,
		&preparation.Disposition, &closedAt, &relayUpgradePending,
	); err != nil {
		return application.ManagedRunPreparation{}, err
	}
	var err error
	preparation.ExpiresAt, err = parseTime(expiresAt)
	if err != nil {
		return application.ManagedRunPreparation{}, err
	}
	if closedAt.Valid {
		parsed, err := parseTime(closedAt.String)
		if err != nil {
			return application.ManagedRunPreparation{}, err
		}
		preparation.ClosedAt = &parsed
	}
	if err := preparation.Validate(task.CreatedAt); err != nil || preparation.ExternalRunRef != task.Handle || relayUpgradePending {
		return application.ManagedRunPreparation{}, errors.New("stored managed-run preparation is invalid")
	}
	return preparation, nil
}

// GetManagedRunPreparation returns the private durable join for service-owned
// reconciliation and bounded diagnostics.
func (store *Store) GetManagedRunPreparation(ctx context.Context, taskHandle string) (application.ManagedRunPreparation, error) {
	task, err := getTask(ctx, store.db, taskHandle)
	if err != nil {
		return application.ManagedRunPreparation{}, err
	}
	return getManagedRunPreparation(ctx, store.db, task)
}

func updateManagedRunPreparation(
	ctx context.Context,
	target execer,
	task domain.Task,
	preparation application.ManagedRunPreparation,
) error {
	if err := preparation.Validate(task.CreatedAt); err != nil || preparation.ExternalRunRef != task.Handle || preparation.ClosedAt == nil {
		return errors.New("update managed-run preparation: invalid closure")
	}
	const statement = `UPDATE task_preparations
		SET state = ?, abandon_reason = ?, disposition = ?, closed_at = ?
		WHERE task_handle = ? AND state = ?`
	result, err := target.ExecContext(ctx, statement, preparation.State, preparation.AbandonReason,
		preparation.Disposition, formatTime(*preparation.ClosedAt), task.Handle, application.PreparationOpen)
	if err != nil {
		return fmt.Errorf("update managed-run preparation: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil || rows != 1 {
		return fmt.Errorf("update managed-run preparation: %w", application.ErrPrecondition)
	}
	return nil
}
