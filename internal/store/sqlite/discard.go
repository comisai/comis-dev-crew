package sqlite

import (
	"context"
	"fmt"
	"time"

	"github.com/comisai/comis-dev-crew/internal/application"
	"github.com/comisai/comis-dev-crew/internal/domain"
)

// The discard flag rides the cleanup operation record because a discard is a
// cleanup whose safety was proven a different way. Recording which proof was
// used is what lets an auditor tell a removal backed by delivered work from one
// backed only by an operator saying so.
const taskDiscardMigration = `
ALTER TABLE task_cleanup_operations ADD COLUMN discard INTEGER NOT NULL DEFAULT 0;
INSERT OR IGNORE INTO schema_migrations(version, applied_at)
VALUES (27, strftime('%Y-%m-%dT%H:%M:%fZ', 'now'));
`

// BeginTaskDiscard holds one settled, undelivered task for removal.
//
// Cancellation preserves work deliberately, and cleanup requires delivery
// evidence a cancelled task will never have — so without this the worktree,
// lease and run binding of every cancelled task stay held with nothing able to
// release them. This is the way out, and it refuses anything that still has work
// in flight: only a task the service has already settled can be discarded.
func (store *Store) BeginTaskDiscard(
	ctx context.Context,
	mutation application.TaskDiscardMutation,
) (application.TaskCleanupRecord, error) {
	if err := store.ready(ctx); err != nil {
		return application.TaskCleanupRecord{}, err
	}
	if domain.ValidateOperationID(mutation.OperationID) != nil ||
		domain.ValidateTaskHandle(mutation.TaskHandle) != nil ||
		len(mutation.SubjectDigest) != 64 || mutation.At.Location() != time.UTC {
		return application.TaskCleanupRecord{}, fmt.Errorf("task discard mutation: %w", application.ErrPrecondition)
	}
	transaction, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return application.TaskCleanupRecord{}, fmt.Errorf("begin task discard: %w", err)
	}
	defer func() { _ = transaction.Rollback() }()
	if existing, found, err := findTaskCleanupRecord(ctx, transaction, mutation.OperationID); err != nil {
		return application.TaskCleanupRecord{}, err
	} else if found {
		if existing.SubjectDigest != mutation.SubjectDigest || existing.TaskHandle != mutation.TaskHandle {
			return application.TaskCleanupRecord{}, fmt.Errorf("task discard altered replay: %w", application.ErrConflict)
		}
		return existing, nil
	}
	task, err := getTask(ctx, transaction, mutation.TaskHandle)
	if err != nil {
		return application.TaskCleanupRecord{}, err
	}
	// Only work the service has already stopped may be discarded. A task still
	// running owns its worktree, and a delivered one has a delivery to prove
	// removal against — that is cleanup's path, with its own evidence gate.
	if task.State != domain.TaskCancelled && task.State != domain.TaskFailed {
		return application.TaskCleanupRecord{}, fmt.Errorf("task discard posture: %w", application.ErrPrecondition)
	}
	if task.ManagedRunID == "" || task.WorkspaceLeaseID == "" || mutation.At.Before(task.UpdatedAt) {
		return application.TaskCleanupRecord{}, fmt.Errorf("task discard authority: %w", application.ErrPrecondition)
	}
	if err := proveNothingIsStillRunning(ctx, transaction, task.Handle, "task discard", false); err != nil {
		return application.TaskCleanupRecord{}, err
	}
	preparationOperationID, worktreePath, err := cleanupPreparation(ctx, transaction, task.Handle)
	if err != nil {
		return application.TaskCleanupRecord{}, err
	}
	held, err := task.ApplyTransition(domain.TransitionCleanupStarted, mutation.At)
	if err != nil {
		return application.TaskCleanupRecord{}, fmt.Errorf("hold task discard: %w", err)
	}
	stateVersion, err := nextMutationStateVersion(ctx, transaction)
	if err != nil {
		return application.TaskCleanupRecord{}, err
	}
	held.StateVersion = stateVersion
	if err := updateTaskState(ctx, transaction, held); err != nil {
		return application.TaskCleanupRecord{}, err
	}
	record := application.TaskCleanupRecord{
		OperationID: mutation.OperationID, SubjectDigest: mutation.SubjectDigest,
		TaskHandle: task.Handle, PreparationOperationID: preparationOperationID,
		ManagedRunID: task.ManagedRunID, WorkspaceLeaseID: task.WorkspaceLeaseID,
		RepositoryID: task.RepositoryID, WorktreePath: worktreePath,
		Stage: application.CleanupPrepared, ReleaseOperationID: mutation.ReleaseOperationID,
		ReleasedAt: mutation.ReleasedAt, Discard: true,
	}
	const insert = `INSERT INTO task_cleanup_operations(
        operation_id, subject_digest, task_handle, preparation_operation_id,
        managed_run_id, workspace_lease_id, repository_id, worktree_path,
        head_revision, evidence_digest, pull_request_id, required_forge_checks_json,
        report_artifact_hash, stage, release_operation_id, released_at, state_version, discard)
        VALUES (?, ?, ?, ?, ?, ?, ?, ?, '', '', '', '[]', '', ?, ?, ?, ?, 1)`
	if _, err := transaction.ExecContext(ctx, insert,
		record.OperationID, record.SubjectDigest, record.TaskHandle, record.PreparationOperationID,
		record.ManagedRunID, record.WorkspaceLeaseID, record.RepositoryID, record.WorktreePath,
		record.Stage, record.ReleaseOperationID, formatTime(record.ReleasedAt), stateVersion,
	); isConstraintError(err) {
		return application.TaskCleanupRecord{}, fmt.Errorf("insert task discard: %w", application.ErrConflict)
	} else if err != nil {
		return application.TaskCleanupRecord{}, fmt.Errorf("insert task discard: %w", err)
	}
	if err := transaction.Commit(); err != nil {
		return application.TaskCleanupRecord{}, fmt.Errorf("commit task discard hold: %w", err)
	}
	return record, nil
}
