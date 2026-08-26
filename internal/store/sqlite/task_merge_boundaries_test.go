package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/comisai/comis-dev-crew/internal/application"
	"github.com/comisai/comis-dev-crew/internal/domain"
)

func TestTaskMergeStoreRejectsInvalidContextInputAndMissingTransactions(t *testing.T) {
	store, err := Open(context.Background(), filepath.Join(canonicalTempDir(t), "boundaries.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	now := time.Date(2026, time.August, 20, 12, 0, 0, 0, time.UTC)
	reservation := application.TaskMergeReservation{
		OperationID: "merge-operation-boundary", TaskHandle: "task-merge-boundary",
		SubjectDigest: strings.Repeat("1", 64), At: now,
	}
	authorization := application.TaskMergeAuthorization{
		OperationID: reservation.OperationID, Method: application.PullRequestMergeSquash, At: now,
	}
	completion := application.TaskMergeCompletion{
		OperationID: reservation.OperationID, At: now,
		Receipt: application.PullRequestMergeReceipt{
			RepositoryID: "repository-merge", PullRequestID: "pull-request-merge",
			HeadRevision: strings.Repeat("b", 40), MergeCommitRevision: strings.Repeat("c", 40),
			Method: application.PullRequestMergeSquash,
		},
	}
	//lint:ignore SA1012 The store boundary rejects nil before touching SQLite.
	if _, err := store.BeginTaskMerge(nil, reservation); err == nil {
		t.Fatal("BeginTaskMerge(nil context) error = nil")
	}
	if _, err := (*Store)(nil).BeginTaskMerge(context.Background(), reservation); err == nil {
		t.Fatal("BeginTaskMerge(nil store) error = nil")
	}
	if _, err := store.BeginTaskMerge(context.Background(), application.TaskMergeReservation{}); err == nil {
		t.Fatal("BeginTaskMerge(invalid) error = nil")
	}
	//lint:ignore SA1012 The store boundary rejects nil before touching SQLite.
	if _, err := store.AuthorizeTaskMerge(nil, authorization); err == nil {
		t.Fatal("AuthorizeTaskMerge(nil context) error = nil")
	}
	if _, err := (*Store)(nil).AuthorizeTaskMerge(context.Background(), authorization); err == nil {
		t.Fatal("AuthorizeTaskMerge(nil store) error = nil")
	}
	if _, err := store.AuthorizeTaskMerge(context.Background(), authorization); !errors.Is(err, application.ErrNotFound) {
		t.Fatalf("AuthorizeTaskMerge(missing) error = %v", err)
	}
	//lint:ignore SA1012 The store boundary rejects nil before touching SQLite.
	if _, err := store.CompleteTaskMerge(nil, completion); err == nil {
		t.Fatal("CompleteTaskMerge(nil context) error = nil")
	}
	if _, err := (*Store)(nil).CompleteTaskMerge(context.Background(), completion); err == nil {
		t.Fatal("CompleteTaskMerge(nil store) error = nil")
	}
	if _, err := store.CompleteTaskMerge(context.Background(), completion); !errors.Is(err, application.ErrNotFound) {
		t.Fatalf("CompleteTaskMerge(missing) error = %v", err)
	}
}

func TestTaskMergeStorageHelpersCompareClosedAuthorityExactly(t *testing.T) {
	now := time.Date(2026, time.August, 20, 12, 0, 0, 0, time.UTC)
	approval := domain.MergeApproval{
		TaskHandle: "task-merge", ApprovalID: "approval-request", ManagedRunID: "managed-run-merge",
		MCPOperationID: "merge-operation", ResolvingPrincipal: "operator_a",
		OperationFingerprint: strings.Repeat("a", 64), ApprovedHead: strings.Repeat("b", 40),
		ApprovedAt: now.Add(-time.Minute), ExpiresAt: now.Add(time.Hour), ConsumedAt: now,
		OperatorEnabled: true,
	}
	row := taskMergeRow{
		operationID: "merge-operation", subjectDigest: strings.Repeat("1", 64), taskHandle: approval.TaskHandle,
		managedRunID: approval.ManagedRunID, repositoryID: "repository-merge", pullRequestID: "pull-request-merge",
		branch: "devcrew/task-merge", headRevision: approval.ApprovedHead, evidenceDigest: strings.Repeat("2", 64),
		requiredChecks: []string{"ci/unit"}, state: application.TaskMergeExecutionAuthorized,
		approvalRequestID: approval.ApprovalID, mcpOperationID: approval.MCPOperationID,
		resolvingPrincipalID: approval.ResolvingPrincipal, operationFingerprint: approval.OperationFingerprint,
		approvedAt: approval.ApprovedAt, expiresAt: approval.ExpiresAt, consumedAt: approval.ConsumedAt,
		mergeMethod: application.PullRequestMergeRebase,
		reservedAt:  now.Add(-2 * time.Minute), stateVersion: 2,
	}
	if !taskMergeApprovalMatches(row, approval) {
		t.Fatal("taskMergeApprovalMatches() = false")
	}
	approval.OperatorEnabled = false
	if taskMergeApprovalMatches(row, approval) {
		t.Fatal("taskMergeApprovalMatches(disabled) = true")
	}
	receipt := application.PullRequestMergeReceipt{
		RepositoryID: row.repositoryID, PullRequestID: row.pullRequestID, HeadRevision: row.headRevision,
		MergeCommitRevision: strings.Repeat("c", 40), Method: application.PullRequestMergeRebase,
	}
	if !taskMergeReceiptMatches(row, receipt) {
		t.Fatal("taskMergeReceiptMatches() = false")
	}
	row.mergeCommitRevision, row.mergeMethod = receipt.MergeCommitRevision, receipt.Method
	if !taskMergeCompletionMatches(row, application.TaskMergeCompletion{OperationID: row.operationID, Receipt: receipt, At: now}) {
		t.Fatal("taskMergeCompletionMatches() = false")
	}
	if !sameStrings([]string{"a", "b"}, []string{"a", "b"}) ||
		sameStrings([]string{"a"}, []string{"a", "b"}) || sameStrings([]string{"a", "b"}, []string{"a", "c"}) {
		t.Fatal("sameStrings() did not compare exact order and length")
	}
	if optionalTime(time.Time{}) != "" || optionalTime(now) != formatTime(now) {
		t.Fatal("optionalTime() projection differs")
	}
	if _, err := scanTaskMerge(errorRowScanner{err: errors.New("scan unavailable")}); err == nil {
		t.Fatal("scanTaskMerge(scanner failure) error = nil")
	}
	if err := updateTaskMerge(context.Background(), fixedResultExecer{}, row); err == nil {
		t.Fatal("updateTaskMerge(no changed row) error = nil")
	}
	if err := updateTaskMergeOperation(
		context.Background(), fixedResultExecer{}, row, domain.OperationAccepted, now,
	); err == nil {
		t.Fatal("updateTaskMergeOperation(no changed row) error = nil")
	}
	if !validStoredMergeMethod(application.PullRequestMergeCommit) ||
		!validStoredMergeMethod(application.PullRequestMergeSquash) ||
		!validStoredMergeMethod(application.PullRequestMergeRebase) ||
		validStoredMergeMethod(application.PullRequestMergeMethod("unknown")) {
		t.Fatal("validStoredMergeMethod() accepted the wrong closed vocabulary")
	}
}

type errorRowScanner struct {
	err error
}

func (scanner errorRowScanner) Scan(...any) error {
	return scanner.err
}

type fixedResultExecer struct{}

func (fixedResultExecer) ExecContext(context.Context, string, ...any) (sql.Result, error) {
	return fixedSQLResult{}, nil
}

type fixedSQLResult struct{}

func (fixedSQLResult) LastInsertId() (int64, error) { return 0, nil }
func (fixedSQLResult) RowsAffected() (int64, error) { return 0, nil }
