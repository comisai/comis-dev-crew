package sqlite

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/comisai/comis-dev-crew/internal/application"
	"github.com/comisai/comis-dev-crew/internal/domain"
)

const commandCleanupTask = "CleanupTask"

const taskCleanupMigration = `
CREATE TABLE task_cleanup_holds (
    task_handle TEXT NOT NULL,
    hold_id TEXT NOT NULL,
    reason TEXT NOT NULL,
    opened_at TEXT NOT NULL,
    closed_at TEXT,
    PRIMARY KEY(task_handle, hold_id),
    FOREIGN KEY(task_handle) REFERENCES tasks(handle)
);
CREATE TABLE task_cleanup_operations (
    operation_id TEXT PRIMARY KEY,
    subject_digest TEXT NOT NULL,
    task_handle TEXT NOT NULL UNIQUE,
    preparation_operation_id TEXT NOT NULL,
    managed_run_id TEXT NOT NULL,
    workspace_lease_id TEXT NOT NULL,
    repository_id TEXT NOT NULL,
    worktree_path TEXT NOT NULL,
    head_revision TEXT NOT NULL,
    evidence_digest TEXT NOT NULL,
    pull_request_id TEXT NOT NULL,
    required_forge_checks_json TEXT NOT NULL,
    report_artifact_hash TEXT NOT NULL,
    stage TEXT NOT NULL,
    release_operation_id TEXT NOT NULL UNIQUE,
    released_at TEXT NOT NULL,
    snapshot_branch TEXT NOT NULL DEFAULT '',
    snapshot_head_revision TEXT NOT NULL DEFAULT '',
    snapshot_cleanliness TEXT NOT NULL DEFAULT '',
    delivery_truth_json TEXT NOT NULL DEFAULT '',
    host_released_at TEXT,
    removal_authorized_at TEXT,
    completed_at TEXT,
    state_version INTEGER NOT NULL,
    FOREIGN KEY(task_handle) REFERENCES tasks(handle)
);
CREATE INDEX task_cleanup_operations_stage_idx
ON task_cleanup_operations(stage, task_handle);
INSERT OR IGNORE INTO schema_migrations(version, applied_at)
VALUES (16, strftime('%Y-%m-%dT%H:%M:%fZ', 'now'));
`

// BeginTaskCleanup proves durable delivery and runtime settlement before
// moving one task into a restart-stable cleanup hold.
func (store *Store) BeginTaskCleanup(
	ctx context.Context,
	mutation application.TaskCleanupMutation,
) (application.TaskCleanupRecord, error) {
	if err := validateCleanupMutation(store, ctx, mutation); err != nil {
		return application.TaskCleanupRecord{}, err
	}
	transaction, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return application.TaskCleanupRecord{}, fmt.Errorf("begin task cleanup: %w", err)
	}
	defer func() { _ = transaction.Rollback() }()
	if existing, found, err := findTaskCleanupRecord(ctx, transaction, mutation.OperationID); err != nil {
		return application.TaskCleanupRecord{}, err
	} else if found {
		if existing.SubjectDigest != mutation.SubjectDigest || existing.TaskHandle != mutation.TaskHandle ||
			existing.ReleaseOperationID != mutation.ReleaseOperationID {
			return application.TaskCleanupRecord{}, fmt.Errorf("task cleanup altered replay: %w", application.ErrConflict)
		}
		return existing, nil
	}
	task, err := getTask(ctx, transaction, mutation.TaskHandle)
	if err != nil {
		return application.TaskCleanupRecord{}, err
	}
	if task.State != domain.TaskDelivered || mutation.At.Before(task.UpdatedAt) ||
		task.ManagedRunID == "" || task.WorkspaceLeaseID == "" {
		return application.TaskCleanupRecord{}, fmt.Errorf("task cleanup posture: %w", application.ErrPrecondition)
	}
	if err := proveCleanupDatabaseSafety(ctx, transaction, task); err != nil {
		return application.TaskCleanupRecord{}, err
	}
	preparationOperationID, worktreePath, err := cleanupPreparation(ctx, transaction, task.Handle)
	if err != nil {
		return application.TaskCleanupRecord{}, err
	}
	evidenceRow, sealed, err := cleanupEvidence(ctx, transaction, task)
	if err != nil {
		return application.TaskCleanupRecord{}, err
	}
	bundle := sealed.Bundle()
	record := application.TaskCleanupRecord{
		OperationID: mutation.OperationID, SubjectDigest: mutation.SubjectDigest,
		TaskHandle: task.Handle, PreparationOperationID: preparationOperationID,
		ManagedRunID: task.ManagedRunID, WorkspaceLeaseID: task.WorkspaceLeaseID,
		RepositoryID: task.RepositoryID, WorktreePath: worktreePath,
		HeadRevision: bundle.HeadRevision, EvidenceDigest: sealed.Digest(),
		RequiredForgeChecks: append([]string(nil), evidenceRow.requiredForgeChecks...),
		Stage:               application.CleanupPrepared, ReleaseOperationID: mutation.ReleaseOperationID,
		ReleasedAt: mutation.ReleasedAt,
	}
	if bundle.ForgeEvidence != nil {
		record.PullRequestID = bundle.ForgeEvidence.PullRequestID
	} else if bundle.ReportArtifact != nil {
		record.ReportArtifactHash = bundle.ReportArtifact.ContentHash
	} else {
		return application.TaskCleanupRecord{}, fmt.Errorf("task cleanup delivery evidence: %w", application.ErrPrecondition)
	}
	held, err := task.ApplyTransition(domain.TransitionCleanupStarted, mutation.At)
	if err != nil {
		return application.TaskCleanupRecord{}, fmt.Errorf("hold task cleanup: %w", err)
	}
	stateVersion, err := nextMutationStateVersion(ctx, transaction)
	if err != nil {
		return application.TaskCleanupRecord{}, err
	}
	held.StateVersion = stateVersion
	recordStateVersion := stateVersion
	if err := updateTaskState(ctx, transaction, held); err != nil {
		return application.TaskCleanupRecord{}, err
	}
	checks, err := json.Marshal(record.RequiredForgeChecks)
	if err != nil {
		return application.TaskCleanupRecord{}, errors.New("insert task cleanup: forge checks cannot be encoded")
	}
	const insert = `INSERT INTO task_cleanup_operations(
        operation_id, subject_digest, task_handle, preparation_operation_id,
        managed_run_id, workspace_lease_id, repository_id, worktree_path,
        head_revision, evidence_digest, pull_request_id, required_forge_checks_json,
        report_artifact_hash, stage, release_operation_id, released_at, state_version)
        VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`
	if _, err := transaction.ExecContext(ctx, insert,
		record.OperationID, record.SubjectDigest, record.TaskHandle, record.PreparationOperationID,
		record.ManagedRunID, record.WorkspaceLeaseID, record.RepositoryID, record.WorktreePath,
		record.HeadRevision, record.EvidenceDigest, record.PullRequestID, string(checks),
		record.ReportArtifactHash, record.Stage, record.ReleaseOperationID, formatTime(record.ReleasedAt),
		recordStateVersion,
	); isConstraintError(err) {
		return application.TaskCleanupRecord{}, fmt.Errorf("insert task cleanup: %w", application.ErrConflict)
	} else if err != nil {
		return application.TaskCleanupRecord{}, fmt.Errorf("insert task cleanup: %w", err)
	}
	if err := transaction.Commit(); err != nil {
		return application.TaskCleanupRecord{}, fmt.Errorf("commit task cleanup hold: %w", err)
	}
	return record, nil
}

// RecordTaskCleanupHostRelease persists the exact host acknowledgement and the
// safety proof that preceded it.
func (store *Store) RecordTaskCleanupHostRelease(
	ctx context.Context,
	mutation application.TaskCleanupHostReleaseMutation,
) (application.TaskCleanupRecord, error) {
	return store.advanceTaskCleanup(ctx, mutation.OperationID, mutation.SubjectDigest,
		application.CleanupPrepared, application.CleanupHostReleased,
		mutation.Snapshot, mutation.DeliveryTruth, &mutation.Receipt, mutation.At)
}

// AuthorizeTaskCleanupRemoval persists the fresh proof observed after host
// release and is the only stage from which Git removal is allowed.
func (store *Store) AuthorizeTaskCleanupRemoval(
	ctx context.Context,
	mutation application.TaskCleanupRemovalAuthorization,
) (application.TaskCleanupRecord, error) {
	return store.advanceTaskCleanup(ctx, mutation.OperationID, mutation.SubjectDigest,
		application.CleanupHostReleased, application.CleanupRemovalAuthorized,
		mutation.Snapshot, mutation.DeliveryTruth, nil, mutation.At)
}

func (store *Store) advanceTaskCleanup(
	ctx context.Context,
	operationID, subjectDigest string,
	from, to application.TaskCleanupStage,
	snapshot application.WorkspaceSnapshot,
	truth application.PullRequestDeliveryTruth,
	receipt *application.ManagedRunReleaseReceipt,
	at time.Time,
) (application.TaskCleanupRecord, error) {
	if store == nil || store.db == nil || ctx == nil || domain.ValidateOperationID(operationID) != nil ||
		len(subjectDigest) != 64 || snapshot.Validate() != nil || at.IsZero() || at.Location() != time.UTC {
		return application.TaskCleanupRecord{}, errors.New("advance task cleanup: input is invalid")
	}
	transaction, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return application.TaskCleanupRecord{}, fmt.Errorf("begin task cleanup stage: %w", err)
	}
	defer func() { _ = transaction.Rollback() }()
	record, found, err := findTaskCleanupRecord(ctx, transaction, operationID)
	if err != nil {
		return application.TaskCleanupRecord{}, err
	}
	if !found {
		return application.TaskCleanupRecord{}, fmt.Errorf("advance task cleanup: %w", application.ErrNotFound)
	}
	if record.SubjectDigest != subjectDigest {
		return application.TaskCleanupRecord{}, fmt.Errorf("advance task cleanup altered replay: %w", application.ErrConflict)
	}
	if record.Stage != from {
		if record.Stage == to && cleanupProofMatches(record, snapshot, truth) {
			return record, nil
		}
		return application.TaskCleanupRecord{}, fmt.Errorf("advance task cleanup stage: %w", application.ErrPrecondition)
	}
	if err := validateCleanupProof(record, snapshot, truth); err != nil {
		return application.TaskCleanupRecord{}, err
	}
	if receipt != nil && (receipt.ManagedRunID != record.ManagedRunID ||
		receipt.WorkspaceLeaseID != record.WorkspaceLeaseID ||
		receipt.Disposition != application.ManagedRunReleaseReapSafe ||
		!receipt.ReleasedAt.Equal(record.ReleasedAt) || receipt.State != application.ManagedRunReleased) {
		return application.TaskCleanupRecord{}, fmt.Errorf("task cleanup host release differs: %w", application.ErrPrecondition)
	}
	task, err := getTask(ctx, transaction, record.TaskHandle)
	if err != nil {
		return application.TaskCleanupRecord{}, err
	}
	if task.State != domain.TaskCleanupHeld || at.Before(task.UpdatedAt) {
		return application.TaskCleanupRecord{}, fmt.Errorf("advance task cleanup posture: %w", application.ErrPrecondition)
	}
	stateVersion, err := nextMutationStateVersion(ctx, transaction)
	if err != nil {
		return application.TaskCleanupRecord{}, err
	}
	task.StateVersion = stateVersion
	task.UpdatedAt = at
	if err := updateTaskState(ctx, transaction, task); err != nil {
		return application.TaskCleanupRecord{}, err
	}
	encodedTruth, err := json.Marshal(truth)
	if err != nil {
		return application.TaskCleanupRecord{}, errors.New("advance task cleanup: delivery truth cannot be encoded")
	}
	timeColumn := "host_released_at"
	if to == application.CleanupRemovalAuthorized {
		timeColumn = "removal_authorized_at"
	}
	statement := `UPDATE task_cleanup_operations SET
        stage = ?, snapshot_branch = ?, snapshot_head_revision = ?, snapshot_cleanliness = ?,
        delivery_truth_json = ?, ` + timeColumn + ` = ?, state_version = ?
        WHERE operation_id = ? AND stage = ?`
	result, err := transaction.ExecContext(ctx, statement, to, snapshot.Branch, snapshot.HeadRevision,
		snapshot.Cleanliness, string(encodedTruth), formatTime(at), stateVersion, operationID, from)
	if err != nil {
		return application.TaskCleanupRecord{}, fmt.Errorf("advance task cleanup: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil || rows != 1 {
		return application.TaskCleanupRecord{}, errors.New("advance task cleanup: exact stage was not updated")
	}
	if err := transaction.Commit(); err != nil {
		return application.TaskCleanupRecord{}, fmt.Errorf("commit task cleanup stage: %w", err)
	}
	record.Stage = to
	record.Snapshot = snapshot
	return record, nil
}

// CompleteTaskCleanup marks cleaned only after the removal-authorized adapter
// call has converged, then clears all released host authority references.
func (store *Store) CompleteTaskCleanup(
	ctx context.Context,
	completion application.TaskCleanupCompletion,
) (application.MutationResult, error) {
	if store == nil || store.db == nil || ctx == nil || domain.ValidateOperationID(completion.OperationID) != nil ||
		len(completion.SubjectDigest) != 64 || completion.At.IsZero() || completion.At.Location() != time.UTC {
		return application.MutationResult{}, errors.New("complete task cleanup: input is invalid")
	}
	transaction, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return application.MutationResult{}, fmt.Errorf("begin task cleanup completion: %w", err)
	}
	defer func() { _ = transaction.Rollback() }()
	if replay, found, err := mutationReplay(ctx, transaction, completion.OperationID, commandCleanupTask, completion.SubjectDigest); err != nil {
		return application.MutationResult{}, commitReplayConflict(transaction, err)
	} else if found {
		return replayResult(ctx, transaction, replay)
	}
	record, found, err := findTaskCleanupRecord(ctx, transaction, completion.OperationID)
	if err != nil {
		return application.MutationResult{}, err
	}
	if !found {
		return application.MutationResult{}, fmt.Errorf("complete task cleanup: %w", application.ErrNotFound)
	}
	if record.SubjectDigest != completion.SubjectDigest {
		return application.MutationResult{}, fmt.Errorf("complete task cleanup altered replay: %w", application.ErrConflict)
	}
	if record.Stage != application.CleanupRemovalAuthorized {
		return application.MutationResult{}, fmt.Errorf("complete task cleanup stage: %w", application.ErrPrecondition)
	}
	task, err := getTask(ctx, transaction, record.TaskHandle)
	if err != nil {
		return application.MutationResult{}, err
	}
	if task.State != domain.TaskCleanupHeld || completion.At.Before(task.UpdatedAt) {
		return application.MutationResult{}, fmt.Errorf("complete task cleanup posture: %w", application.ErrPrecondition)
	}
	cleaned, err := task.ApplyTransition(domain.TransitionCleanupAccepted, completion.At)
	if err != nil {
		return application.MutationResult{}, fmt.Errorf("complete task cleanup transition: %w", err)
	}
	cleaned.ManagedRunID = ""
	cleaned.WorkspaceLeaseID = ""
	cleaned.ExecutionAttachmentID = ""
	cleaned.AttachmentTargetName = ""
	stateVersion, err := nextMutationStateVersion(ctx, transaction)
	if err != nil {
		return application.MutationResult{}, err
	}
	cleaned.StateVersion = stateVersion
	if err := cleaned.Validate(); err != nil {
		return application.MutationResult{}, fmt.Errorf("validate cleaned task: %w", err)
	}
	if err := updateTaskState(ctx, transaction, cleaned); err != nil {
		return application.MutationResult{}, err
	}
	operation := completedMutationOperation(completion.OperationID, commandCleanupTask,
		completion.SubjectDigest, cleaned.Handle, stateVersion, completion.At)
	if err := insertOperation(ctx, transaction, operation); err != nil {
		return application.MutationResult{}, fmt.Errorf("insert task cleanup operation: %w", err)
	}
	const update = `UPDATE task_cleanup_operations SET stage = ?, completed_at = ?, state_version = ?
        WHERE operation_id = ? AND stage = ?`
	result, err := transaction.ExecContext(ctx, update, application.CleanupCompleted, formatTime(completion.At),
		stateVersion, completion.OperationID, application.CleanupRemovalAuthorized)
	if err != nil {
		return application.MutationResult{}, fmt.Errorf("complete task cleanup record: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil || rows != 1 {
		return application.MutationResult{}, errors.New("complete task cleanup record: exact stage was not updated")
	}
	if err := transaction.Commit(); err != nil {
		return application.MutationResult{}, fmt.Errorf("commit task cleanup completion: %w", err)
	}
	return application.MutationResult{Task: cleaned, Operation: operation}, nil
}
