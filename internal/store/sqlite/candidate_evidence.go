package sqlite

import (
	"bytes"
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

const candidateEvidenceMigration = `
CREATE TABLE candidate_evidence (
    task_handle TEXT NOT NULL,
    evidence_digest TEXT NOT NULL,
    canonical BLOB NOT NULL,
    required_local_checks_json TEXT NOT NULL,
    required_forge_checks_json TEXT NOT NULL,
    outcome TEXT NOT NULL,
    reason TEXT NOT NULL,
    judged_at TEXT NOT NULL,
    state_version INTEGER NOT NULL,
    PRIMARY KEY(task_handle, evidence_digest),
    FOREIGN KEY(task_handle) REFERENCES tasks(handle)
);
CREATE INDEX candidate_evidence_latest_idx
ON candidate_evidence(task_handle, state_version DESC, evidence_digest);
INSERT OR IGNORE INTO schema_migrations(version, applied_at)
VALUES (13, strftime('%Y-%m-%dT%H:%M:%fZ', 'now'));
`

type candidateEvidenceRow struct {
	taskHandle          string
	digest              string
	canonical           []byte
	requiredLocalChecks []string
	requiredForgeChecks []string
	judgment            domain.CandidateJudgment
	judgedAt            time.Time
	stateVersion        int64
}

// CommitCandidateEvidence persists one immutable judgment and advances a task
// to candidate completion only when the pure domain judge accepts current facts.
func (store *Store) CommitCandidateEvidence(
	ctx context.Context,
	taskHandle string,
	evidence *domain.SealedDeliveryEvidence,
	requiredLocalChecks []string,
	requiredForgeChecks []string,
	judgedAt time.Time,
	publications []application.ComisEvidencePublication,
) (domain.Task, domain.CandidateJudgment, error) {
	if store == nil || store.db == nil || ctx == nil || evidence == nil || domain.ValidateTaskHandle(taskHandle) != nil ||
		judgedAt.IsZero() || judgedAt.Location() != time.UTC {
		return domain.Task{}, domain.CandidateJudgment{}, errors.New("commit candidate evidence: input is invalid")
	}
	if err := ctx.Err(); err != nil {
		return domain.Task{}, domain.CandidateJudgment{}, err
	}
	if evidence.Bundle().TaskHandle != taskHandle {
		return domain.Task{}, domain.CandidateJudgment{}, errors.New("commit candidate evidence: task identity differs")
	}
	transaction, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.Task{}, domain.CandidateJudgment{}, fmt.Errorf("begin candidate evidence: %w", err)
	}
	defer func() { _ = transaction.Rollback() }()
	existing, found, err := findCandidateEvidence(ctx, transaction, taskHandle, evidence.Digest())
	if err != nil {
		return domain.Task{}, domain.CandidateJudgment{}, err
	}
	if found {
		if !bytes.Equal(existing.canonical, evidence.Canonical()) ||
			!reflect.DeepEqual(existing.requiredLocalChecks, requiredLocalChecks) ||
			!reflect.DeepEqual(existing.requiredForgeChecks, requiredForgeChecks) || !existing.judgedAt.Equal(judgedAt) {
			return domain.Task{}, domain.CandidateJudgment{}, fmt.Errorf("candidate evidence altered replay: %w", application.ErrConflict)
		}
		task, readErr := getTask(ctx, transaction, taskHandle)
		if readErr != nil {
			return domain.Task{}, domain.CandidateJudgment{}, readErr
		}
		if err := validateReconciledCandidateEvidenceAuthority(ctx, transaction, task, evidence); err != nil {
			return domain.Task{}, domain.CandidateJudgment{}, err
		}
		if existing.judgment.Outcome == domain.CandidateAccepted {
			if err := validateCandidatePublications(task, evidence, publications); err != nil {
				return domain.Task{}, domain.CandidateJudgment{}, fmt.Errorf("candidate publication altered replay: %w", application.ErrConflict)
			}
			matches, matchErr := candidatePublicationsMatch(ctx, transaction, publications)
			if matchErr != nil {
				return domain.Task{}, domain.CandidateJudgment{}, matchErr
			}
			if !matches {
				return domain.Task{}, domain.CandidateJudgment{}, fmt.Errorf("candidate publication altered replay: %w", application.ErrConflict)
			}
		}
		return task, existing.judgment, nil
	}
	task, err := getTask(ctx, transaction, taskHandle)
	if err != nil {
		return domain.Task{}, domain.CandidateJudgment{}, err
	}
	if task.State != domain.TaskValidating {
		return domain.Task{}, domain.CandidateJudgment{}, fmt.Errorf("commit candidate evidence: task is not validating: %w", application.ErrPrecondition)
	}
	if err := validateReconciledCandidateEvidenceAuthority(ctx, transaction, task, evidence); err != nil {
		return domain.Task{}, domain.CandidateJudgment{}, err
	}
	judgment := domain.JudgeCandidate(domain.CandidateJudgeInput{
		Task: task, Evidence: evidence, RequiredLocalChecks: append([]string(nil), requiredLocalChecks...),
		RequiredForgeChecks: append([]string(nil), requiredForgeChecks...), Now: judgedAt,
	})
	if !validCandidateJudgment(judgment) {
		return domain.Task{}, domain.CandidateJudgment{}, errors.New("commit candidate evidence: judge returned an invalid verdict")
	}
	updated := task
	switch judgment.Outcome {
	case domain.CandidateAccepted:
		updated, err = task.ApplyTransition(domain.TransitionValidationAccepted, judgedAt)
		if err != nil {
			return domain.Task{}, domain.CandidateJudgment{}, fmt.Errorf("commit candidate evidence: %w", err)
		}
	case domain.CandidateRejected:
		updated, err = task.ApplyTransition(domain.TransitionFailureObserved, judgedAt)
		if err != nil {
			return domain.Task{}, domain.CandidateJudgment{}, fmt.Errorf("commit candidate evidence: %w", err)
		}
	case domain.CandidateUnknown:
		if judgedAt.Before(task.UpdatedAt) {
			return domain.Task{}, domain.CandidateJudgment{}, errors.New("commit candidate evidence: judgment time is regressive")
		}
		updated.UpdatedAt = judgedAt
	default:
		return domain.Task{}, domain.CandidateJudgment{}, errors.New("commit candidate evidence: judge returned an invalid outcome")
	}
	stateVersion, err := nextMutationStateVersion(ctx, transaction)
	if err != nil {
		return domain.Task{}, domain.CandidateJudgment{}, err
	}
	updated.StateVersion = stateVersion
	if err := updateTaskState(ctx, transaction, updated); err != nil {
		return domain.Task{}, domain.CandidateJudgment{}, err
	}
	row := candidateEvidenceRow{
		taskHandle: taskHandle, digest: evidence.Digest(), canonical: evidence.Canonical(),
		requiredLocalChecks: append([]string(nil), requiredLocalChecks...),
		requiredForgeChecks: append([]string(nil), requiredForgeChecks...),
		judgment:            judgment, judgedAt: judgedAt, stateVersion: stateVersion,
	}
	if err := insertCandidateEvidence(ctx, transaction, row); err != nil {
		return domain.Task{}, domain.CandidateJudgment{}, err
	}
	if judgment.Outcome == domain.CandidateAccepted {
		if err := insertCandidatePublications(ctx, transaction, task, evidence, publications, stateVersion); err != nil {
			return domain.Task{}, domain.CandidateJudgment{}, err
		}
	}
	if err := transaction.Commit(); err != nil {
		return domain.Task{}, domain.CandidateJudgment{}, fmt.Errorf("commit candidate evidence: %w", err)
	}
	return updated, judgment, nil
}

// LatestCandidateEvidence returns the highest-version immutable judgment.
func (store *Store) LatestCandidateEvidence(
	ctx context.Context,
	taskHandle string,
) (*domain.SealedDeliveryEvidence, domain.CandidateJudgment, error) {
	if store == nil || store.db == nil || ctx == nil || domain.ValidateTaskHandle(taskHandle) != nil {
		return nil, domain.CandidateJudgment{}, errors.New("read candidate evidence: input is invalid")
	}
	if err := ctx.Err(); err != nil {
		return nil, domain.CandidateJudgment{}, err
	}
	const query = `SELECT task_handle, evidence_digest, canonical,
        required_local_checks_json, required_forge_checks_json,
        outcome, reason, judged_at, state_version
    FROM candidate_evidence WHERE task_handle = ?
    ORDER BY state_version DESC, evidence_digest LIMIT 1`
	row, err := scanCandidateEvidence(store.db.QueryRowContext(ctx, query, taskHandle))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, domain.CandidateJudgment{}, fmt.Errorf("read candidate evidence: %w", application.ErrNotFound)
	}
	if err != nil {
		return nil, domain.CandidateJudgment{}, fmt.Errorf("read candidate evidence: %w", err)
	}
	sealed, err := domain.ParseDeliveryEvidence(row.canonical, row.digest)
	if err != nil {
		return nil, domain.CandidateJudgment{}, errors.New("read candidate evidence: stored evidence is invalid")
	}
	return sealed, row.judgment, nil
}

func findCandidateEvidence(
	ctx context.Context,
	source queryer,
	taskHandle, digest string,
) (candidateEvidenceRow, bool, error) {
	const query = `SELECT task_handle, evidence_digest, canonical,
        required_local_checks_json, required_forge_checks_json,
        outcome, reason, judged_at, state_version
    FROM candidate_evidence WHERE task_handle = ? AND evidence_digest = ?`
	row, err := scanCandidateEvidence(source.QueryRowContext(ctx, query, taskHandle, digest))
	if errors.Is(err, sql.ErrNoRows) {
		return candidateEvidenceRow{}, false, nil
	}
	if err != nil {
		return candidateEvidenceRow{}, false, fmt.Errorf("find candidate evidence: %w", err)
	}
	return row, true, nil
}

func insertCandidateEvidence(ctx context.Context, target execer, row candidateEvidenceRow) error {
	localChecks, err := json.Marshal(row.requiredLocalChecks)
	if err != nil {
		return errors.New("insert candidate evidence: local requirements cannot be encoded")
	}
	forgeChecks, err := json.Marshal(row.requiredForgeChecks)
	if err != nil {
		return errors.New("insert candidate evidence: forge requirements cannot be encoded")
	}
	const statement = `INSERT INTO candidate_evidence (
        task_handle, evidence_digest, canonical, required_local_checks_json,
        required_forge_checks_json, outcome, reason, judged_at, state_version
    ) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`
	_, err = target.ExecContext(ctx, statement,
		row.taskHandle, row.digest, row.canonical, string(localChecks), string(forgeChecks),
		row.judgment.Outcome, row.judgment.Reason, formatTime(row.judgedAt), row.stateVersion,
	)
	if isConstraintError(err) {
		return fmt.Errorf("insert candidate evidence: %w", application.ErrConflict)
	}
	if err != nil {
		return fmt.Errorf("insert candidate evidence: %w", err)
	}
	return nil
}

func scanCandidateEvidence(row rowScanner) (candidateEvidenceRow, error) {
	var stored candidateEvidenceRow
	var localChecks string
	var forgeChecks string
	var judgedAt string
	if err := row.Scan(
		&stored.taskHandle, &stored.digest, &stored.canonical, &localChecks, &forgeChecks,
		&stored.judgment.Outcome, &stored.judgment.Reason, &judgedAt, &stored.stateVersion,
	); err != nil {
		return candidateEvidenceRow{}, err
	}
	if domain.ValidateTaskHandle(stored.taskHandle) != nil || stored.stateVersion < 1 ||
		json.Unmarshal([]byte(localChecks), &stored.requiredLocalChecks) != nil ||
		json.Unmarshal([]byte(forgeChecks), &stored.requiredForgeChecks) != nil || !validCandidateJudgment(stored.judgment) {
		return candidateEvidenceRow{}, errors.New("stored candidate evidence metadata is invalid")
	}
	var err error
	stored.judgedAt, err = parseTime(judgedAt)
	if err != nil {
		return candidateEvidenceRow{}, errors.New("stored candidate evidence time is invalid")
	}
	if _, err := domain.ParseDeliveryEvidence(stored.canonical, stored.digest); err != nil {
		return candidateEvidenceRow{}, errors.New("stored candidate evidence body is invalid")
	}
	return stored, nil
}

func validCandidateJudgment(judgment domain.CandidateJudgment) bool {
	switch judgment.Outcome {
	case domain.CandidateAccepted:
		return judgment.Reason == domain.CandidateEvidenceAccepted
	case domain.CandidateRejected:
		return judgment.Reason == domain.CandidateValidationFailed || judgment.Reason == domain.CandidateForgeFailed
	case domain.CandidateUnknown:
		switch judgment.Reason {
		case domain.CandidateEvidenceInvalid, domain.CandidateEvidenceStale, domain.CandidateEvidenceConflicting,
			domain.CandidateWorktreeUnverified, domain.CandidateDecisionUnresolved, domain.CandidateValidationMissing,
			domain.CandidateValidationUnknown, domain.CandidateForgeMissing, domain.CandidateForgeUnknown,
			domain.CandidateReportMissing:
			return true
		default:
			return false
		}
	default:
		return false
	}
}
