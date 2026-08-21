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

func insertIntegrationApplication(ctx context.Context, target execer, row integrationApplicationRow) error {
	conflicts, err := json.Marshal(row.conflicts)
	if err != nil {
		return errors.New("insert integration application: conflicts cannot be encoded")
	}
	const statement = `INSERT INTO integration_applications (
        operation_id, subject_digest, initiative_handle, integration_task_handle, candidate_task_handle,
        repository_id, policy_id, strategy, target_worktree, expected_target_head,
        candidate_worktree, candidate_base, candidate_head, evidence_digest, evidence_expires_at,
        status, resulting_head, conflicts_json, reserved_at, completed_at, state_version
    ) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, '', ?, ?, '', 0)`
	_, err = target.ExecContext(ctx, statement,
		row.operationID, row.subjectDigest, row.initiativeHandle, row.integrationTaskHandle, row.candidateTaskHandle,
		row.repositoryID, row.policyID, row.strategy, row.targetWorktree, row.expectedTargetHead,
		row.candidateWorktree, row.candidateBase, row.candidateHead, row.evidenceDigest, formatTime(row.evidenceExpiresAt),
		row.status, string(conflicts), formatTime(row.reservedAt),
	)
	if isConstraintError(err) {
		return fmt.Errorf("insert integration application: %w", application.ErrConflict)
	}
	return err
}

func updateIntegrationApplication(ctx context.Context, target execer, row integrationApplicationRow) error {
	conflicts, err := json.Marshal(row.conflicts)
	if err != nil {
		return errors.New("update integration application: conflicts cannot be encoded")
	}
	result, err := target.ExecContext(ctx, `UPDATE integration_applications
        SET status = ?, resulting_head = ?, conflicts_json = ?, completed_at = ?, state_version = ?
        WHERE operation_id = ? AND status = 'reserved'`, row.status, row.resultingHead, string(conflicts),
		formatTime(row.completedAt), row.stateVersion, row.operationID)
	if err != nil {
		return fmt.Errorf("update integration application: %w", err)
	}
	changed, err := result.RowsAffected()
	if err != nil || changed != 1 {
		return fmt.Errorf("update integration application: %w", application.ErrConflict)
	}
	return nil
}

func findIntegrationApplication(ctx context.Context, source queryer, operationID string) (integrationApplicationRow, bool, error) {
	const query = `SELECT operation_id, subject_digest, initiative_handle, integration_task_handle,
        candidate_task_handle, repository_id, policy_id, strategy, target_worktree, expected_target_head,
        candidate_worktree, candidate_base, candidate_head, evidence_digest, evidence_expires_at,
        status, resulting_head, conflicts_json, reserved_at, completed_at, state_version
        FROM integration_applications WHERE operation_id = ?`
	row, err := scanIntegrationApplication(source.QueryRowContext(ctx, query, operationID))
	if errors.Is(err, sql.ErrNoRows) {
		return integrationApplicationRow{}, false, nil
	}
	if err != nil {
		return integrationApplicationRow{}, false, fmt.Errorf("read integration application: %w", err)
	}
	return row, true, nil
}

func findCandidateIntegrationApplication(
	ctx context.Context,
	source queryer,
	initiativeHandle string,
	integrationTaskHandle string,
	candidateTaskHandle string,
	candidateHead string,
) (integrationApplicationRow, bool, error) {
	const query = `SELECT operation_id, subject_digest, initiative_handle, integration_task_handle,
        candidate_task_handle, repository_id, policy_id, strategy, target_worktree, expected_target_head,
        candidate_worktree, candidate_base, candidate_head, evidence_digest, evidence_expires_at,
        status, resulting_head, conflicts_json, reserved_at, completed_at, state_version
        FROM integration_applications
        WHERE initiative_handle = ? AND integration_task_handle = ?
          AND candidate_task_handle = ? AND candidate_head = ?
          AND status IN ('reserved', 'applied', 'conflicted')
        ORDER BY reserved_at, operation_id LIMIT 1`
	row, err := scanIntegrationApplication(source.QueryRowContext(
		ctx, query, initiativeHandle, integrationTaskHandle, candidateTaskHandle, candidateHead,
	))
	if errors.Is(err, sql.ErrNoRows) {
		return integrationApplicationRow{}, false, nil
	}
	if err != nil {
		return integrationApplicationRow{}, false, fmt.Errorf("read candidate integration application: %w", err)
	}
	return row, true, nil
}

func scanIntegrationApplication(scanner rowScanner) (integrationApplicationRow, error) {
	var row integrationApplicationRow
	var evidenceExpiresAt, conflicts, reservedAt, completedAt string
	if err := scanner.Scan(
		&row.operationID, &row.subjectDigest, &row.initiativeHandle, &row.integrationTaskHandle,
		&row.candidateTaskHandle, &row.repositoryID, &row.policyID, &row.strategy,
		&row.targetWorktree, &row.expectedTargetHead, &row.candidateWorktree, &row.candidateBase,
		&row.candidateHead, &row.evidenceDigest, &evidenceExpiresAt, &row.status,
		&row.resultingHead, &conflicts, &reservedAt, &completedAt, &row.stateVersion,
	); err != nil {
		return integrationApplicationRow{}, err
	}
	var err error
	row.evidenceExpiresAt, err = parseTime(evidenceExpiresAt)
	if err != nil || json.Unmarshal([]byte(conflicts), &row.conflicts) != nil {
		return integrationApplicationRow{}, errors.New("stored integration evidence or conflicts are invalid")
	}
	row.reservedAt, err = parseTime(reservedAt)
	if err != nil {
		return integrationApplicationRow{}, errors.New("stored integration reservation time is invalid")
	}
	if completedAt != "" {
		row.completedAt, err = parseTime(completedAt)
		if err != nil {
			return integrationApplicationRow{}, errors.New("stored integration completion time is invalid")
		}
	}
	if !validIntegrationRow(row) {
		return integrationApplicationRow{}, errors.New("stored integration application is invalid")
	}
	return row, nil
}

func validIntegrationRow(row integrationApplicationRow) bool {
	if domain.ValidateOperationID(row.operationID) != nil || domain.ValidateBriefRevisionHash(row.subjectDigest) != nil ||
		domain.ValidateTaskHandle(row.initiativeHandle) != nil || domain.ValidateTaskHandle(row.integrationTaskHandle) != nil ||
		domain.ValidateTaskHandle(row.candidateTaskHandle) != nil || domain.ValidateRepositoryID(row.repositoryID) != nil ||
		domain.ValidateTaskHandle(row.policyID) != nil || domain.ValidateGitRevision(row.expectedTargetHead) != nil ||
		domain.ValidateGitRevision(row.candidateBase) != nil || domain.ValidateGitRevision(row.candidateHead) != nil ||
		domain.ValidateBriefRevisionHash(row.evidenceDigest) != nil || row.reservedAt.IsZero() || row.evidenceExpiresAt.IsZero() ||
		row.targetWorktree == row.candidateWorktree {
		return false
	}
	switch row.status {
	case "reserved":
		return row.resultingHead == "" && len(row.conflicts) == 0 && row.completedAt.IsZero() && row.stateVersion == 0
	case string(application.IntegrationApplied):
		return domain.ValidateGitRevision(row.resultingHead) == nil && len(row.conflicts) == 0 && !row.completedAt.IsZero() && row.stateVersion > 0
	case string(application.IntegrationConflicted):
		return row.resultingHead == "" && validStoredConflictPaths(row.conflicts) && !row.completedAt.IsZero() && row.stateVersion > 0
	case string(application.IntegrationInvalidated):
		return row.resultingHead == "" && len(row.conflicts) == 0 && !row.completedAt.IsZero() && row.stateVersion > 0
	default:
		return false
	}
}

func integrationReservationFromRow(row integrationApplicationRow) application.ReservedIntegrationApplication {
	reserved := application.ReservedIntegrationApplication{
		OperationID: row.operationID, SubjectDigest: row.subjectDigest,
		InitiativeHandle: row.initiativeHandle, IntegrationTaskHandle: row.integrationTaskHandle,
		PolicyID: row.policyID, Strategy: row.strategy,
		Target: application.IntegrationTargetReference{
			TaskHandle: row.integrationTaskHandle, RepositoryID: row.repositoryID,
			WorktreePath: row.targetWorktree, ExpectedHead: row.expectedTargetHead,
		},
		Candidate: application.IntegrationCandidateReference{
			TaskHandle: row.candidateTaskHandle, RepositoryID: row.repositoryID,
			WorktreePath: row.candidateWorktree, BaseRevision: row.candidateBase,
			HeadRevision: row.candidateHead, EvidenceDigest: row.evidenceDigest,
		},
		EvidenceExpiresAt: row.evidenceExpiresAt, ReservedAt: row.reservedAt,
	}
	if row.status != "reserved" {
		result := integrationResultFromRow(row)
		reserved.Result = &result
	}
	return reserved
}

func integrationResultFromRow(row integrationApplicationRow) application.IntegrationApplicationResult {
	return application.IntegrationApplicationResult{
		OperationID: row.operationID, InitiativeHandle: row.initiativeHandle,
		IntegrationTaskHandle: row.integrationTaskHandle,
		Candidate: application.IntegrationCandidateReference{
			TaskHandle: row.candidateTaskHandle, RepositoryID: row.repositoryID,
			WorktreePath: row.candidateWorktree, BaseRevision: row.candidateBase,
			HeadRevision: row.candidateHead, EvidenceDigest: row.evidenceDigest,
		},
		Strategy: row.strategy, Outcome: application.IntegrationOutcome(row.status),
		PreviousHead: row.expectedTargetHead, ResultingHead: row.resultingHead,
		ConflictPaths: append([]string(nil), row.conflicts...), StateVersion: row.stateVersion,
		CompletedAt: row.completedAt,
	}
}

func integrationRowMatchesRequest(row integrationApplicationRow, request application.IntegrationReservationRequest) bool {
	return row.operationID == request.Command.OperationID && row.subjectDigest == request.SubjectDigest &&
		row.initiativeHandle == request.Command.InitiativeHandle && row.integrationTaskHandle == request.Command.IntegrationTaskHandle &&
		row.candidateTaskHandle == request.Command.CandidateTaskHandle && row.policyID == request.PolicyID && row.strategy == request.Strategy &&
		row.candidateHead == request.Command.CandidateHead && row.expectedTargetHead == request.Command.ExpectedIntegrationHead
}

func integrationRowMatchesReservation(row integrationApplicationRow, reserved application.ReservedIntegrationApplication) bool {
	left := integrationReservationFromRow(row)
	return left.OperationID == reserved.OperationID && left.SubjectDigest == reserved.SubjectDigest &&
		left.InitiativeHandle == reserved.InitiativeHandle && left.IntegrationTaskHandle == reserved.IntegrationTaskHandle &&
		left.PolicyID == reserved.PolicyID && left.Strategy == reserved.Strategy && left.Target == reserved.Target &&
		left.Candidate == reserved.Candidate && left.EvidenceExpiresAt.Equal(reserved.EvidenceExpiresAt) &&
		left.ReservedAt.Equal(reserved.ReservedAt)
}

func integrationResultMatchesAdapter(result application.IntegrationApplicationResult, adapter application.IntegrationAdapterResult) bool {
	if result.Outcome != adapter.Outcome || result.PreviousHead != adapter.PreviousHead || result.ResultingHead != adapter.ResultingHead ||
		len(result.ConflictPaths) != len(adapter.ConflictPaths) {
		return false
	}
	for index := range result.ConflictPaths {
		if result.ConflictPaths[index] != adapter.ConflictPaths[index] {
			return false
		}
	}
	return true
}
