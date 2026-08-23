package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/comisai/comis-dev-crew/internal/application"
	"github.com/comisai/comis-dev-crew/internal/domain"
)

const commandApplyIntegrationCandidate = "ApplyIntegrationCandidate"

const integrationApplicationMigration = `
CREATE TABLE integration_applications (
    operation_id TEXT PRIMARY KEY,
    subject_digest TEXT NOT NULL,
    initiative_handle TEXT NOT NULL,
    integration_task_handle TEXT NOT NULL,
    candidate_task_handle TEXT NOT NULL,
    repository_id TEXT NOT NULL,
    policy_id TEXT NOT NULL,
    strategy TEXT NOT NULL,
    target_worktree TEXT NOT NULL,
    expected_target_head TEXT NOT NULL,
    candidate_worktree TEXT NOT NULL,
    candidate_base TEXT NOT NULL,
    candidate_head TEXT NOT NULL,
    evidence_digest TEXT NOT NULL,
    evidence_expires_at TEXT NOT NULL,
    status TEXT NOT NULL,
    resulting_head TEXT NOT NULL,
    conflicts_json TEXT NOT NULL,
    reserved_at TEXT NOT NULL,
    completed_at TEXT NOT NULL,
    state_version INTEGER NOT NULL,
    FOREIGN KEY(initiative_handle) REFERENCES initiatives(handle),
    FOREIGN KEY(integration_task_handle) REFERENCES tasks(handle),
    FOREIGN KEY(candidate_task_handle) REFERENCES tasks(handle)
);
CREATE INDEX integration_applications_initiative_idx
ON integration_applications(initiative_handle, status, operation_id);
INSERT INTO schema_migrations(version, applied_at)
VALUES (39, strftime('%Y-%m-%dT%H:%M:%fZ', 'now'));
`

type integrationApplicationRow struct {
	operationID           string
	subjectDigest         string
	initiativeHandle      string
	integrationTaskHandle string
	candidateTaskHandle   string
	repositoryID          string
	policyID              string
	strategy              application.IntegrationStrategy
	targetWorktree        string
	expectedTargetHead    string
	candidateWorktree     string
	candidateBase         string
	candidateHead         string
	evidenceDigest        string
	evidenceExpiresAt     time.Time
	status                string
	resultingHead         string
	conflicts             []string
	reservedAt            time.Time
	completedAt           time.Time
	stateVersion          int64
}

var _ application.IntegrationStore = (*Store)(nil)

// IntegrationPolicy reads the immutable policy identity recorded by one
// validated initiative. Strategy resolution remains outside the store.
func (store *Store) IntegrationPolicy(ctx context.Context, initiativeHandle string) (string, error) {
	if store == nil || store.db == nil || ctx == nil || domain.ValidateTaskHandle(initiativeHandle) != nil {
		return "", errors.New("read integration policy: input is invalid")
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	initiative, err := getInitiative(ctx, store.db, initiativeHandle)
	if err != nil {
		return "", fmt.Errorf("read integration policy: %w", err)
	}
	return initiative.IntegrationPolicyID, nil
}

// ReserveIntegrationApplication resolves every path and evidence identity
// under one transaction before the Git adapter receives mutation authority.
func (store *Store) ReserveIntegrationApplication(
	ctx context.Context,
	request application.IntegrationReservationRequest,
) (application.ReservedIntegrationApplication, error) {
	if store == nil || store.db == nil || ctx == nil || validateIntegrationReservationRequest(request) != nil {
		return application.ReservedIntegrationApplication{}, errors.New("reserve integration application: input is invalid")
	}
	if err := ctx.Err(); err != nil {
		return application.ReservedIntegrationApplication{}, err
	}
	transaction, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return application.ReservedIntegrationApplication{}, fmt.Errorf("begin integration reservation: %w", err)
	}
	defer func() { _ = transaction.Rollback() }()
	if row, found, err := findIntegrationApplication(ctx, transaction, request.Command.OperationID); err != nil {
		return application.ReservedIntegrationApplication{}, err
	} else if found {
		if !integrationRowMatchesRequest(row, request) {
			return application.ReservedIntegrationApplication{}, fmt.Errorf("integration reservation altered replay: %w", application.ErrConflict)
		}
		if err := transaction.Commit(); err != nil {
			return application.ReservedIntegrationApplication{}, fmt.Errorf("commit integration reservation replay: %w", err)
		}
		return integrationReservationFromRow(row), nil
	}
	if _, err := getOperation(ctx, transaction, request.Command.OperationID); err == nil {
		return application.ReservedIntegrationApplication{}, fmt.Errorf("integration operation identity is already used: %w", application.ErrConflict)
	} else if !errors.Is(err, application.ErrNotFound) {
		return application.ReservedIntegrationApplication{}, err
	}
	row, err := resolveIntegrationReservation(ctx, transaction, request)
	if err != nil {
		return application.ReservedIntegrationApplication{}, err
	}
	if err := insertIntegrationApplication(ctx, transaction, row); err != nil {
		return application.ReservedIntegrationApplication{}, err
	}
	if err := transaction.Commit(); err != nil {
		return application.ReservedIntegrationApplication{}, fmt.Errorf("commit integration reservation: %w", err)
	}
	return integrationReservationFromRow(row), nil
}

// CompleteIntegrationApplication atomically records either the exact applied
// head or the exact conflict set together with the canonical operation ledger.
func (store *Store) CompleteIntegrationApplication(
	ctx context.Context,
	completion application.IntegrationCompletion,
) (application.IntegrationApplicationResult, error) {
	if store == nil || store.db == nil || ctx == nil || validateIntegrationCompletion(completion) != nil {
		return application.IntegrationApplicationResult{}, errors.New("complete integration application: input is invalid")
	}
	if err := ctx.Err(); err != nil {
		return application.IntegrationApplicationResult{}, err
	}
	transaction, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return application.IntegrationApplicationResult{}, fmt.Errorf("begin integration completion: %w", err)
	}
	defer func() { _ = transaction.Rollback() }()
	row, found, err := findIntegrationApplication(ctx, transaction, completion.Reservation.OperationID)
	if err != nil {
		return application.IntegrationApplicationResult{}, err
	}
	if !found || !integrationRowMatchesReservation(row, completion.Reservation) {
		return application.IntegrationApplicationResult{}, fmt.Errorf("integration completion reservation differs: %w", application.ErrConflict)
	}
	if row.status != "reserved" {
		result := integrationResultFromRow(row)
		if !integrationResultMatchesAdapter(result, completion.AdapterResult) {
			return application.IntegrationApplicationResult{}, fmt.Errorf("integration completion altered replay: %w", application.ErrConflict)
		}
		if err := transaction.Commit(); err != nil {
			return application.IntegrationApplicationResult{}, fmt.Errorf("commit integration completion replay: %w", err)
		}
		return result, nil
	}
	stateVersion, err := nextMutationStateVersion(ctx, transaction)
	if err != nil {
		return application.IntegrationApplicationResult{}, err
	}
	row.status = string(completion.AdapterResult.Outcome)
	row.resultingHead = completion.AdapterResult.ResultingHead
	row.conflicts = append([]string(nil), completion.AdapterResult.ConflictPaths...)
	row.completedAt = completion.At
	row.stateVersion = stateVersion
	if err := updateIntegrationApplication(ctx, transaction, row); err != nil {
		return application.IntegrationApplicationResult{}, err
	}
	if completion.AdapterResult.Outcome == application.IntegrationInvalidated {
		candidate, readErr := getTask(ctx, transaction, row.candidateTaskHandle)
		if readErr != nil {
			return application.IntegrationApplicationResult{}, readErr
		}
		invalidated, transitionErr := candidate.ApplyTransition(domain.TransitionEvidenceInvalidated, completion.At)
		if transitionErr != nil {
			return application.IntegrationApplicationResult{}, fmt.Errorf("invalidate integration candidate evidence: %w", transitionErr)
		}
		invalidated.StateVersion = stateVersion
		if err := updateTaskState(ctx, transaction, invalidated); err != nil {
			return application.IntegrationApplicationResult{}, err
		}
	}
	operation := completedMutationOperation(
		row.operationID, commandApplyIntegrationCandidate, row.subjectDigest,
		row.integrationTaskHandle, stateVersion, completion.At,
	)
	if err := insertOperation(ctx, transaction, operation); err != nil {
		return application.IntegrationApplicationResult{}, fmt.Errorf("insert integration operation: %w", err)
	}
	if err := transaction.Commit(); err != nil {
		return application.IntegrationApplicationResult{}, fmt.Errorf("commit integration completion: %w", err)
	}
	return integrationResultFromRow(row), nil
}

func resolveIntegrationReservation(
	ctx context.Context,
	transaction *sql.Tx,
	request application.IntegrationReservationRequest,
) (integrationApplicationRow, error) {
	initiative, err := getInitiative(ctx, transaction, request.Command.InitiativeHandle)
	if err != nil {
		return integrationApplicationRow{}, err
	}
	if initiative.State != domain.InitiativeActive && initiative.State != domain.InitiativeIntegrating {
		return integrationApplicationRow{}, fmt.Errorf("integration initiative is not active: %w", application.ErrPrecondition)
	}
	if initiative.IntegrationPolicyID != request.PolicyID || initiative.AuthorizeIntegrationWrite(request.Command.IntegrationTaskHandle) != nil {
		return integrationApplicationRow{}, fmt.Errorf("integration policy or owner differs: %w", application.ErrPrecondition)
	}
	integrationTask, err := getTask(ctx, transaction, request.Command.IntegrationTaskHandle)
	if err != nil {
		return integrationApplicationRow{}, err
	}
	candidateTask, err := getTask(ctx, transaction, request.Command.CandidateTaskHandle)
	if err != nil {
		return integrationApplicationRow{}, err
	}
	repositoryID, found := initiativeRepositoryForTask(initiative, candidateTask.Handle)
	targetRepositoryID, targetFound := initiativeRepositoryForTask(initiative, integrationTask.Handle)
	if !found || !targetFound || repositoryID != targetRepositoryID || candidateTask.RepositoryID != repositoryID ||
		integrationTask.RepositoryID != repositoryID || candidateTask.BaseRevision != initiativeBaseForRepository(initiative, repositoryID) ||
		integrationTask.BaseRevision != initiativeBaseForRepository(initiative, repositoryID) {
		return integrationApplicationRow{}, fmt.Errorf("integration task repository authority differs: %w", application.ErrPrecondition)
	}
	if candidateTask.State != domain.TaskCandidateComplete && candidateTask.State != domain.TaskDelivered {
		return integrationApplicationRow{}, fmt.Errorf("integration candidate is not complete: %w", application.ErrPrecondition)
	}
	worktrees := make(map[string]string)
	deliverySatisfied := make(map[string]bool)
	for _, component := range initiative.Components {
		for _, taskHandle := range component.TaskHandles {
			task, readErr := getTask(ctx, transaction, taskHandle)
			if readErr != nil {
				return integrationApplicationRow{}, readErr
			}
			preparation, readErr := getManagedRunPreparation(ctx, transaction, task)
			if readErr != nil || preparation.State != application.PreparationOpen || preparation.RequestedWorkspaceRoot == "" {
				return integrationApplicationRow{}, fmt.Errorf("integration worktree authority is unavailable: %w", application.ErrPrecondition)
			}
			worktrees[taskHandle] = preparation.RequestedWorkspaceRoot
			deliverySatisfied[taskHandle] = task.State.SatisfiesInitiativeDependency()
		}
	}
	ownerWritable := integrationTask.State == domain.TaskWorking ||
		integrationTask.State == domain.TaskAwaitingDecision || integrationTask.State == domain.TaskBlocked
	if integrationTask.State == domain.TaskReady {
		for _, taskHandle := range initiative.DependencyReadyTasks(deliverySatisfied) {
			if taskHandle == integrationTask.Handle {
				ownerWritable = true
				break
			}
		}
	}
	if !ownerWritable {
		return integrationApplicationRow{}, fmt.Errorf("integration owner is not writable: %w", application.ErrPrecondition)
	}
	if err := initiative.AuthorizeIntegrationWorktree(integrationTask.Handle, worktrees); err != nil {
		return integrationApplicationRow{}, fmt.Errorf("integration worktree authority differs: %w", application.ErrPrecondition)
	}
	evidenceRow, err := latestCandidateEvidenceRow(ctx, transaction, candidateTask.Handle)
	if err != nil {
		return integrationApplicationRow{}, err
	}
	sealed, err := domain.ParseDeliveryEvidence(evidenceRow.canonical, evidenceRow.digest)
	if err != nil || evidenceRow.judgment.Outcome != domain.CandidateAccepted {
		return integrationApplicationRow{}, fmt.Errorf("integration candidate evidence is unavailable: %w", application.ErrPrecondition)
	}
	judgment := domain.JudgeCandidate(domain.CandidateJudgeInput{
		Task: candidateTask, Evidence: sealed, RequiredLocalChecks: evidenceRow.requiredLocalChecks,
		RequiredForgeChecks: evidenceRow.requiredForgeChecks, Now: request.At,
	})
	bundle := sealed.Bundle()
	if judgment.Outcome != domain.CandidateAccepted || bundle.HeadRevision != request.Command.CandidateHead {
		return integrationApplicationRow{}, fmt.Errorf("integration candidate evidence is stale: %w", application.ErrPrecondition)
	}
	if _, found, readErr := findCandidateIntegrationApplication(
		ctx, transaction, initiative.Handle, integrationTask.Handle, candidateTask.Handle, request.Command.CandidateHead,
	); readErr != nil {
		return integrationApplicationRow{}, readErr
	} else if found {
		return integrationApplicationRow{}, fmt.Errorf("integration candidate application already exists: %w", application.ErrIntegrationApplicationExists)
	}
	return integrationApplicationRow{
		operationID: request.Command.OperationID, subjectDigest: request.SubjectDigest,
		initiativeHandle: initiative.Handle, integrationTaskHandle: integrationTask.Handle,
		candidateTaskHandle: candidateTask.Handle, repositoryID: repositoryID,
		policyID: request.PolicyID, strategy: request.Strategy,
		targetWorktree: worktrees[integrationTask.Handle], expectedTargetHead: request.Command.ExpectedIntegrationHead,
		candidateWorktree: worktrees[candidateTask.Handle], candidateBase: candidateTask.BaseRevision,
		candidateHead: request.Command.CandidateHead, evidenceDigest: sealed.Digest(),
		evidenceExpiresAt: bundle.ExpiresAt, status: "reserved", conflicts: []string{},
		reservedAt: request.At,
	}, nil
}

func latestCandidateEvidenceRow(ctx context.Context, source queryer, taskHandle string) (candidateEvidenceRow, error) {
	const query = `SELECT task_handle, evidence_digest, canonical,
        required_local_checks_json, required_forge_checks_json,
        outcome, reason, judged_at, state_version
        FROM candidate_evidence WHERE task_handle = ?
        ORDER BY state_version DESC, evidence_digest LIMIT 1`
	row, err := scanCandidateEvidence(source.QueryRowContext(ctx, query, taskHandle))
	if errors.Is(err, sql.ErrNoRows) {
		return candidateEvidenceRow{}, fmt.Errorf("read integration candidate evidence: %w", application.ErrNotFound)
	}
	if err != nil {
		return candidateEvidenceRow{}, fmt.Errorf("read integration candidate evidence: %w", err)
	}
	return row, nil
}

func validateIntegrationReservationRequest(request application.IntegrationReservationRequest) error {
	strategyValid := request.Strategy == application.IntegrationMerge || request.Strategy == application.IntegrationRebase ||
		request.Strategy == application.IntegrationCherryPick
	if domain.ValidateOperationID(request.Command.OperationID) != nil || domain.ValidateTaskHandle(request.Command.InitiativeHandle) != nil ||
		domain.ValidateTaskHandle(request.Command.IntegrationTaskHandle) != nil || domain.ValidateTaskHandle(request.Command.CandidateTaskHandle) != nil ||
		request.Command.IntegrationTaskHandle == request.Command.CandidateTaskHandle || domain.ValidateGitRevision(request.Command.CandidateHead) != nil ||
		domain.ValidateGitRevision(request.Command.ExpectedIntegrationHead) != nil || domain.ValidateTaskHandle(request.PolicyID) != nil ||
		domain.ValidateBriefRevisionHash(request.SubjectDigest) != nil || !strategyValid || request.At.IsZero() || request.At.Location() != time.UTC {
		return errors.New("integration reservation request is invalid")
	}
	return nil
}

func validateIntegrationCompletion(completion application.IntegrationCompletion) error {
	if completion.At.IsZero() || completion.At.Location() != time.UTC || completion.At.Before(completion.Reservation.ReservedAt) {
		return errors.New("integration completion time is invalid")
	}
	result := completion.AdapterResult
	if result.PreviousHead != completion.Reservation.Target.ExpectedHead {
		return errors.New("integration completion head differs")
	}
	switch result.Outcome {
	case application.IntegrationApplied:
		if domain.ValidateGitRevision(result.ResultingHead) != nil || result.ResultingHead == result.PreviousHead || len(result.ConflictPaths) != 0 {
			return errors.New("applied integration completion is invalid")
		}
	case application.IntegrationConflicted:
		if result.ResultingHead != "" || !validStoredConflictPaths(result.ConflictPaths) {
			return errors.New("conflicted integration completion is invalid")
		}
	case application.IntegrationInvalidated:
		if result.ResultingHead != "" || len(result.ConflictPaths) != 0 {
			return errors.New("invalidated integration completion is invalid")
		}
	default:
		return errors.New("integration completion outcome is invalid")
	}
	return nil
}

func validStoredConflictPaths(paths []string) bool {
	if len(paths) == 0 || len(paths) > 256 || !sort.StringsAreSorted(paths) {
		return false
	}
	for index, path := range paths {
		if path == "" || len([]byte(path)) > 1024 || filepath.IsAbs(path) || filepath.Clean(path) != path || path == "." || path == ".." ||
			strings.HasPrefix(path, ".."+string(filepath.Separator)) || (index > 0 && paths[index-1] == path) {
			return false
		}
	}
	return true
}

func initiativeRepositoryForTask(initiative domain.DevelopmentInitiative, taskHandle string) (string, bool) {
	for _, component := range initiative.Components {
		for _, member := range component.TaskHandles {
			if member == taskHandle {
				return component.RepositoryID, true
			}
		}
	}
	return "", false
}

func initiativeBaseForRepository(initiative domain.DevelopmentInitiative, repositoryID string) string {
	for _, base := range initiative.BaseRevisionSet {
		if base.RepositoryID == repositoryID {
			return base.Revision
		}
	}
	return ""
}
