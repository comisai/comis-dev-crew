package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/comisai/comis-dev-crew/internal/application"
	"github.com/comisai/comis-dev-crew/internal/domain"
)

const taskCandidateReconciliationMigration = `
CREATE TABLE task_candidate_reconciliations (
    operation_id TEXT PRIMARY KEY,
    task_handle TEXT NOT NULL,
    action TEXT NOT NULL,
    preparation_operation_id TEXT NOT NULL,
    repository_id TEXT NOT NULL,
    worktree_path TEXT NOT NULL,
    branch TEXT NOT NULL,
    base_revision TEXT NOT NULL,
    head_revision TEXT NOT NULL,
    cleanliness TEXT NOT NULL,
    terminal_session_id TEXT NOT NULL,
    terminal_transition TEXT NOT NULL,
    terminal_observed_at TEXT NOT NULL,
    observed_at TEXT NOT NULL,
    started_state_version INTEGER NOT NULL,
    completed_state_version INTEGER NOT NULL,
    FOREIGN KEY(operation_id) REFERENCES operations(id),
    FOREIGN KEY(task_handle) REFERENCES tasks(handle),
    FOREIGN KEY(preparation_operation_id) REFERENCES operations(id)
);
CREATE INDEX task_candidate_reconciliations_task_version_idx
ON task_candidate_reconciliations(task_handle, completed_state_version DESC, operation_id);
INSERT OR IGNORE INTO schema_migrations(version, applied_at)
VALUES (17, strftime('%Y-%m-%dT%H:%M:%fZ', 'now'));
`

const commandReconcileTask = "ReconcileTask"

// ReadTaskReconciliationAuthority returns one consistent durable snapshot of
// task, preparation, preparation operation, and terminal settlement identity.
func (store *Store) ReadTaskReconciliationAuthority(
	ctx context.Context,
	taskHandle string,
) (application.TaskReconciliationAuthority, error) {
	if domain.ValidateTaskHandle(taskHandle) != nil {
		return application.TaskReconciliationAuthority{}, errors.New("read task reconciliation authority: invalid task")
	}
	transaction, err := store.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return application.TaskReconciliationAuthority{}, fmt.Errorf("begin task reconciliation authority read: %w", err)
	}
	defer func() { _ = transaction.Rollback() }()
	authority, err := readTaskReconciliationAuthority(ctx, transaction, taskHandle)
	if err != nil {
		return application.TaskReconciliationAuthority{}, err
	}
	if err := transaction.Commit(); err != nil {
		return application.TaskReconciliationAuthority{}, fmt.Errorf("commit task reconciliation authority read: %w", err)
	}
	return authority, nil
}

// CommitTaskCandidateReconciliation atomically records fresh recovery
// evidence and advances unknown through reconciling into normal validation.
// It does not insert or advance a worker report.
func (store *Store) CommitTaskCandidateReconciliation(
	ctx context.Context,
	mutation application.TaskCandidateReconciliationMutation,
) (application.MutationResult, error) {
	if err := validateTaskCandidateReconciliationMutation(mutation); err != nil {
		return application.MutationResult{}, err
	}
	transaction, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return application.MutationResult{}, fmt.Errorf("begin task candidate reconciliation: %w", err)
	}
	defer func() { _ = transaction.Rollback() }()
	if replay, found, err := mutationReplay(
		ctx, transaction, mutation.OperationID, commandReconcileTask, mutation.SubjectDigest,
	); err != nil {
		return application.MutationResult{}, commitReplayConflict(transaction, err)
	} else if found {
		return replayResult(ctx, transaction, replay)
	}
	authority, err := readTaskReconciliationAuthority(ctx, transaction, mutation.TaskHandle)
	if err != nil {
		return application.MutationResult{}, err
	}
	if err := verifyTaskCandidateReconciliationAuthority(authority, mutation); err != nil {
		return application.MutationResult{}, err
	}
	recoveryHistory, err := candidateRecoveryHistoryExists(ctx, transaction, mutation.TaskHandle)
	if err != nil {
		return application.MutationResult{}, err
	}
	if recoveryHistory {
		return application.MutationResult{}, fmt.Errorf("task candidate reconciliation already exists: %w", application.ErrPrecondition)
	}
	var activeValidationProcesses int
	if err := transaction.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM validation_processes WHERE task_handle = ? AND state NOT IN ('exited', 'absent')",
		mutation.TaskHandle,
	).Scan(&activeValidationProcesses); err != nil {
		return application.MutationResult{}, fmt.Errorf("inspect task reconciliation validation processes: %w", err)
	}
	if activeValidationProcesses != 0 {
		return application.MutationResult{}, fmt.Errorf("task reconciliation validation process remains: %w", application.ErrPrecondition)
	}
	var unresolvedDecisions int
	if err := transaction.QueryRowContext(ctx, `SELECT COUNT(*) FROM reports AS decision
		WHERE decision.task_handle = ? AND decision.kind = 'decision'
		AND NOT EXISTS (
			SELECT 1 FROM reports AS resolution
			WHERE resolution.task_handle = decision.task_handle
			AND resolution.kind = 'resolution'
			AND resolution.external_key = decision.external_key
		)`, mutation.TaskHandle).Scan(&unresolvedDecisions); err != nil {
		return application.MutationResult{}, fmt.Errorf("inspect task reconciliation decisions: %w", err)
	}
	if unresolvedDecisions != 0 {
		return application.MutationResult{}, fmt.Errorf("task reconciliation decision remains: %w", application.ErrPrecondition)
	}

	reconciling, err := authority.Task.ApplyTransition(domain.TransitionReconcileRequired, mutation.At)
	if err != nil {
		return application.MutationResult{}, fmt.Errorf("start task candidate reconciliation: %w", err)
	}
	startedVersion, err := nextMutationStateVersion(ctx, transaction)
	if err != nil {
		return application.MutationResult{}, err
	}
	if startedVersion == math.MaxInt64 {
		return application.MutationResult{}, errors.New("task candidate reconciliation state version is exhausted")
	}
	reconciling.StateVersion = startedVersion
	validating, err := reconciling.ApplyTransition(domain.TransitionReconciledValidating, mutation.At)
	if err != nil {
		return application.MutationResult{}, fmt.Errorf("complete task candidate reconciliation: %w", err)
	}
	validating.StateVersion = startedVersion + 1
	if err := updateTaskState(ctx, transaction, validating); err != nil {
		return application.MutationResult{}, err
	}
	operation := completedMutationOperation(
		mutation.OperationID, commandReconcileTask, mutation.SubjectDigest,
		validating.Handle, validating.StateVersion, mutation.At,
	)
	if err := insertOperation(ctx, transaction, operation); err != nil {
		return application.MutationResult{}, taskReconciliationConstraintFailure("insert task reconciliation operation", err)
	}
	const insertEvidence = `INSERT INTO task_candidate_reconciliations (
		operation_id, task_handle, action, preparation_operation_id,
		repository_id, worktree_path, branch, base_revision, head_revision,
		cleanliness, terminal_session_id, terminal_transition,
		terminal_observed_at, observed_at, started_state_version, completed_state_version
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`
	if _, err := transaction.ExecContext(ctx, insertEvidence,
		mutation.OperationID, mutation.TaskHandle, mutation.Action, mutation.PreparationOperationID,
		mutation.Snapshot.RepositoryID, mutation.Snapshot.WorktreePath, mutation.Snapshot.Branch,
		authority.Task.BaseRevision, mutation.Snapshot.HeadRevision, mutation.Snapshot.Cleanliness,
		mutation.TerminalSessionID, mutation.TerminalTransition, formatTime(mutation.TerminalObservedAt),
		formatTime(mutation.At), startedVersion, validating.StateVersion,
	); err != nil {
		return application.MutationResult{}, taskReconciliationConstraintFailure("insert task reconciliation evidence", err)
	}
	if err := transaction.Commit(); err != nil {
		return application.MutationResult{}, fmt.Errorf("commit task candidate reconciliation: %w", err)
	}
	return application.MutationResult{Task: validating, Operation: operation}, nil
}

func readTaskReconciliationAuthority(
	ctx context.Context,
	source queryer,
	taskHandle string,
) (application.TaskReconciliationAuthority, error) {
	refused, err := runtimeRelayIdentityRefusalExists(ctx, source, taskHandle)
	if err != nil {
		return application.TaskReconciliationAuthority{}, err
	}
	if refused {
		return application.TaskReconciliationAuthority{}, fmt.Errorf("task reconciliation relay authority is unproven: %w", application.ErrPrecondition)
	}
	task, err := getTask(ctx, source, taskHandle)
	if err != nil {
		return application.TaskReconciliationAuthority{}, err
	}
	preparation, err := getManagedRunPreparation(ctx, source, task)
	if err != nil {
		return application.TaskReconciliationAuthority{}, fmt.Errorf("read task reconciliation preparation: %w", err)
	}
	preparationOperationID, err := taskPreparationOperationID(ctx, source, task.Handle)
	if err != nil {
		return application.TaskReconciliationAuthority{}, err
	}
	binding, found, err := findTerminalBinding(ctx, source, task.Handle)
	if err != nil {
		return application.TaskReconciliationAuthority{}, err
	}
	if !found || binding.managedRunID != task.ManagedRunID || binding.workspaceLeaseID != task.WorkspaceLeaseID {
		return application.TaskReconciliationAuthority{}, fmt.Errorf("task reconciliation terminal binding is unavailable: %w", application.ErrPrecondition)
	}
	return application.TaskReconciliationAuthority{
		Task: task, Preparation: preparation, PreparationOperationID: preparationOperationID,
		TerminalSessionID: binding.terminalSessionID, TerminalTransition: binding.latestTransition,
		TerminalObservedAt: binding.updatedAt,
	}, nil
}

func taskPreparationOperationID(ctx context.Context, source queryer, taskHandle string) (string, error) {
	const query = `SELECT id FROM operations
		WHERE command = ? AND status = ? AND result_ref = ? ORDER BY id`
	rows, err := source.QueryContext(ctx, query, commandPrepareTask, domain.OperationCompleted, taskHandle)
	if err != nil {
		return "", fmt.Errorf("inspect task reconciliation preparation operation: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var operationID string
	count := 0
	for rows.Next() {
		if err := rows.Scan(&operationID); err != nil {
			return "", fmt.Errorf("scan task reconciliation preparation operation: %w", err)
		}
		count++
	}
	if err := rows.Err(); err != nil {
		return "", fmt.Errorf("inspect task reconciliation preparation operation: %w", err)
	}
	if count != 1 || domain.ValidateOperationID(operationID) != nil {
		return "", fmt.Errorf("task reconciliation preparation operation is ambiguous: %w", application.ErrPrecondition)
	}
	return operationID, nil
}

func verifyTaskCandidateReconciliationAuthority(
	authority application.TaskReconciliationAuthority,
	mutation application.TaskCandidateReconciliationMutation,
) error {
	task := authority.Task
	if task.State != domain.TaskUnknown || task.StateVersion != mutation.ExpectedTaskVersion ||
		mutation.At.Before(task.UpdatedAt) || authority.Preparation.State != application.PreparationOpen ||
		authority.PreparationOperationID != mutation.PreparationOperationID ||
		mutation.Snapshot.TaskHandle != task.Handle || mutation.Snapshot.RepositoryID != task.RepositoryID ||
		mutation.Snapshot.WorktreePath != authority.Preparation.RequestedWorkspaceRoot ||
		mutation.Snapshot.Cleanliness != application.WorkspaceClean ||
		mutation.Snapshot.HeadRevision == task.BaseRevision ||
		authority.TerminalSessionID != mutation.TerminalSessionID ||
		authority.TerminalTransition != mutation.TerminalTransition ||
		!authority.TerminalObservedAt.Equal(mutation.TerminalObservedAt) ||
		(authority.TerminalTransition != application.TerminalExited && authority.TerminalTransition != application.TerminalReleased) {
		return fmt.Errorf("task candidate reconciliation authority differs: %w", application.ErrPrecondition)
	}
	return nil
}

func validateTaskCandidateReconciliationMutation(mutation application.TaskCandidateReconciliationMutation) error {
	if domain.ValidateOperationID(mutation.OperationID) != nil || len(mutation.SubjectDigest) != 64 ||
		domain.ValidateTaskHandle(mutation.TaskHandle) != nil ||
		mutation.Action != application.ReconcileValidateCleanCandidate ||
		domain.ValidateOperationID(mutation.PreparationOperationID) != nil ||
		mutation.Snapshot.Validate() != nil ||
		domain.ValidateAuthorityReference("terminalSessionId", mutation.TerminalSessionID) != nil ||
		(mutation.TerminalTransition != application.TerminalExited && mutation.TerminalTransition != application.TerminalReleased) ||
		mutation.TerminalObservedAt.IsZero() || mutation.TerminalObservedAt.Location() != time.UTC ||
		mutation.ExpectedTaskVersion < 1 || mutation.At.IsZero() || mutation.At.Location() != time.UTC ||
		mutation.At.Before(mutation.TerminalObservedAt) {
		return errors.New("commit task candidate reconciliation: invalid mutation")
	}
	return nil
}

func taskReconciliationConstraintFailure(action string, err error) error {
	if isConstraintError(err) {
		return fmt.Errorf("%s: %w", action, application.ErrConflict)
	}
	return fmt.Errorf("%s: %w", action, err)
}
