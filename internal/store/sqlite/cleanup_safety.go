package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"time"

	"github.com/comisai/comis-dev-crew/internal/application"
	"github.com/comisai/comis-dev-crew/internal/domain"
)

func validateCleanupMutation(store *Store, ctx context.Context, mutation application.TaskCleanupMutation) error {
	if store == nil || store.db == nil || ctx == nil || domain.ValidateOperationID(mutation.OperationID) != nil ||
		domain.ValidateOperationID(mutation.ReleaseOperationID) != nil || domain.ValidateTaskHandle(mutation.TaskHandle) != nil ||
		len(mutation.SubjectDigest) != 64 || mutation.ReleasedAt.IsZero() || mutation.ReleasedAt.Location() != time.UTC ||
		mutation.At.IsZero() || mutation.At.Location() != time.UTC || !mutation.ReleasedAt.Equal(mutation.At) {
		return errors.New("begin task cleanup: input is invalid")
	}
	return ctx.Err()
}

func proveCleanupDatabaseSafety(ctx context.Context, transaction *sql.Tx, task domain.Task) error {
	queries := []struct {
		name    string
		query   string
		args    []any
		blocker error
	}{
		{name: "open hold", query: `SELECT COUNT(*) FROM task_cleanup_holds
            WHERE task_handle = ? AND closed_at IS NULL`, args: []any{task.Handle}, blocker: application.ErrCleanupOpenHold},
		{name: "active validation", query: `SELECT COUNT(*) FROM validation_processes
		    WHERE task_handle = ? AND state NOT IN ('exited', 'absent')`, args: []any{task.Handle}, blocker: application.ErrCleanupActiveExecution},
		{name: "unresolved decision", query: `SELECT COUNT(*) FROM reports d
            WHERE d.task_handle = ? AND d.kind = 'decision' AND NOT EXISTS (
                SELECT 1 FROM reports r WHERE r.task_handle = d.task_handle
                AND r.kind = 'resolution' AND r.external_key = d.external_key
			)`, args: []any{task.Handle}, blocker: application.ErrCleanupOpenDecision},
	}
	// A scout is the only shape whose worktree holds an investigation nobody
	// else has a copy of. The gate turns on the absence of a record as much as
	// on its content: no row means nobody looked, which is not the same fact as
	// somebody having looked and found nothing.
	if task.Shape == domain.ShapeScout {
		queries = append(queries, struct {
			name    string
			query   string
			args    []any
			blocker error
		}{
			name: "scout decision inventory",
			query: `SELECT CASE WHEN EXISTS (
                SELECT 1 FROM scout_decision_attestations
                WHERE task_handle = ? AND finding = 'no_open_decisions'
            ) THEN 0 ELSE 1 END`,
			args: []any{task.Handle}, blocker: application.ErrCleanupUnattestedScout,
		})
	}
	for _, check := range queries {
		var count int
		if err := transaction.QueryRowContext(ctx, check.query, check.args...).Scan(&count); err != nil {
			return fmt.Errorf("inspect task cleanup %s: %w", check.name, err)
		}
		if count != 0 {
			return fmt.Errorf("task cleanup %s remains: %w", check.name, check.blocker)
		}
	}
	var managedRunID, workspaceLeaseID string
	var transition application.TerminalTransition
	err := transaction.QueryRowContext(ctx, `SELECT managed_run_id, workspace_lease_id, latest_transition
        FROM task_terminal_bindings WHERE task_handle = ?`, task.Handle).Scan(
		&managedRunID, &workspaceLeaseID, &transition,
	)
	if errors.Is(err, sql.ErrNoRows) && task.WorkerProfileID == "fixture-worker" {
		// The deterministic in-process fixture never acquires terminal custody.
		// Delivery, evidence, decision, validation, hold, and workspace checks
		// below remain mandatory before its managed run can be released.
	} else if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("task cleanup terminal is unavailable: %w", application.ErrCleanupUnknownExecution)
	} else if err != nil {
		return fmt.Errorf("inspect task cleanup terminal: %w", err)
	} else if managedRunID != task.ManagedRunID || workspaceLeaseID != task.WorkspaceLeaseID {
		return fmt.Errorf("task cleanup terminal authority differs: %w", application.ErrCleanupUnknownExecution)
	} else {
		switch transition {
		case application.TerminalExited, application.TerminalReleased:
		case application.TerminalLost:
			return fmt.Errorf("task cleanup terminal is lost: %w", application.ErrCleanupUnknownExecution)
		case application.TerminalCreated, application.TerminalRunning, application.TerminalInputNeeded,
			application.TerminalStuck, application.TerminalRecovered:
			return fmt.Errorf("task cleanup terminal remains active: %w", application.ErrCleanupActiveExecution)
		default:
			return fmt.Errorf("task cleanup terminal state is unknown: %w", application.ErrCleanupUnknownExecution)
		}
	}
	var evidenceTotal, evidenceDelivered int
	if err := transaction.QueryRowContext(ctx, `SELECT COUNT(*), COUNT(delivered_at)
        FROM comis_evidence_outbox WHERE task_handle = ?`, task.Handle).Scan(&evidenceTotal, &evidenceDelivered); err != nil {
		return fmt.Errorf("inspect task cleanup evidence delivery: %w", err)
	}
	if evidenceTotal != 2 || evidenceDelivered != 2 {
		return fmt.Errorf("task cleanup evidence delivery is incomplete: %w", application.ErrPrecondition)
	}
	return nil
}

func proveCleanupCandidateOrigin(
	ctx context.Context,
	transaction *sql.Tx,
	task domain.Task,
	preparationOperationID string,
	worktreePath string,
	headRevision string,
) error {
	var candidateTotal, candidateDelivered int
	if err := transaction.QueryRowContext(ctx, `SELECT COUNT(*), COUNT(o.delivered_at)
		FROM reports r JOIN comis_report_outbox o
		ON o.task_handle = r.task_handle AND o.local_report_id = r.local_report_id
		WHERE r.task_handle = ? AND r.kind = 'candidate_complete'`, task.Handle).Scan(
		&candidateTotal, &candidateDelivered,
	); err != nil {
		return fmt.Errorf("inspect task cleanup report delivery: %w", err)
	}
	var reconciliationTotal int
	if err := transaction.QueryRowContext(ctx, `SELECT COUNT(*)
		FROM task_candidate_reconciliations r
		JOIN operations recovery ON recovery.id = r.operation_id
		JOIN operations preparation ON preparation.id = r.preparation_operation_id
		JOIN task_preparations p ON p.task_handle = r.task_handle
		JOIN task_terminal_bindings terminal ON terminal.task_handle = r.task_handle
		WHERE r.task_handle = ? AND r.action = ?
		AND r.preparation_operation_id = ?
		AND r.repository_id = ? AND r.worktree_path = ?
		AND p.requested_workspace_root = r.worktree_path
		AND r.base_revision = ? AND r.head_revision = ? AND r.cleanliness = ?
		AND terminal.terminal_session_id = r.terminal_session_id
		AND terminal.latest_transition = r.terminal_transition
		AND terminal.updated_at = r.terminal_observed_at
		AND recovery.command = ? AND recovery.status = ? AND recovery.result_ref = r.task_handle
		AND recovery.state_version = r.completed_state_version
		AND preparation.command = ? AND preparation.status = ? AND preparation.result_ref = r.task_handle
		AND r.started_state_version + 1 = r.completed_state_version
		AND r.completed_state_version <= ?`,
		task.Handle, application.ReconcileValidateCleanCandidate, preparationOperationID,
		task.RepositoryID, worktreePath, task.BaseRevision, headRevision, application.WorkspaceClean,
		commandReconcileTask, domain.OperationCompleted, commandPrepareTask, domain.OperationCompleted,
		task.StateVersion,
	).Scan(&reconciliationTotal); err != nil {
		return fmt.Errorf("inspect task cleanup reconciliation evidence: %w", err)
	}
	reportOrigin := candidateTotal == 1 && candidateDelivered == 1 && reconciliationTotal == 0
	reconciliationOrigin := candidateTotal == 0 && candidateDelivered == 0 && reconciliationTotal == 1
	if !reportOrigin && !reconciliationOrigin {
		return fmt.Errorf("task cleanup candidate origin is incomplete or ambiguous: %w", application.ErrPrecondition)
	}
	return nil
}

func cleanupPreparation(ctx context.Context, transaction *sql.Tx, taskHandle string) (string, string, error) {
	const query = `SELECT o.id, p.requested_workspace_root
        FROM operations o JOIN task_preparations p ON p.task_handle = o.result_ref
        WHERE o.command = ? AND o.status = ? AND o.result_ref = ?`
	rows, err := transaction.QueryContext(ctx, query, commandPrepareTask, domain.OperationCompleted, taskHandle)
	if err != nil {
		return "", "", fmt.Errorf("inspect task cleanup preparation: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var operationID, worktreePath string
	count := 0
	for rows.Next() {
		if err := rows.Scan(&operationID, &worktreePath); err != nil {
			return "", "", fmt.Errorf("scan task cleanup preparation: %w", err)
		}
		count++
	}
	if err := rows.Err(); err != nil {
		return "", "", fmt.Errorf("inspect task cleanup preparation: %w", err)
	}
	if count != 1 || worktreePath == "" {
		return "", "", fmt.Errorf("task cleanup preparation is ambiguous: %w", application.ErrPrecondition)
	}
	return operationID, worktreePath, nil
}

func cleanupEvidence(
	ctx context.Context,
	transaction *sql.Tx,
	task domain.Task,
) (candidateEvidenceRow, *domain.SealedDeliveryEvidence, error) {
	const query = `SELECT task_handle, evidence_digest, canonical,
        required_local_checks_json, required_forge_checks_json,
        outcome, reason, judged_at, state_version
        FROM candidate_evidence WHERE task_handle = ?
        ORDER BY state_version DESC, evidence_digest LIMIT 1`
	row, err := scanCandidateEvidence(transaction.QueryRowContext(ctx, query, task.Handle))
	if err != nil {
		return candidateEvidenceRow{}, nil, fmt.Errorf("read task cleanup evidence: %w", err)
	}
	sealed, err := domain.ParseDeliveryEvidence(row.canonical, row.digest)
	if err != nil {
		return candidateEvidenceRow{}, nil, errors.New("read task cleanup evidence: stored evidence is invalid")
	}
	bundle := sealed.Bundle()
	if row.judgment.Outcome != domain.CandidateAccepted || row.judgment.Reason != domain.CandidateEvidenceAccepted ||
		bundle.TaskHandle != task.Handle || bundle.RepositoryIdentity != task.RepositoryID ||
		bundle.BaseRevision != task.BaseRevision || bundle.WorktreeCleanliness != domain.WorktreeClean ||
		bundle.UnresolvedDecisionCount != 0 || (task.DeliveryMode == domain.DeliveryPullRequest) != (bundle.ForgeEvidence != nil) ||
		(task.DeliveryMode == domain.DeliveryReport) != (bundle.ReportArtifact != nil) {
		return candidateEvidenceRow{}, nil, fmt.Errorf("task cleanup candidate evidence differs: %w", application.ErrPrecondition)
	}
	return row, sealed, nil
}

func findTaskCleanupRecord(
	ctx context.Context,
	source queryer,
	operationID string,
) (application.TaskCleanupRecord, bool, error) {
	return scanTaskCleanupRecord(source.QueryRowContext(
		ctx,
		taskCleanupRecordQuery+" WHERE operation_id = ?",
		operationID,
	))
}

func findTaskCleanupRecordByTask(
	ctx context.Context,
	source queryer,
	taskHandle string,
) (application.TaskCleanupRecord, bool, error) {
	return scanTaskCleanupRecord(source.QueryRowContext(
		ctx,
		taskCleanupRecordQuery+" WHERE task_handle = ?",
		taskHandle,
	))
}

const taskCleanupRecordQuery = `SELECT operation_id, subject_digest, task_handle, preparation_operation_id,
        managed_run_id, workspace_lease_id, repository_id, worktree_path,
        head_revision, evidence_digest, pull_request_id, required_forge_checks_json,
        report_artifact_hash, stage, release_operation_id, released_at,
        snapshot_branch, snapshot_head_revision, snapshot_cleanliness, discard
		FROM task_cleanup_operations`

func scanTaskCleanupRecord(row rowScanner) (application.TaskCleanupRecord, bool, error) {
	var record application.TaskCleanupRecord
	var checks, releasedAt string
	var snapshotBranch, snapshotHead string
	var snapshotCleanliness application.WorkspaceCleanliness
	err := row.Scan(
		&record.OperationID, &record.SubjectDigest, &record.TaskHandle, &record.PreparationOperationID,
		&record.ManagedRunID, &record.WorkspaceLeaseID, &record.RepositoryID, &record.WorktreePath,
		&record.HeadRevision, &record.EvidenceDigest, &record.PullRequestID, &checks,
		&record.ReportArtifactHash, &record.Stage, &record.ReleaseOperationID, &releasedAt,
		&snapshotBranch, &snapshotHead, &snapshotCleanliness, &record.Discard,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return application.TaskCleanupRecord{}, false, nil
	}
	if err != nil {
		return application.TaskCleanupRecord{}, false, fmt.Errorf("read task cleanup record: %w", err)
	}
	if err := json.Unmarshal([]byte(checks), &record.RequiredForgeChecks); err != nil {
		return application.TaskCleanupRecord{}, false, errors.New("read task cleanup record: forge checks are invalid")
	}
	record.ReleasedAt, err = parseTime(releasedAt)
	if err != nil {
		return application.TaskCleanupRecord{}, false, errors.New("read task cleanup record: release time is invalid")
	}
	if snapshotBranch != "" || snapshotHead != "" || snapshotCleanliness != "" {
		record.Snapshot = application.WorkspaceSnapshot{
			TaskHandle: record.TaskHandle, RepositoryID: record.RepositoryID, WorktreePath: record.WorktreePath,
			Branch: snapshotBranch, HeadRevision: snapshotHead, Cleanliness: snapshotCleanliness,
		}
		if record.Snapshot.Validate() != nil {
			return application.TaskCleanupRecord{}, false, errors.New("read task cleanup record: snapshot is invalid")
		}
	}
	switch record.Stage {
	case application.CleanupPrepared, application.CleanupHostReleased,
		application.CleanupRemovalAuthorized, application.CleanupCompleted:
	default:
		return application.TaskCleanupRecord{}, false, errors.New("read task cleanup record: stage is invalid")
	}
	return record, true, nil
}

func validateCleanupProof(
	record application.TaskCleanupRecord,
	snapshot application.WorkspaceSnapshot,
	truth application.PullRequestDeliveryTruth,
) error {
	if snapshot.TaskHandle != record.TaskHandle || snapshot.RepositoryID != record.RepositoryID ||
		snapshot.WorktreePath != record.WorktreePath || snapshot.HeadRevision != record.HeadRevision ||
		snapshot.Cleanliness != application.WorkspaceClean {
		return fmt.Errorf("task cleanup workspace proof differs: %w", application.ErrPrecondition)
	}
	if record.PullRequestID == "" {
		if record.ReportArtifactHash == "" || !reflect.DeepEqual(truth, application.PullRequestDeliveryTruth{}) {
			return fmt.Errorf("task cleanup report proof differs: %w", application.ErrPrecondition)
		}
		return nil
	}
	wantChecks := make([]application.ForgeCheckTruth, len(record.RequiredForgeChecks))
	for index, name := range record.RequiredForgeChecks {
		wantChecks[index] = application.ForgeCheckTruth{Name: name, Conclusion: domain.CheckPassed}
	}
	if truth.RepositoryID != record.RepositoryID || truth.PullRequestID != record.PullRequestID ||
		truth.HeadRevision != record.HeadRevision || !reflect.DeepEqual(truth.Checks, wantChecks) {
		return fmt.Errorf("task cleanup forge proof differs: %w", application.ErrPrecondition)
	}
	return nil
}

func cleanupProofMatches(
	record application.TaskCleanupRecord,
	snapshot application.WorkspaceSnapshot,
	truth application.PullRequestDeliveryTruth,
) bool {
	return reflect.DeepEqual(record.Snapshot, snapshot) && validateCleanupProof(record, snapshot, truth) == nil
}
