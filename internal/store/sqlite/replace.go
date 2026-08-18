package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/comisai/comis-dev-crew/internal/application"
	"github.com/comisai/comis-dev-crew/internal/domain"
)

// A replacement is its own event, not a variant of handback. It records which
// worker was swapped for which and the exact tree state at the moment of the
// swap, because "the previous worker stopped here and a different one continued"
// is the fact an operator needs afterwards and neither task state nor the
// handback trail carries it.
const taskReplacementMigration = `
CREATE TABLE task_replacements (
    operation_id TEXT PRIMARY KEY,
    task_handle TEXT NOT NULL,
    previous_worker_profile_id TEXT NOT NULL,
    worker_profile_id TEXT NOT NULL,
    repository_id TEXT NOT NULL,
    worktree_path TEXT NOT NULL,
    branch TEXT NOT NULL,
    head_revision TEXT NOT NULL,
    cleanliness TEXT NOT NULL,
    brief_revision INTEGER NOT NULL,
    observed_at TEXT NOT NULL,
    state_version INTEGER NOT NULL,
    FOREIGN KEY(operation_id) REFERENCES operations(id),
    FOREIGN KEY(task_handle) REFERENCES tasks(handle)
);
CREATE INDEX task_replacements_task_idx ON task_replacements(task_handle, state_version);
INSERT OR IGNORE INTO schema_migrations(version, applied_at)
VALUES (25, strftime('%Y-%m-%dT%H:%M:%fZ', 'now'));
`

// CommitTaskReplace preserves one task's work and readies it for a fresh worker.
//
// The worktree is untouched. Replacement is for the case where the work is worth
// keeping and the worker is not — a harness that wedged, a profile that turned
// out wrong — so discarding the tree would destroy the thing being preserved.
//
// The task passes through reconciliation on its way back to ready, which is what
// the word means here: the fresh head and dirty state are captured, the brief is
// re-pinned at a new revision, and exactly one worker generation becomes
// launchable. Evidence produced under the old brief is superseded by
// state version rather than deleted, so the trail of what the previous worker
// actually did survives the swap.
func (store *Store) CommitTaskReplace(
	ctx context.Context,
	mutation application.TaskReplaceMutation,
) (application.MutationResult, error) {
	if err := validateTaskReplaceMutation(mutation); err != nil {
		return application.MutationResult{}, err
	}
	var previousWorkerProfileID string
	return commitTaskMutation(ctx, store, taskMutationSpec{
		Command:     commandReplaceWorker,
		OperationID: mutation.OperationID, SubjectDigest: mutation.SubjectDigest,
		At: mutation.At, Label: "worker replacement",
		Record: func(ctx context.Context, transaction *sql.Tx, persisted domain.Task) error {
			// The shared state writer deliberately persists only the transition.
			// Replacement is the one command that also rewrites the brief and the
			// worker, so it says so here rather than widening a writer every other
			// command uses — a widened writer would let any command change who
			// runs a task without naming that as its purpose.
			if err := updateTaskBriefAndWorker(ctx, transaction, persisted); err != nil {
				return err
			}
			return recordTaskReplacement(
				ctx, transaction, mutation, previousWorkerProfileID,
				persisted.BriefRevision, persisted.StateVersion,
			)
		},
	}, func(ctx context.Context, transaction *sql.Tx) (domain.Task, error) {
		task, err := getTask(ctx, transaction, mutation.TaskHandle)
		if err != nil {
			return domain.Task{}, err
		}
		previousWorkerProfileID = task.WorkerProfileID
		if err := proveReplacementSafety(ctx, transaction, task, mutation); err != nil {
			return domain.Task{}, err
		}
		reconciling, err := task.ApplyTransition(domain.TransitionReconcileRequired, mutation.At)
		if err != nil {
			return domain.Task{}, fmt.Errorf("reconcile task for replacement: %w", err)
		}
		ready, err := reconciling.ApplyTransition(domain.TransitionReconciledReady, mutation.At)
		if err != nil {
			return domain.Task{}, fmt.Errorf("ready task for replacement: %w", err)
		}
		// A fresh brief revision is what makes the replacement one generation
		// rather than a second worker joining the first: the previous worker's
		// reports name a revision that is no longer current and are refused.
		ready.WorkerProfileID = mutation.WorkerProfileID
		ready.BriefRevision = task.BriefRevision + 1
		ready, err = ready.PinBriefRevision()
		if err != nil {
			return domain.Task{}, fmt.Errorf("pin replacement brief: %w", err)
		}
		return ready, nil
	})
}

// proveReplacementSafety refuses a swap while anything is still running. A
// worker or validation process that outlives the replacement would keep writing
// into a worktree the new worker has been told it owns.
func proveReplacementSafety(
	ctx context.Context,
	transaction *sql.Tx,
	task domain.Task,
	mutation application.TaskReplaceMutation,
) error {
	if task.State != domain.TaskPaused || mutation.At.Before(task.UpdatedAt) {
		return fmt.Errorf("worker replacement state: %w", application.ErrPrecondition)
	}
	preparation, err := getManagedRunPreparation(ctx, transaction, task)
	if err != nil {
		return fmt.Errorf("read worker replacement workspace: %w", err)
	}
	if mutation.Snapshot.TaskHandle != task.Handle ||
		mutation.Snapshot.RepositoryID != task.RepositoryID ||
		mutation.Snapshot.WorktreePath != preparation.RequestedWorkspaceRoot {
		return fmt.Errorf("worker replacement authority differs: %w", application.ErrPrecondition)
	}
	return proveNothingIsStillRunning(ctx, transaction, task.Handle, "worker replacement", true)
}

// proveNothingIsStillRunning refuses while anything still owns the worktree.
//
// A worker whose terminal never settled may still be alive, and a validation
// process reads and writes the same tree. Every command that takes a worktree
// away from whoever had it needs exactly this proof, and a second copy of it is
// a second place for one of the two conditions to be forgotten.
//
// requireBinding separates two different absences. Handback and replacement act
// on a paused task, which is paused because a worker reported — so no binding at
// all means the durable record is inconsistent and the command must refuse.
// Discard acts on settled work that may have been cancelled before it ever
// launched, where no binding means nothing ever ran, which is the safest state
// there is. Treating those alike would make undelivered work unremovable in
// exactly the case where removing it is most obviously safe.
func proveNothingIsStillRunning(
	ctx context.Context,
	transaction *sql.Tx,
	taskHandle string,
	label string,
	requireBinding bool,
) error {
	binding, found, err := findTerminalBinding(ctx, transaction, taskHandle)
	if err != nil {
		return err
	}
	if !found && requireBinding {
		return fmt.Errorf("%s terminal is unsettled: %w", label, application.ErrPrecondition)
	}
	if found && binding.latestTransition != application.TerminalExited &&
		binding.latestTransition != application.TerminalReleased {
		return fmt.Errorf("%s terminal is unsettled: %w", label, application.ErrPrecondition)
	}
	var activeProcesses int
	if err := transaction.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM validation_processes WHERE task_handle = ? AND state NOT IN ('exited', 'absent')",
		taskHandle,
	).Scan(&activeProcesses); err != nil {
		return fmt.Errorf("inspect %s validation processes: %w", label, err)
	}
	if activeProcesses != 0 {
		return fmt.Errorf("%s validation process remains: %w", label, application.ErrPrecondition)
	}
	return nil
}

// updateTaskBriefAndWorker persists the fields a replacement rewrites. It is
// separate from the shared state writer because changing who runs a task is a
// distinct authority from moving it between states.
func updateTaskBriefAndWorker(ctx context.Context, transaction *sql.Tx, task domain.Task) error {
	const update = `UPDATE tasks SET worker_profile_id = ?, brief_revision = ?, brief_revision_hash = ?
        WHERE handle = ?`
	result, err := transaction.ExecContext(ctx, update,
		task.WorkerProfileID, task.BriefRevision, task.BriefRevisionHash, task.Handle)
	if err != nil {
		return fmt.Errorf("update task brief and worker: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil || rows != 1 {
		return errors.New("update task brief and worker: exact task was not updated")
	}
	return nil
}

// recordTaskReplacement writes the durable trail of one swap.
func recordTaskReplacement(
	ctx context.Context,
	transaction *sql.Tx,
	mutation application.TaskReplaceMutation,
	previousWorkerProfileID string,
	briefRevision int64,
	stateVersion int64,
) error {
	const insert = `INSERT INTO task_replacements (
        operation_id, task_handle, previous_worker_profile_id, worker_profile_id,
        repository_id, worktree_path, branch, head_revision, cleanliness,
        brief_revision, observed_at, state_version
    ) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`
	if _, err := transaction.ExecContext(ctx, insert,
		mutation.OperationID, mutation.TaskHandle, previousWorkerProfileID, mutation.WorkerProfileID,
		mutation.Snapshot.RepositoryID, mutation.Snapshot.WorktreePath, mutation.Snapshot.Branch,
		mutation.Snapshot.HeadRevision, mutation.Snapshot.Cleanliness,
		briefRevision, formatTime(mutation.At), stateVersion,
	); err != nil {
		return fmt.Errorf("insert worker replacement: %w", err)
	}
	return nil
}

func validateTaskReplaceMutation(mutation application.TaskReplaceMutation) error {
	if domain.ValidateOperationID(mutation.OperationID) != nil || len(mutation.SubjectDigest) != 64 ||
		domain.ValidateTaskHandle(mutation.TaskHandle) != nil ||
		domain.ValidateAuthorityReference("workerProfileId", mutation.WorkerProfileID) != nil ||
		mutation.Snapshot.Validate() != nil || mutation.At.IsZero() {
		return errors.New("commit worker replacement: invalid mutation")
	}
	return nil
}

// TaskReplacement reports the most recent worker swap for one task.
func (store *Store) TaskReplacement(
	ctx context.Context,
	taskHandle string,
) (application.TaskReplacementRecord, bool, error) {
	if err := store.ready(ctx); err != nil {
		return application.TaskReplacementRecord{}, false, err
	}
	const query = `SELECT operation_id, task_handle, previous_worker_profile_id, worker_profile_id,
        head_revision, cleanliness, brief_revision, observed_at, state_version
    FROM task_replacements WHERE task_handle = ?
    ORDER BY state_version DESC LIMIT 1`
	var record application.TaskReplacementRecord
	var observedAt string
	err := store.db.QueryRowContext(ctx, query, taskHandle).Scan(
		&record.OperationID, &record.TaskHandle, &record.PreviousWorkerProfileID,
		&record.WorkerProfileID, &record.HeadRevision, &record.Cleanliness,
		&record.BriefRevision, &observedAt, &record.StateVersion,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return application.TaskReplacementRecord{}, false, nil
	}
	if err != nil {
		return application.TaskReplacementRecord{}, false, fmt.Errorf("read worker replacement: %w", err)
	}
	parsed, err := parseTime(observedAt)
	if err != nil {
		return application.TaskReplacementRecord{}, false, fmt.Errorf("read worker replacement time: %w", err)
	}
	record.ObservedAt = parsed
	return record, true, nil
}
