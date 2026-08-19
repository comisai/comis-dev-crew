package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/comisai/comis-dev-crew/internal/application"
	"github.com/comisai/comis-dev-crew/internal/domain"
)

const commandRespondDecision = "RespondDecision"

// An answer is stored beside the question rather than as a worker report. A
// report would attribute to the worker a conclusion it has not reached; the two
// are audited differently, because an answer says the human replied while a
// resolution says the work can proceed.
const decisionResponseMigration = `
CREATE TABLE task_decision_responses (
    task_handle TEXT NOT NULL,
    external_key TEXT NOT NULL,
    operation_id TEXT NOT NULL,
    response TEXT NOT NULL,
    responded_at TEXT NOT NULL,
    PRIMARY KEY(task_handle, external_key),
    FOREIGN KEY(task_handle) REFERENCES tasks(handle)
);
INSERT OR IGNORE INTO schema_migrations(version, applied_at)
VALUES (32, strftime('%Y-%m-%dT%H:%M:%fZ', 'now'));
`

// decisionAwaitingHumanClause is the single definition of a question that still
// owes the human a prompt, correlated against the given decision-report alias.
//
// It is deliberately narrower than decisionStillOpenClause. An answered question
// is no longer awaiting anybody — the human replied — yet it stays open until the
// worker applies that answer, so completion gates keep waiting for it while the
// asking cadence stops. Keeping the two clauses separate, and each defined once,
// is what lets those gates disagree on purpose instead of by accident.
func decisionAwaitingHumanClause(alias string) string {
	return decisionStillOpenClause(alias) + ` AND NOT EXISTS (
        SELECT 1 FROM task_decision_responses response
        WHERE response.task_handle = ` + alias + `.task_handle
          AND response.external_key = ` + alias + `.external_key
    )`
}

// CommitDecisionResponse records one operator answer to an open question.
//
// Only a question that exists and is still open can be answered: answering an
// unknown or already-settled key is refused rather than stored as an answer an
// operator could believe reached a worker. It reuses the shared task-mutation
// discipline, so replay, conflict auditing and the durable operation record
// behave exactly as they do for every other mutation.
func (store *Store) CommitDecisionResponse(
	ctx context.Context,
	mutation application.DecisionResponseMutation,
) (application.MutationResult, error) {
	if domain.ValidateDecisionKey(mutation.ExternalKey) != nil {
		return application.MutationResult{}, fmt.Errorf("decision response key: %w", application.ErrPrecondition)
	}
	if err := domain.ValidateDecisionResponse(mutation.Response); err != nil {
		return application.MutationResult{}, fmt.Errorf("decision response body: %w", application.ErrPrecondition)
	}
	return commitTaskMutation(ctx, store, taskMutationSpec{
		Command:     commandRespondDecision,
		OperationID: mutation.OperationID, SubjectDigest: mutation.SubjectDigest,
		At: mutation.At, Label: "decision response",
		Record: func(ctx context.Context, transaction *sql.Tx, persisted domain.Task) error {
			if _, err := transaction.ExecContext(ctx,
				`INSERT INTO task_decision_responses(
                    task_handle, external_key, operation_id, response, responded_at
                ) VALUES (?, ?, ?, ?, ?)`,
				persisted.Handle, mutation.ExternalKey, mutation.OperationID,
				mutation.Response, formatTime(mutation.At),
			); err != nil {
				return fmt.Errorf("record decision response: %w", err)
			}
			// The event is content-free: it names the question answered, never
			// the answer, which stays private task detail.
			return appendServiceEvent(ctx, transaction, application.ServiceEvent{
				OccurredAt: mutation.At, Kind: application.EventDecisionAnswered,
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
			return domain.Task{}, fmt.Errorf("decision response target: %w", application.ErrPrecondition)
		}
		answered, err := decisionIsAnswered(ctx, transaction, mutation.TaskHandle, mutation.ExternalKey)
		if err != nil {
			return domain.Task{}, err
		}
		if answered {
			return domain.Task{}, fmt.Errorf("decision response target: %w", application.ErrPrecondition)
		}
		return task, nil
	})
}

// ReadDecisionResponse returns the stored answer for one question, if the human
// has given one.
func (store *Store) ReadDecisionResponse(
	ctx context.Context,
	taskHandle string,
	externalKey string,
) (application.DecisionResponse, bool, error) {
	if ctx == nil {
		return application.DecisionResponse{}, false, errors.New("read decision response: context is required")
	}
	if err := ctx.Err(); err != nil {
		return application.DecisionResponse{}, false, err
	}
	response := application.DecisionResponse{TaskHandle: taskHandle, ExternalKey: externalKey}
	var respondedAt string
	err := store.db.QueryRowContext(ctx,
		`SELECT response, operation_id, responded_at FROM task_decision_responses
         WHERE task_handle = ? AND external_key = ?`,
		taskHandle, externalKey,
	).Scan(&response.Response, &response.OperationID, &respondedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return application.DecisionResponse{}, false, nil
	}
	if err != nil {
		return application.DecisionResponse{}, false, fmt.Errorf("read decision response: %w", err)
	}
	at, err := parseTime(respondedAt)
	if err != nil {
		return application.DecisionResponse{}, false, fmt.Errorf("decode decision response time: %w", err)
	}
	response.RespondedAt = at
	return response, true, nil
}

// ReadDecisionResponseForManagedRun returns the stored answer for one question
// asked by one managed run.
//
// The worker holds its managed run rather than the task handle, and the run is
// what proves the question is its own: correlating on it keeps one task's answer
// from being served to another.
func (store *Store) ReadDecisionResponseForManagedRun(
	ctx context.Context,
	managedRunID string,
	externalKey string,
) (application.DecisionResponse, bool, error) {
	if ctx == nil {
		return application.DecisionResponse{}, false, errors.New("read decision response: context is required")
	}
	if err := ctx.Err(); err != nil {
		return application.DecisionResponse{}, false, err
	}
	if managedRunID == "" {
		return application.DecisionResponse{}, false, errors.New("read decision response: managed run is required")
	}
	response := application.DecisionResponse{ExternalKey: externalKey}
	var respondedAt string
	err := store.db.QueryRowContext(ctx,
		`SELECT r.task_handle, r.response, r.operation_id, r.responded_at
         FROM task_decision_responses r
         JOIN tasks t ON t.handle = r.task_handle
         WHERE t.managed_run_id = ? AND r.external_key = ?`,
		managedRunID, externalKey,
	).Scan(&response.TaskHandle, &response.Response, &response.OperationID, &respondedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return application.DecisionResponse{}, false, nil
	}
	if err != nil {
		return application.DecisionResponse{}, false, fmt.Errorf("read decision response: %w", err)
	}
	at, err := parseTime(respondedAt)
	if err != nil {
		return application.DecisionResponse{}, false, fmt.Errorf("decode decision response time: %w", err)
	}
	response.RespondedAt = at
	return response, true, nil
}

func decisionIsAnswered(ctx context.Context, transaction *sql.Tx, taskHandle, externalKey string) (bool, error) {
	var answered int
	if err := transaction.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM task_decision_responses WHERE task_handle = ? AND external_key = ?`,
		taskHandle, externalKey,
	).Scan(&answered); err != nil {
		return false, fmt.Errorf("inspect answered decision: %w", err)
	}
	return answered != 0, nil
}
