package sqlite

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/comisai/comis-dev-crew/internal/application"
	"github.com/comisai/comis-dev-crew/internal/domain"
)

// Steering instructions are queued, not overwritten. An operator who sends two
// instructions meant both of them; keeping only the newest would silently drop
// the first, and the operator would have no way to tell it never arrived.
const taskSteeringMigration = `
CREATE TABLE task_steering_instructions (
    operation_id TEXT PRIMARY KEY,
    task_handle TEXT NOT NULL,
    instruction TEXT NOT NULL,
    queued_at TEXT NOT NULL,
    delivered_at TEXT NOT NULL DEFAULT '',
    state_version INTEGER NOT NULL,
    FOREIGN KEY(operation_id) REFERENCES operations(id),
    FOREIGN KEY(task_handle) REFERENCES tasks(handle)
);
CREATE INDEX task_steering_pending_idx
    ON task_steering_instructions(task_handle, delivered_at, state_version);
INSERT OR IGNORE INTO schema_migrations(version, applied_at)
VALUES (26, strftime('%Y-%m-%dT%H:%M:%fZ', 'now'));
`

// CommitTaskSteer queues one bounded instruction for a task's current worker.
//
// It does not move the task. Steering says something to a worker that is still
// working; a state change would claim the instruction had already had an effect
// nobody has observed. The worker reads it through its own report receipt, so an
// instruction reaches it at a boundary the worker chose.
func (store *Store) CommitTaskSteer(
	ctx context.Context,
	mutation application.TaskSteerMutation,
) (application.MutationResult, error) {
	if domain.ValidateSteeringInstruction(mutation.Instruction) != nil {
		return application.MutationResult{}, fmt.Errorf("task steer instruction: %w", application.ErrPrecondition)
	}
	return commitTaskMutation(ctx, store, taskMutationSpec{
		Command:     commandSteerTask,
		OperationID: mutation.OperationID, SubjectDigest: mutation.SubjectDigest,
		At: mutation.At, Label: "task steer",
		Record: func(ctx context.Context, transaction *sql.Tx, persisted domain.Task) error {
			if _, err := transaction.ExecContext(ctx,
				`INSERT INTO task_steering_instructions(
                    operation_id, task_handle, instruction, queued_at, state_version
                ) VALUES (?, ?, ?, ?, ?)`,
				mutation.OperationID, persisted.Handle, mutation.Instruction,
				formatTime(mutation.At), persisted.StateVersion,
			); err != nil {
				return fmt.Errorf("queue task steering instruction: %w", err)
			}
			return nil
		},
	}, func(ctx context.Context, transaction *sql.Tx) (domain.Task, error) {
		task, err := getTask(ctx, transaction, mutation.TaskHandle)
		if err != nil {
			return domain.Task{}, err
		}
		// An instruction for a task with no worker can never be read. Queuing it
		// would leave an operator believing they had said something to someone.
		if !taskHoldsAWorker(task.State) {
			return domain.Task{}, fmt.Errorf("task steer state: %w", application.ErrPrecondition)
		}
		return task, nil
	})
}

// consumeSteeringInstruction returns the oldest instruction this task's worker
// has not seen and marks it delivered in the same transaction that records the
// report carrying it. Delivering inside that transaction is what stops one
// instruction reaching a worker twice.
func consumeSteeringInstruction(
	ctx context.Context,
	transaction *sql.Tx,
	taskHandle string,
	deliveredAt string,
) (string, error) {
	var operationID, instruction string
	err := transaction.QueryRowContext(ctx,
		`SELECT operation_id, instruction FROM task_steering_instructions
         WHERE task_handle = ? AND delivered_at = ''
         ORDER BY state_version, operation_id LIMIT 1`, taskHandle,
	).Scan(&operationID, &instruction)
	if err == sql.ErrNoRows {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("read task steering instruction: %w", err)
	}
	if _, err := transaction.ExecContext(ctx,
		`UPDATE task_steering_instructions SET delivered_at = ? WHERE operation_id = ?`,
		deliveredAt, operationID,
	); err != nil {
		return "", fmt.Errorf("mark task steering instruction delivered: %w", err)
	}
	return instruction, nil
}

// PendingSteeringInstructions counts what one task's worker has not yet read.
func (store *Store) PendingSteeringInstructions(
	ctx context.Context,
	taskHandle string,
) (int, error) {
	if err := store.ready(ctx); err != nil {
		return 0, err
	}
	var pending int
	if err := store.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM task_steering_instructions WHERE task_handle = ? AND delivered_at = ''`,
		taskHandle,
	).Scan(&pending); err != nil {
		return 0, fmt.Errorf("count task steering instructions: %w", err)
	}
	return pending, nil
}
