package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/comisai/comis-dev-crew/internal/application"
	"github.com/comisai/comis-dev-crew/internal/domain"
)

const taskResumeLaunchMigration = `
ALTER TABLE task_launch_acknowledgements RENAME TO task_launch_acknowledgements_previous;
CREATE TABLE task_launch_acknowledgements (
    operation_id TEXT PRIMARY KEY,
    task_handle TEXT NOT NULL,
    managed_run_id TEXT NOT NULL,
    workspace_lease_id TEXT NOT NULL,
    working_directory TEXT NOT NULL,
    brief_revision INTEGER NOT NULL,
    brief_revision_hash TEXT NOT NULL,
    launch_state_version INTEGER NOT NULL,
    acknowledged_at TEXT NOT NULL,
    FOREIGN KEY(operation_id) REFERENCES operations(id),
    FOREIGN KEY(task_handle) REFERENCES tasks(handle)
);
INSERT INTO task_launch_acknowledgements (
    operation_id, task_handle, managed_run_id, workspace_lease_id,
    working_directory, brief_revision, brief_revision_hash,
    launch_state_version, acknowledged_at
)
SELECT operation_id, task_handle, managed_run_id, workspace_lease_id,
    working_directory, brief_revision, brief_revision_hash, 0, acknowledged_at
FROM task_launch_acknowledgements_previous;
DROP TABLE task_launch_acknowledgements_previous;
CREATE INDEX task_launch_acknowledgements_generation_idx
ON task_launch_acknowledgements(task_handle, launch_state_version, operation_id);
CREATE TABLE task_resume_launches (
    operation_id TEXT PRIMARY KEY,
    task_handle TEXT NOT NULL,
    head_revision TEXT NOT NULL,
    observed_at TEXT NOT NULL,
    state_version INTEGER NOT NULL,
    FOREIGN KEY(operation_id) REFERENCES operations(id),
    FOREIGN KEY(task_handle) REFERENCES tasks(handle)
);
CREATE INDEX task_resume_launches_task_idx
ON task_resume_launches(task_handle, state_version DESC, operation_id);
INSERT OR IGNORE INTO schema_migrations(version, applied_at)
VALUES (43, strftime('%Y-%m-%dT%H:%M:%fZ', 'now'));
`

// CommitTaskResume records one paused task as ready for a new authenticated
// generation of its existing worker profile.
//
// The caller has already proven the worktree is exactly as the worker left it.
// That proof is the whole precondition: resuming the same worker onto a tree
// someone edited would continue from a brief and an evidence set that describe a
// different tree, and the worker would have no way to notice. When the tree did
// change, the operator's path is handback, which revalidates.
func (store *Store) CommitTaskResume(
	ctx context.Context,
	mutation application.TaskResumeMutation,
) (application.MutationResult, error) {
	return commitTaskMutation(ctx, store, taskMutationSpec{
		Command:     commandResumeTask,
		OperationID: mutation.OperationID, SubjectDigest: mutation.SubjectDigest,
		At: mutation.At, Label: "task resume",
		Record: func(ctx context.Context, transaction *sql.Tx, persisted domain.Task) error {
			const insert = `INSERT INTO task_resume_launches (
                operation_id, task_handle, head_revision, observed_at, state_version
            ) VALUES (?, ?, ?, ?, ?)`
			if _, err := transaction.ExecContext(ctx, insert,
				mutation.OperationID, persisted.Handle, mutation.ObservedHeadRevision,
				formatTime(mutation.At), persisted.StateVersion,
			); err != nil {
				return fmt.Errorf("insert task resume launch: %w", err)
			}
			return nil
		},
	}, func(ctx context.Context, transaction *sql.Tx) (domain.Task, error) {
		task, err := getTask(ctx, transaction, mutation.TaskHandle)
		if err != nil {
			return domain.Task{}, err
		}
		if mutation.At.Before(task.UpdatedAt) {
			return domain.Task{}, fmt.Errorf("task resume time: %w", application.ErrPrecondition)
		}
		if task.State != domain.TaskPaused {
			return domain.Task{}, fmt.Errorf("task resume state: %w", application.ErrPrecondition)
		}
		// The head observed at resume is recorded against the task the caller
		// inspected. A resume committed against a task whose brief moved on since
		// the inspection would be resuming onto state the caller never saw.
		if mutation.ObservedHeadRevision == "" ||
			domain.ValidateGitRevision(mutation.ObservedHeadRevision) != nil {
			return domain.Task{}, fmt.Errorf("task resume head: %w", application.ErrPrecondition)
		}
		if err := proveNothingIsStillRunning(ctx, transaction, task, "task resume", true); err != nil {
			return domain.Task{}, err
		}
		updated, err := task.ApplyTransition(domain.TransitionResumed, mutation.At)
		if err != nil {
			return domain.Task{}, fmt.Errorf("apply task resume: %w", err)
		}
		// No pause request is cleared here. The only route into the paused state
		// is a worker's own paused report, which clears the request it answers in
		// the same transaction, so a paused task never carries one. Clearing
		// again would be an unreachable branch asserting an invariant that
		// belongs to the report path.
		return updated, nil
	})
}

// TaskResumeLaunch returns the latest durable resume generation. Callers still
// compare its state version to the current task before selecting a bootstrap.
func (store *Store) TaskResumeLaunch(
	ctx context.Context,
	taskHandle string,
) (application.TaskResumeLaunch, bool, error) {
	if err := store.ready(ctx); err != nil {
		return application.TaskResumeLaunch{}, false, err
	}
	const query = `SELECT operation_id, task_handle, head_revision, state_version
        FROM task_resume_launches WHERE task_handle = ?
        ORDER BY state_version DESC, operation_id LIMIT 1`
	var launch application.TaskResumeLaunch
	err := store.db.QueryRowContext(ctx, query, taskHandle).Scan(
		&launch.OperationID, &launch.TaskHandle, &launch.HeadRevision, &launch.StateVersion,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return application.TaskResumeLaunch{}, false, nil
	}
	if err != nil {
		return application.TaskResumeLaunch{}, false, fmt.Errorf("read task resume launch: %w", err)
	}
	return launch, true, nil
}
