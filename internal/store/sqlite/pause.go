package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/comisai/comis-dev-crew/internal/application"
	"github.com/comisai/comis-dev-crew/internal/domain"
)

// A pause is a standing request, not a state. The task keeps its own state
// until the worker settles and reports, so the request is recorded beside the
// task rather than written into it.
const taskPauseRequestMigration = `
ALTER TABLE tasks ADD COLUMN pause_requested_operation_id TEXT NOT NULL DEFAULT '';
ALTER TABLE tasks ADD COLUMN pause_requested_at TEXT NOT NULL DEFAULT '';
INSERT OR IGNORE INTO schema_migrations(version, applied_at)
VALUES (23, strftime('%Y-%m-%dT%H:%M:%fZ', 'now'));
`

// CommitTaskPauseRequest records that an operator asked one task's worker to
// settle at a safe boundary.
//
// It deliberately does not transition the task. The worker is still holding the
// worktree when the request lands; writing "paused" here would make the durable
// state disagree with what is actually running, and the operator who then edits
// the worktree would be editing under a live worker. The worker's own paused
// report is the only thing that settles the state, and this request is what it
// reads to know it should send one.
func (store *Store) CommitTaskPauseRequest(
	ctx context.Context,
	mutation application.TaskPauseRequestMutation,
) (application.MutationResult, error) {
	return commitTaskMutation(ctx, store, taskMutationSpec{
		Command:     commandPauseTask,
		OperationID: mutation.OperationID, SubjectDigest: mutation.SubjectDigest,
		At: mutation.At, Label: "task pause request",
	}, func(ctx context.Context, transaction *sql.Tx) (domain.Task, error) {
		task, err := getTask(ctx, transaction, mutation.TaskHandle)
		if err != nil {
			return domain.Task{}, err
		}
		if mutation.At.Location() != time.UTC {
			return domain.Task{}, fmt.Errorf("task pause request time: %w", application.ErrPrecondition)
		}
		// Asking a settled task to settle cannot be honoured: no worker remains
		// to reach a boundary, and a standing request nothing will ever clear
		// would read as a pause that is still pending.
		if !taskHoldsAWorker(task.State) {
			return domain.Task{}, fmt.Errorf("task pause request state: %w", application.ErrPrecondition)
		}
		if _, err := transaction.ExecContext(ctx,
			`UPDATE tasks SET pause_requested_operation_id = ?, pause_requested_at = ? WHERE handle = ?`,
			mutation.OperationID, mutation.At.UTC().Format(time.RFC3339Nano), task.Handle,
		); err != nil {
			return domain.Task{}, fmt.Errorf("record task pause request: %w", err)
		}
		return task, nil
	})
}

// PauseRequest reports whether one task has an unsettled pause request. The
// worker reads it through its own report receipt; nothing else may act on it.
func (store *Store) PauseRequest(ctx context.Context, taskHandle string) (bool, error) {
	if ctx == nil {
		return false, fmt.Errorf("read task pause request: %w", application.ErrPrecondition)
	}
	var operationID string
	err := store.db.QueryRowContext(ctx,
		`SELECT pause_requested_operation_id FROM tasks WHERE handle = ?`, taskHandle,
	).Scan(&operationID)
	if err != nil {
		return false, fmt.Errorf("read task pause request: %w", err)
	}
	return operationID != "", nil
}

// A task holds a worker while it is between an accepted launch and a settled
// outcome. Only those states have something that can reach a safe boundary.
func taskHoldsAWorker(state domain.TaskState) bool {
	switch state {
	case domain.TaskLaunching, domain.TaskWorking, domain.TaskAwaitingDecision, domain.TaskBlocked:
		return true
	default:
		return false
	}
}
