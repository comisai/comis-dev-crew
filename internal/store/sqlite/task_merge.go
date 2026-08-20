package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/comisai/comis-dev-crew/internal/application"
	"github.com/comisai/comis-dev-crew/internal/domain"
)

const commandMergeTask = "MergeTask"

var _ application.TaskMergeStore = (*Store)(nil)

// BeginTaskMerge reserves the exact latest valid delivered evidence and an
// accepted operation ledger entry in one transaction.
func (store *Store) BeginTaskMerge(
	ctx context.Context,
	request application.TaskMergeReservation,
) (application.TaskMergeRecord, error) {
	if store == nil || store.db == nil || ctx == nil || validateTaskMergeReservation(request) != nil {
		return application.TaskMergeRecord{}, errors.New("reserve task merge: input is invalid")
	}
	if err := ctx.Err(); err != nil {
		return application.TaskMergeRecord{}, err
	}
	transaction, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return application.TaskMergeRecord{}, fmt.Errorf("begin task merge reservation: %w", err)
	}
	defer func() { _ = transaction.Rollback() }()
	if row, found, readErr := findTaskMerge(ctx, transaction, request.OperationID); readErr != nil {
		return application.TaskMergeRecord{}, readErr
	} else if found {
		if row.taskHandle != request.TaskHandle || row.subjectDigest != request.SubjectDigest {
			return application.TaskMergeRecord{}, fmt.Errorf("task merge reservation altered replay: %w", application.ErrConflict)
		}
		if row.state == application.TaskMergeAwaitingApproval {
			if err := revalidateTaskMergeReservation(ctx, transaction, row, request.At); err != nil {
				return application.TaskMergeRecord{}, err
			}
		}
		if err := verifyTaskMergeOperation(ctx, transaction, row); err != nil {
			return application.TaskMergeRecord{}, err
		}
		if err := transaction.Commit(); err != nil {
			return application.TaskMergeRecord{}, fmt.Errorf("commit task merge reservation replay: %w", err)
		}
		return taskMergeRecord(row), nil
	}
	if _, err := getOperation(ctx, transaction, request.OperationID); err == nil {
		return application.TaskMergeRecord{}, fmt.Errorf("task merge operation identity is already used: %w", application.ErrConflict)
	} else if !errors.Is(err, application.ErrNotFound) {
		return application.TaskMergeRecord{}, err
	}
	row, err := resolveTaskMergeReservation(ctx, transaction, request)
	if err != nil {
		return application.TaskMergeRecord{}, err
	}
	stateVersion, err := nextMutationStateVersion(ctx, transaction)
	if err != nil {
		return application.TaskMergeRecord{}, err
	}
	row.stateVersion = stateVersion
	operation := domain.OperationRecord{
		SchemaVersion: 1, ID: row.operationID, Command: commandMergeTask,
		SubjectDigest: row.subjectDigest, Status: domain.OperationAccepted,
		ResultRef: row.taskHandle, StateVersion: stateVersion,
		CreatedAt: row.reservedAt, UpdatedAt: row.reservedAt,
	}
	if err := insertOperation(ctx, transaction, operation); err != nil {
		return application.TaskMergeRecord{}, fmt.Errorf("insert task merge operation: %w", err)
	}
	if err := insertTaskMerge(ctx, transaction, row); err != nil {
		return application.TaskMergeRecord{}, err
	}
	if err := transaction.Commit(); err != nil {
		return application.TaskMergeRecord{}, fmt.Errorf("commit task merge reservation: %w", err)
	}
	return taskMergeRecord(row), nil
}

// AuthorizeTaskMerge atomically persists the exact authenticated receipt and
// marks the external mutation intent before the forge adapter is invoked.
func (store *Store) AuthorizeTaskMerge(
	ctx context.Context,
	request application.TaskMergeAuthorization,
) (application.TaskMergeRecord, error) {
	if store == nil || store.db == nil || ctx == nil ||
		domain.ValidateOperationID(request.OperationID) != nil || request.At.IsZero() || request.At.Location() != time.UTC {
		return application.TaskMergeRecord{}, errors.New("authorize task merge: input is invalid")
	}
	if err := ctx.Err(); err != nil {
		return application.TaskMergeRecord{}, err
	}
	transaction, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return application.TaskMergeRecord{}, fmt.Errorf("begin task merge authorization: %w", err)
	}
	defer func() { _ = transaction.Rollback() }()
	row, found, err := findTaskMerge(ctx, transaction, request.OperationID)
	if err != nil {
		return application.TaskMergeRecord{}, err
	}
	if !found {
		return application.TaskMergeRecord{}, fmt.Errorf("authorize task merge: %w", application.ErrNotFound)
	}
	if request.Approval.AuthorizeMerge(domain.MergeAuthorization{
		ObservedHead: row.headRevision, ManagedRunID: row.managedRunID,
		MCPOperationID: request.OperationID, Now: request.At,
	}) != nil {
		return application.TaskMergeRecord{}, fmt.Errorf("authorize task merge receipt differs: %w", application.ErrPrecondition)
	}
	if row.state != application.TaskMergeAwaitingApproval {
		if !taskMergeApprovalMatches(row, request.Approval) {
			return application.TaskMergeRecord{}, fmt.Errorf("task merge authorization altered replay: %w", application.ErrConflict)
		}
		if err := transaction.Commit(); err != nil {
			return application.TaskMergeRecord{}, fmt.Errorf("commit task merge authorization replay: %w", err)
		}
		return taskMergeRecord(row), nil
	}
	if err := revalidateTaskMergeReservation(ctx, transaction, row, request.At); err != nil {
		return application.TaskMergeRecord{}, err
	}
	stateVersion, err := nextMutationStateVersion(ctx, transaction)
	if err != nil {
		return application.TaskMergeRecord{}, err
	}
	row.state = application.TaskMergeExecutionAuthorized
	row.approvalRequestID = request.Approval.ApprovalID
	row.mcpOperationID = request.Approval.MCPOperationID
	row.resolvingPrincipalID = request.Approval.ResolvingPrincipal
	row.operationFingerprint = request.Approval.OperationFingerprint
	row.approvedAt, row.expiresAt, row.consumedAt = request.Approval.ApprovedAt, request.Approval.ExpiresAt, request.Approval.ConsumedAt
	row.stateVersion = stateVersion
	if err := updateTaskMerge(ctx, transaction, row); err != nil {
		return application.TaskMergeRecord{}, err
	}
	if err := updateTaskMergeOperation(ctx, transaction, row, domain.OperationAccepted, request.At); err != nil {
		return application.TaskMergeRecord{}, err
	}
	if err := transaction.Commit(); err != nil {
		return application.TaskMergeRecord{}, fmt.Errorf("commit task merge authorization: %w", err)
	}
	return taskMergeRecord(row), nil
}

// CompleteTaskMerge records only an exact post-mutation forge receipt and
// closes the canonical operation ledger in the same transaction.
func (store *Store) CompleteTaskMerge(
	ctx context.Context,
	request application.TaskMergeCompletion,
) (application.TaskMergeRecord, error) {
	if store == nil || store.db == nil || ctx == nil || validateTaskMergeCompletion(request) != nil {
		return application.TaskMergeRecord{}, errors.New("complete task merge: input is invalid")
	}
	if err := ctx.Err(); err != nil {
		return application.TaskMergeRecord{}, err
	}
	transaction, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return application.TaskMergeRecord{}, fmt.Errorf("begin task merge completion: %w", err)
	}
	defer func() { _ = transaction.Rollback() }()
	row, found, err := findTaskMerge(ctx, transaction, request.OperationID)
	if err != nil {
		return application.TaskMergeRecord{}, err
	}
	if !found {
		return application.TaskMergeRecord{}, fmt.Errorf("complete task merge: %w", application.ErrNotFound)
	}
	if row.state == application.TaskMergeCompleted {
		if !taskMergeCompletionMatches(row, request) {
			return application.TaskMergeRecord{}, fmt.Errorf("task merge completion altered replay: %w", application.ErrConflict)
		}
		if err := transaction.Commit(); err != nil {
			return application.TaskMergeRecord{}, fmt.Errorf("commit task merge completion replay: %w", err)
		}
		return taskMergeRecord(row), nil
	}
	if row.state != application.TaskMergeExecutionAuthorized || !taskMergeReceiptMatches(row, request.Receipt) ||
		request.At.Before(row.consumedAt) {
		return application.TaskMergeRecord{}, fmt.Errorf("task merge completion authority differs: %w", application.ErrPrecondition)
	}
	stateVersion, err := nextMutationStateVersion(ctx, transaction)
	if err != nil {
		return application.TaskMergeRecord{}, err
	}
	row.state = application.TaskMergeCompleted
	row.mergeCommitRevision = request.Receipt.MergeCommitRevision
	row.mergeMethod = request.Receipt.Method
	row.completedAt = request.At
	row.stateVersion = stateVersion
	if err := updateTaskMerge(ctx, transaction, row); err != nil {
		return application.TaskMergeRecord{}, err
	}
	if err := updateTaskMergeOperation(ctx, transaction, row, domain.OperationCompleted, request.At); err != nil {
		return application.TaskMergeRecord{}, err
	}
	if err := transaction.Commit(); err != nil {
		return application.TaskMergeRecord{}, fmt.Errorf("commit task merge completion: %w", err)
	}
	return taskMergeRecord(row), nil
}

func resolveTaskMergeReservation(
	ctx context.Context,
	transaction *sql.Tx,
	request application.TaskMergeReservation,
) (taskMergeRow, error) {
	task, err := getTask(ctx, transaction, request.TaskHandle)
	if err != nil {
		return taskMergeRow{}, err
	}
	if task.State != domain.TaskDelivered || task.Shape != domain.ShapeShip ||
		task.DeliveryMode != domain.DeliveryMergeAfterApproval || task.ManagedRunID == "" {
		return taskMergeRow{}, fmt.Errorf("task is not eligible for approval-bound merge: %w", application.ErrPrecondition)
	}
	evidenceRow, err := latestCandidateEvidenceRow(ctx, transaction, task.Handle)
	if err != nil {
		return taskMergeRow{}, err
	}
	sealed, err := domain.ParseDeliveryEvidence(evidenceRow.canonical, evidenceRow.digest)
	if err != nil {
		return taskMergeRow{}, fmt.Errorf("task merge evidence is invalid: %w", application.ErrPrecondition)
	}
	judgment := domain.JudgeCandidate(domain.CandidateJudgeInput{
		Task: task, Evidence: sealed, RequiredLocalChecks: evidenceRow.requiredLocalChecks,
		RequiredForgeChecks: evidenceRow.requiredForgeChecks, Now: request.At,
	})
	bundle := sealed.Bundle()
	if judgment.Outcome != domain.CandidateAccepted || bundle.ForgeEvidence == nil {
		return taskMergeRow{}, fmt.Errorf("task merge evidence is not current and accepted: %w", application.ErrPrecondition)
	}
	forgeEvidence := bundle.ForgeEvidence
	return taskMergeRow{
		operationID: request.OperationID, subjectDigest: request.SubjectDigest,
		taskHandle: task.Handle, managedRunID: task.ManagedRunID, repositoryID: task.RepositoryID,
		pullRequestID: forgeEvidence.PullRequestID, branch: forgeEvidence.Branch,
		headRevision: forgeEvidence.HeadRevision, evidenceDigest: sealed.Digest(),
		requiredChecks: append([]string(nil), evidenceRow.requiredForgeChecks...),
		state:          application.TaskMergeAwaitingApproval, reservedAt: request.At,
	}, nil
}

func revalidateTaskMergeReservation(ctx context.Context, transaction *sql.Tx, row taskMergeRow, at time.Time) error {
	request := application.TaskMergeReservation{
		OperationID: row.operationID, TaskHandle: row.taskHandle, SubjectDigest: row.subjectDigest, At: at,
	}
	current, err := resolveTaskMergeReservation(ctx, transaction, request)
	if err != nil {
		return err
	}
	if current.managedRunID != row.managedRunID || current.repositoryID != row.repositoryID ||
		current.pullRequestID != row.pullRequestID || current.branch != row.branch ||
		current.headRevision != row.headRevision || current.evidenceDigest != row.evidenceDigest ||
		!sameStrings(current.requiredChecks, row.requiredChecks) {
		return fmt.Errorf("task merge evidence changed after reservation: %w", application.ErrPrecondition)
	}
	return nil
}

func validateTaskMergeReservation(request application.TaskMergeReservation) error {
	if domain.ValidateOperationID(request.OperationID) != nil || domain.ValidateTaskHandle(request.TaskHandle) != nil ||
		domain.ValidateBriefRevisionHash(request.SubjectDigest) != nil || request.At.IsZero() || request.At.Location() != time.UTC {
		return errors.New("task merge reservation is invalid")
	}
	return nil
}

func validateTaskMergeCompletion(request application.TaskMergeCompletion) error {
	receipt := request.Receipt
	if domain.ValidateOperationID(request.OperationID) != nil || request.At.IsZero() || request.At.Location() != time.UTC ||
		domain.ValidateRepositoryID(receipt.RepositoryID) != nil ||
		domain.ValidateAuthorityReference("pullRequestId", receipt.PullRequestID) != nil ||
		domain.ValidateGitRevision(receipt.HeadRevision) != nil ||
		domain.ValidateGitRevision(receipt.MergeCommitRevision) != nil || !validStoredMergeMethod(receipt.Method) {
		return errors.New("task merge completion is invalid")
	}
	return nil
}

func validStoredMergeMethod(method application.PullRequestMergeMethod) bool {
	return method == application.PullRequestMergeCommit || method == application.PullRequestMergeSquash ||
		method == application.PullRequestMergeRebase
}
