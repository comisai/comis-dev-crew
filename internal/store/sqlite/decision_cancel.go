package sqlite

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/comisai/comis-dev-crew/internal/application"
	"github.com/comisai/comis-dev-crew/internal/domain"
)

const commandCancelDecision = "CancelDecision"

// A withdrawn question is closed without inventing a worker report. Recording it
// as a resolution would attribute to the worker an answer nobody gave, and the
// two facts are audited differently: a resolution says the work can proceed, a
// cancellation says the question no longer applies.
const decisionCancellationMigration = `
CREATE TABLE task_decision_cancellations (
    task_handle TEXT NOT NULL,
    external_key TEXT NOT NULL,
    operation_id TEXT NOT NULL,
    cancelled_at TEXT NOT NULL,
    PRIMARY KEY(task_handle, external_key),
    FOREIGN KEY(task_handle) REFERENCES tasks(handle)
);
INSERT OR IGNORE INTO schema_migrations(version, applied_at)
VALUES (31, strftime('%Y-%m-%dT%H:%M:%fZ', 'now'));
`

// decisionStillOpenClause is the single definition of a question still awaiting
// a human, correlated against the given decision-report alias.
//
// It exists once because cleanup safety, the re-surfacing cadence, the operator
// inventory, candidate reconciliation and the evidence view all consult it. When
// each carried its own copy, adding a way to close a question meant changing five
// predicates in step or leaving gates that disagree — a question the human
// withdrew that still blocks cleanup is one nobody can ever close.
//
// The alias is a compile-time constant chosen by this package, never caller
// input.
func decisionStillOpenClause(alias string) string {
	return `NOT EXISTS (
        SELECT 1 FROM reports resolution
        WHERE resolution.task_handle = ` + alias + `.task_handle
          AND resolution.kind = 'resolution' AND resolution.external_key = ` + alias + `.external_key
    ) AND NOT EXISTS (
        SELECT 1 FROM task_decision_cancellations cancellation
        WHERE cancellation.task_handle = ` + alias + `.task_handle
          AND cancellation.external_key = ` + alias + `.external_key
    )`
}

// CommitDecisionCancellation withdraws one open question.
//
// Only a question that exists and is still open can be withdrawn: cancelling an
// unknown or already-settled key is refused rather than recorded as though it had
// an effect an operator could rely on. It reuses the shared task-mutation
// discipline, so replay, conflict auditing and the durable operation record
// behave exactly as they do for every other mutation.
func (store *Store) CommitDecisionCancellation(
	ctx context.Context,
	mutation application.DecisionCancellationMutation,
) (application.MutationResult, error) {
	if domain.ValidateDecisionKey(mutation.ExternalKey) != nil {
		return application.MutationResult{}, fmt.Errorf("decision cancellation key: %w", application.ErrPrecondition)
	}
	return commitTaskMutation(ctx, store, taskMutationSpec{
		Command:     commandCancelDecision,
		OperationID: mutation.OperationID, SubjectDigest: mutation.SubjectDigest,
		At: mutation.At, Label: "decision cancellation",
		Record: func(ctx context.Context, transaction *sql.Tx, persisted domain.Task) error {
			if _, err := transaction.ExecContext(ctx,
				`INSERT INTO task_decision_cancellations(
                    task_handle, external_key, operation_id, cancelled_at
                ) VALUES (?, ?, ?, ?)`,
				persisted.Handle, mutation.ExternalKey, mutation.OperationID, formatTime(mutation.At),
			); err != nil {
				return fmt.Errorf("record decision cancellation: %w", err)
			}
			// A withdrawal is a transition an operator watching the fleet must
			// see, and it closes the same open state a resolution would.
			return appendServiceEvent(ctx, transaction, application.ServiceEvent{
				OccurredAt: mutation.At, Kind: application.EventDecisionResolved,
				TaskHandle: persisted.Handle, Reason: mutation.ExternalKey,
				StateVersion: persisted.StateVersion,
			})
		},
	}, func(ctx context.Context, transaction *sql.Tx) (domain.Task, error) {
		task, err := getTask(ctx, transaction, mutation.TaskHandle)
		if err != nil {
			return domain.Task{}, err
		}
		open, err := decisionIsOpen(ctx, transaction, mutation.TaskHandle, mutation.ExternalKey)
		if err != nil {
			return domain.Task{}, err
		}
		if !open {
			return domain.Task{}, fmt.Errorf("decision cancellation target: %w", application.ErrPrecondition)
		}
		return task, nil
	})
}

func decisionIsOpen(ctx context.Context, transaction *sql.Tx, taskHandle, externalKey string) (bool, error) {
	var open int
	query := `SELECT COUNT(*) FROM reports d
        WHERE d.task_handle = ? AND d.external_key = ? AND d.kind = 'decision' AND ` +
		decisionStillOpenClause("d")
	if err := transaction.QueryRowContext(ctx, query, taskHandle, externalKey).Scan(&open); err != nil {
		return false, fmt.Errorf("inspect open decision: %w", err)
	}
	return open != 0, nil
}
