package sqlite

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/comisai/comis-dev-crew/internal/application"
	"github.com/comisai/comis-dev-crew/internal/domain"
)

const taskHandbackMigration = `
CREATE TABLE task_handbacks (
    operation_id TEXT PRIMARY KEY,
    task_handle TEXT NOT NULL,
    action TEXT NOT NULL,
    repository_id TEXT NOT NULL,
    worktree_path TEXT NOT NULL,
    branch TEXT NOT NULL,
    head_revision TEXT NOT NULL,
    cleanliness TEXT NOT NULL,
    observed_at TEXT NOT NULL,
    state_version INTEGER NOT NULL,
    FOREIGN KEY(operation_id) REFERENCES operations(id),
    FOREIGN KEY(task_handle) REFERENCES tasks(handle)
);
CREATE INDEX task_handbacks_task_version_idx
ON task_handbacks(task_handle, state_version DESC, operation_id);
INSERT OR IGNORE INTO schema_migrations(version, applied_at)
VALUES (15, strftime('%Y-%m-%dT%H:%M:%fZ', 'now'));
`

const commandHandbackTask = "HandbackTask"

// CommitTaskHandback captures one fresh Git snapshot and starts normal
// validation only after a safe paused worker has released its terminal.
func (store *Store) CommitTaskHandback(
	ctx context.Context,
	mutation application.TaskHandbackMutation,
) (application.MutationResult, error) {
	if err := validateTaskHandbackMutation(mutation); err != nil {
		return application.MutationResult{}, err
	}
	transaction, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return application.MutationResult{}, fmt.Errorf("begin task handback: %w", err)
	}
	defer func() { _ = transaction.Rollback() }()
	if replay, found, err := mutationReplay(ctx, transaction, mutation.OperationID, commandHandbackTask, mutation.SubjectDigest); err != nil {
		return application.MutationResult{}, commitReplayConflict(transaction, err)
	} else if found {
		return replayResult(ctx, transaction, replay)
	}
	task, err := getTask(ctx, transaction, mutation.TaskHandle)
	if err != nil {
		return application.MutationResult{}, err
	}
	preparation, err := getManagedRunPreparation(ctx, transaction, task)
	if err != nil {
		return application.MutationResult{}, fmt.Errorf("read task handback workspace: %w", err)
	}
	binding, found, err := findTerminalBinding(ctx, transaction, task.Handle)
	if err != nil {
		return application.MutationResult{}, err
	}
	terminalSettled := found && (binding.latestTransition == application.TerminalExited || binding.latestTransition == application.TerminalReleased)
	if task.State != domain.TaskPaused || mutation.At.Before(task.UpdatedAt) ||
		mutation.Snapshot.TaskHandle != task.Handle || mutation.Snapshot.RepositoryID != task.RepositoryID ||
		mutation.Snapshot.WorktreePath != preparation.RequestedWorkspaceRoot || !terminalSettled {
		return application.MutationResult{}, fmt.Errorf("task handback authority differs: %w", application.ErrPrecondition)
	}
	var activeProcesses int
	if err := transaction.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM validation_processes WHERE task_handle = ? AND state <> 'exited'", task.Handle,
	).Scan(&activeProcesses); err != nil {
		return application.MutationResult{}, fmt.Errorf("inspect task handback validation processes: %w", err)
	}
	if activeProcesses != 0 {
		return application.MutationResult{}, fmt.Errorf("task handback validation process remains: %w", application.ErrPrecondition)
	}
	updated, err := task.AcceptWorkerReport(mutation.CandidateReport, mutation.At)
	if err != nil {
		return application.MutationResult{}, fmt.Errorf("apply task handback: %w", err)
	}
	stateVersion, err := nextMutationStateVersion(ctx, transaction)
	if err != nil {
		return application.MutationResult{}, err
	}
	updated.StateVersion = stateVersion
	if err := updateReportedTask(ctx, transaction, updated); err != nil {
		return application.MutationResult{}, err
	}
	accepted := domain.AcceptedReport{
		TaskHandle: task.Handle, Report: mutation.CandidateReport,
		SubjectDigest: mutation.CandidateReportDigest, StateVersion: stateVersion, AcceptedAt: mutation.At,
	}
	if err := insertAcceptedReport(ctx, transaction, accepted); err != nil {
		return application.MutationResult{}, err
	}
	if err := insertComisReport(ctx, transaction, task, accepted); err != nil {
		return application.MutationResult{}, err
	}
	operation := completedMutationOperation(
		mutation.OperationID, commandHandbackTask, mutation.SubjectDigest,
		updated.Handle, stateVersion, mutation.At,
	)
	if err := insertOperation(ctx, transaction, operation); err != nil {
		return application.MutationResult{}, fmt.Errorf("insert task handback operation: %w", err)
	}
	const insert = `INSERT INTO task_handbacks (
        operation_id, task_handle, action, repository_id, worktree_path,
        branch, head_revision, cleanliness, observed_at, state_version
    ) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`
	if _, err := transaction.ExecContext(ctx, insert,
		mutation.OperationID, task.Handle, mutation.Action, mutation.Snapshot.RepositoryID,
		mutation.Snapshot.WorktreePath, mutation.Snapshot.Branch, mutation.Snapshot.HeadRevision,
		mutation.Snapshot.Cleanliness, formatTime(mutation.At), stateVersion,
	); err != nil {
		return application.MutationResult{}, fmt.Errorf("insert task handback snapshot: %w", err)
	}
	if err := transaction.Commit(); err != nil {
		return application.MutationResult{}, fmt.Errorf("commit task handback: %w", err)
	}
	return application.MutationResult{Task: updated, Operation: operation}, nil
}

func validateTaskHandbackMutation(mutation application.TaskHandbackMutation) error {
	if domain.ValidateOperationID(mutation.OperationID) != nil || len(mutation.SubjectDigest) != 64 ||
		domain.ValidateTaskHandle(mutation.TaskHandle) != nil || mutation.Action != application.HandbackValidateDeveloperWork ||
		mutation.Snapshot.Validate() != nil || mutation.CandidateReport.Validate() != nil ||
		mutation.CandidateReport.LocalReportID != mutation.OperationID ||
		mutation.CandidateReport.Kind != domain.ReportCandidateComplete ||
		len(mutation.CandidateReportDigest) != 64 ||
		mutation.At.IsZero() || mutation.At.Location() != time.UTC {
		return errors.New("commit task handback: invalid mutation")
	}
	return nil
}
