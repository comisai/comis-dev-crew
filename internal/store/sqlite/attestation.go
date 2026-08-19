package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/comisai/comis-dev-crew/internal/application"
	"github.com/comisai/comis-dev-crew/internal/domain"
)

// One row per task, replaced by a later inventory of the same surface: an
// attestation is the current judgement, not an append-only trail, and a stale
// one must never outvote a fresher look. The absence of a row is meaningful on
// its own — it says nobody has inventoried this scout yet, which is why the
// finding is stored explicitly instead of being implied by an empty key list.
const scoutAttestationMigration = `
CREATE TABLE scout_decision_attestations (
    task_handle TEXT PRIMARY KEY,
    operation_id TEXT NOT NULL,
    finding TEXT NOT NULL,
    open_decision_keys TEXT NOT NULL,
    attested_at TEXT NOT NULL,
    state_version INTEGER NOT NULL,
    FOREIGN KEY(operation_id) REFERENCES operations(id),
    FOREIGN KEY(task_handle) REFERENCES tasks(handle)
);
INSERT OR IGNORE INTO schema_migrations(version, applied_at)
VALUES (28, strftime('%Y-%m-%dT%H:%M:%fZ', 'now'));
`

// CommitScoutDecisionAttestation records one liaison inventory.
//
// It does not move the task. Inventorying a scout's open questions observes the
// work; it does not finish, deliver, or retire it, and a state change here would
// claim a transition nobody performed.
func (store *Store) CommitScoutDecisionAttestation(
	ctx context.Context,
	mutation application.ScoutDecisionAttestationMutation,
) (application.MutationResult, error) {
	encodedKeys, err := json.Marshal(append([]string(nil), mutation.OpenDecisionKeys...))
	if err != nil {
		return application.MutationResult{}, fmt.Errorf("encode attested inventory: %w", application.ErrPrecondition)
	}
	return commitTaskMutation(ctx, store, taskMutationSpec{
		Command:     commandAttestScoutDecisions,
		OperationID: mutation.OperationID, SubjectDigest: mutation.SubjectDigest,
		At: mutation.At, Label: "scout decision attestation",
		Record: func(ctx context.Context, transaction *sql.Tx, persisted domain.Task) error {
			if _, err := transaction.ExecContext(ctx,
				`INSERT INTO scout_decision_attestations(
                    task_handle, operation_id, finding, open_decision_keys, attested_at, state_version
                ) VALUES (?, ?, ?, ?, ?, ?)
                ON CONFLICT(task_handle) DO UPDATE SET
                    operation_id = excluded.operation_id,
                    finding = excluded.finding,
                    open_decision_keys = excluded.open_decision_keys,
                    attested_at = excluded.attested_at,
                    state_version = excluded.state_version`,
				persisted.Handle, mutation.OperationID, string(mutation.Finding),
				string(encodedKeys), formatTime(mutation.At), persisted.StateVersion,
			); err != nil {
				return fmt.Errorf("record scout decision attestation: %w", err)
			}
			return nil
		},
	}, func(ctx context.Context, transaction *sql.Tx) (domain.Task, error) {
		task, err := getTask(ctx, transaction, mutation.TaskHandle)
		if err != nil {
			return domain.Task{}, err
		}
		// Only a scout carries an investigation surface to inventory. A ship
		// task's open decisions are already governed by the ordinary decision
		// inventory and the cleanup gate that reads it.
		if task.Shape != domain.ShapeScout {
			return domain.Task{}, fmt.Errorf("attestation applies to scouts only: %w", application.ErrPrecondition)
		}
		return task, nil
	})
}

// ReadScoutDecisionInventory returns the current recorded inventory, and
// whether one exists at all. The two are reported separately because "nobody
// looked" and "somebody looked and found nothing" are different facts.
func (store *Store) ReadScoutDecisionInventory(
	ctx context.Context,
	taskHandle string,
) (application.ScoutDecisionInventory, bool, error) {
	if ctx == nil {
		return application.ScoutDecisionInventory{}, false, errors.New("read scout decision inventory: context is required")
	}
	if err := ctx.Err(); err != nil {
		return application.ScoutDecisionInventory{}, false, err
	}
	var finding, encodedKeys, attestedAt string
	err := store.db.QueryRowContext(ctx,
		`SELECT finding, open_decision_keys, attested_at
         FROM scout_decision_attestations WHERE task_handle = ?`, taskHandle,
	).Scan(&finding, &encodedKeys, &attestedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return application.ScoutDecisionInventory{}, false, nil
	}
	if err != nil {
		return application.ScoutDecisionInventory{}, false, fmt.Errorf("read scout decision attestation: %w", err)
	}
	var keys []string
	if err := json.Unmarshal([]byte(encodedKeys), &keys); err != nil {
		return application.ScoutDecisionInventory{}, false, fmt.Errorf("decode attested inventory: %w", err)
	}
	observed, err := parseTime(attestedAt)
	if err != nil {
		return application.ScoutDecisionInventory{}, false, fmt.Errorf("decode attestation time: %w", err)
	}
	return application.ScoutDecisionInventory{
		TaskHandle: taskHandle, Finding: application.ScoutAttestationFinding(finding),
		OpenDecisionKeys: keys, AttestedAt: observed,
	}, true, nil
}
