package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/comisai/comis-dev-crew/internal/application"
	"github.com/comisai/comis-dev-crew/internal/domain"
)

const taskMergeMigration = `
CREATE TABLE task_merges (
    operation_id TEXT PRIMARY KEY,
    subject_digest TEXT NOT NULL,
    task_handle TEXT NOT NULL,
    managed_run_id TEXT NOT NULL,
    repository_id TEXT NOT NULL,
    pull_request_id TEXT NOT NULL,
	branch TEXT NOT NULL,
	head_revision TEXT NOT NULL,
	evidence_digest TEXT NOT NULL,
	evidence_expires_at TEXT NOT NULL,
	required_checks_json TEXT NOT NULL,
    state TEXT NOT NULL,
    approval_request_id TEXT NOT NULL,
    mcp_operation_id TEXT NOT NULL,
    resolving_principal_id TEXT NOT NULL,
    operation_fingerprint TEXT NOT NULL,
    approved_at TEXT NOT NULL,
    expires_at TEXT NOT NULL,
    consumed_at TEXT NOT NULL,
    merge_commit_revision TEXT NOT NULL,
    merge_method TEXT NOT NULL,
    reserved_at TEXT NOT NULL,
    completed_at TEXT NOT NULL,
    state_version INTEGER NOT NULL,
    FOREIGN KEY(operation_id) REFERENCES operations(id),
    FOREIGN KEY(task_handle) REFERENCES tasks(handle)
);
CREATE INDEX task_merges_task_state_idx
ON task_merges(task_handle, state, operation_id);
INSERT INTO schema_migrations(version, applied_at)
VALUES (40, strftime('%Y-%m-%dT%H:%M:%fZ', 'now'));
`

type taskMergeRow struct {
	operationID          string
	subjectDigest        string
	taskHandle           string
	managedRunID         string
	repositoryID         string
	pullRequestID        string
	branch               string
	headRevision         string
	evidenceDigest       string
	evidenceExpiresAt    time.Time
	requiredChecks       []string
	state                application.TaskMergeState
	approvalRequestID    string
	mcpOperationID       string
	resolvingPrincipalID string
	operationFingerprint string
	approvedAt           time.Time
	expiresAt            time.Time
	consumedAt           time.Time
	mergeCommitRevision  string
	mergeMethod          application.PullRequestMergeMethod
	reservedAt           time.Time
	completedAt          time.Time
	stateVersion         int64
}

func insertTaskMerge(ctx context.Context, target execer, row taskMergeRow) error {
	checks, err := json.Marshal(row.requiredChecks)
	if err != nil {
		return errors.New("insert task merge: required checks cannot be encoded")
	}
	const statement = `INSERT INTO task_merges (
	        operation_id, subject_digest, task_handle, managed_run_id, repository_id,
	        pull_request_id, branch, head_revision, evidence_digest, evidence_expires_at, required_checks_json,
	        state, approval_request_id, mcp_operation_id, resolving_principal_id,
	        operation_fingerprint, approved_at, expires_at, consumed_at,
	        merge_commit_revision, merge_method, reserved_at, completed_at, state_version
	    ) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, '', '', '', '', '', '', '', '', '', ?, '', ?)`
	_, err = target.ExecContext(ctx, statement,
		row.operationID, row.subjectDigest, row.taskHandle, row.managedRunID, row.repositoryID,
		row.pullRequestID, row.branch, row.headRevision, row.evidenceDigest, formatTime(row.evidenceExpiresAt), string(checks),
		row.state, formatTime(row.reservedAt), row.stateVersion,
	)
	if isConstraintError(err) {
		return fmt.Errorf("insert task merge: %w", application.ErrConflict)
	}
	if err != nil {
		return fmt.Errorf("insert task merge: %w", err)
	}
	return nil
}

func updateTaskMerge(ctx context.Context, target execer, row taskMergeRow) error {
	const statement = `UPDATE task_merges SET
        state = ?, approval_request_id = ?, mcp_operation_id = ?, resolving_principal_id = ?,
        operation_fingerprint = ?, approved_at = ?, expires_at = ?, consumed_at = ?,
        merge_commit_revision = ?, merge_method = ?, completed_at = ?, state_version = ?
        WHERE operation_id = ?`
	result, err := target.ExecContext(ctx, statement,
		row.state, row.approvalRequestID, row.mcpOperationID, row.resolvingPrincipalID,
		row.operationFingerprint, optionalTime(row.approvedAt), optionalTime(row.expiresAt), optionalTime(row.consumedAt),
		row.mergeCommitRevision, row.mergeMethod, optionalTime(row.completedAt), row.stateVersion, row.operationID,
	)
	if err != nil {
		return fmt.Errorf("update task merge: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil || rows != 1 {
		return errors.New("update task merge: exact row was not updated")
	}
	return nil
}

func updateTaskMergeOperation(
	ctx context.Context,
	target execer,
	row taskMergeRow,
	status domain.OperationStatus,
	at time.Time,
) error {
	const statement = `UPDATE operations SET status = ?, state_version = ?, updated_at = ?
        WHERE id = ? AND command = ? AND subject_digest = ?`
	result, err := target.ExecContext(ctx, statement, status, row.stateVersion, formatTime(at),
		row.operationID, commandMergeTask, row.subjectDigest)
	if err != nil {
		return fmt.Errorf("update task merge operation: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil || rows != 1 {
		return errors.New("update task merge operation: exact ledger row was not updated")
	}
	return nil
}

func findTaskMerge(ctx context.Context, source queryer, operationID string) (taskMergeRow, bool, error) {
	const query = `SELECT
	        operation_id, subject_digest, task_handle, managed_run_id, repository_id,
	        pull_request_id, branch, head_revision, evidence_digest, evidence_expires_at, required_checks_json,
        state, approval_request_id, mcp_operation_id, resolving_principal_id,
        operation_fingerprint, approved_at, expires_at, consumed_at,
        merge_commit_revision, merge_method, reserved_at, completed_at, state_version
        FROM task_merges WHERE operation_id = ?`
	row, err := scanTaskMerge(source.QueryRowContext(ctx, query, operationID))
	if errors.Is(err, sql.ErrNoRows) {
		return taskMergeRow{}, false, nil
	}
	if err != nil {
		return taskMergeRow{}, false, fmt.Errorf("read task merge: %w", err)
	}
	return row, true, nil
}

func scanTaskMerge(source rowScanner) (taskMergeRow, error) {
	var row taskMergeRow
	var requiredChecks, evidenceExpiresAt, approvedAt, expiresAt, consumedAt, reservedAt, completedAt string
	if err := source.Scan(
		&row.operationID, &row.subjectDigest, &row.taskHandle, &row.managedRunID, &row.repositoryID,
		&row.pullRequestID, &row.branch, &row.headRevision, &row.evidenceDigest, &evidenceExpiresAt, &requiredChecks,
		&row.state, &row.approvalRequestID, &row.mcpOperationID, &row.resolvingPrincipalID,
		&row.operationFingerprint, &approvedAt, &expiresAt, &consumedAt,
		&row.mergeCommitRevision, &row.mergeMethod, &reservedAt, &completedAt, &row.stateVersion,
	); err != nil {
		return taskMergeRow{}, err
	}
	if err := json.Unmarshal([]byte(requiredChecks), &row.requiredChecks); err != nil || len(row.requiredChecks) == 0 {
		return taskMergeRow{}, errors.New("stored task merge checks are invalid")
	}
	var err error
	row.reservedAt, err = parseTime(reservedAt)
	if err != nil {
		return taskMergeRow{}, errors.New("stored task merge reservation time is invalid")
	}
	row.evidenceExpiresAt, err = parseTime(evidenceExpiresAt)
	if err != nil {
		return taskMergeRow{}, errors.New("stored task merge evidence expiry is invalid")
	}
	if row.approvedAt, err = parseOptionalTaskMergeTime(approvedAt); err != nil {
		return taskMergeRow{}, err
	}
	if row.expiresAt, err = parseOptionalTaskMergeTime(expiresAt); err != nil {
		return taskMergeRow{}, err
	}
	if row.consumedAt, err = parseOptionalTaskMergeTime(consumedAt); err != nil {
		return taskMergeRow{}, err
	}
	if row.completedAt, err = parseOptionalTaskMergeTime(completedAt); err != nil {
		return taskMergeRow{}, err
	}
	return row, nil
}

func parseOptionalTaskMergeTime(value string) (time.Time, error) {
	if value == "" {
		return time.Time{}, nil
	}
	parsed, err := parseTime(value)
	if err != nil {
		return time.Time{}, errors.New("stored task merge time is invalid")
	}
	return parsed, nil
}

func verifyTaskMergeOperation(ctx context.Context, source queryer, row taskMergeRow) error {
	operation, err := getOperation(ctx, source, row.operationID)
	if err != nil {
		return err
	}
	wantStatus := domain.OperationAccepted
	if row.state == application.TaskMergeCompleted {
		wantStatus = domain.OperationCompleted
	}
	if operation.Command != commandMergeTask || operation.SubjectDigest != row.subjectDigest ||
		operation.ResultRef != row.taskHandle || operation.Status != wantStatus || operation.StateVersion != row.stateVersion {
		return errors.New("task merge operation ledger differs")
	}
	return nil
}

func taskMergeRecord(row taskMergeRow) application.TaskMergeRecord {
	return application.TaskMergeRecord{
		OperationID: row.operationID, SubjectDigest: row.subjectDigest,
		TaskHandle: row.taskHandle, ManagedRunID: row.managedRunID, RepositoryID: row.repositoryID,
		PullRequestID: row.pullRequestID, Branch: row.branch, HeadRevision: row.headRevision,
		EvidenceDigest: row.evidenceDigest, EvidenceExpiresAt: row.evidenceExpiresAt,
		RequiredChecks: append([]string(nil), row.requiredChecks...),
		State:          row.state, Approval: domain.MergeApproval{
			TaskHandle: row.taskHandle, ApprovalID: row.approvalRequestID,
			ManagedRunID: row.managedRunID, MCPOperationID: row.mcpOperationID,
			ResolvingPrincipal: row.resolvingPrincipalID, OperationFingerprint: row.operationFingerprint,
			ApprovedHead: row.headRevision, ApprovedAt: row.approvedAt, ExpiresAt: row.expiresAt,
			ConsumedAt: row.consumedAt, OperatorEnabled: row.state != application.TaskMergeAwaitingApproval,
		},
		MergeCommitRevision: row.mergeCommitRevision, Method: row.mergeMethod,
		ReservedAt: row.reservedAt, CompletedAt: row.completedAt, StateVersion: row.stateVersion,
	}
}

func taskMergeApprovalMatches(row taskMergeRow, approval domain.MergeApproval) bool {
	return row.taskHandle == approval.TaskHandle && row.approvalRequestID == approval.ApprovalID &&
		row.managedRunID == approval.ManagedRunID &&
		row.mcpOperationID == approval.MCPOperationID && row.resolvingPrincipalID == approval.ResolvingPrincipal &&
		row.operationFingerprint == approval.OperationFingerprint && row.headRevision == approval.ApprovedHead &&
		row.approvedAt.Equal(approval.ApprovedAt) && row.expiresAt.Equal(approval.ExpiresAt) &&
		row.consumedAt.Equal(approval.ConsumedAt) && approval.OperatorEnabled
}

func taskMergeReceiptMatches(row taskMergeRow, receipt application.PullRequestMergeReceipt) bool {
	return row.repositoryID == receipt.RepositoryID && row.pullRequestID == receipt.PullRequestID &&
		row.headRevision == receipt.HeadRevision && domain.ValidateGitRevision(receipt.MergeCommitRevision) == nil &&
		validStoredMergeMethod(receipt.Method) && row.mergeMethod == receipt.Method
}

func taskMergeCompletionMatches(row taskMergeRow, request application.TaskMergeCompletion) bool {
	return taskMergeReceiptMatches(row, request.Receipt) && row.mergeCommitRevision == request.Receipt.MergeCommitRevision &&
		row.mergeMethod == request.Receipt.Method
}

func sameStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func optionalTime(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return formatTime(value)
}
