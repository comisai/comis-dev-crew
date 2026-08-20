package sqlite

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/comisai/comis-dev-crew/internal/application"
	"github.com/comisai/comis-dev-crew/internal/domain"
)

func TestTaskMergeStoreRejectsInvalidContextInputAndMissingTransactions(t *testing.T) {
	store, reservation, authorization, completion := openTaskMergeFixture(
		t, filepath.Join(canonicalTempDir(t), "boundaries.db"), "task-merge-boundary-input",
	)
	t.Cleanup(func() { _ = store.Close() })
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
	if _, err := store.AuthorizeTaskMerge(context.Background(), application.TaskMergeAuthorization{}); err == nil {
		t.Fatal("AuthorizeTaskMerge(invalid) error = nil")
	}
	//lint:ignore SA1012 The store boundary rejects nil before touching SQLite.
	if _, err := store.CompleteTaskMerge(nil, completion); err == nil {
		t.Fatal("CompleteTaskMerge(nil context) error = nil")
	}
	if _, err := (*Store)(nil).CompleteTaskMerge(context.Background(), completion); err == nil {
		t.Fatal("CompleteTaskMerge(nil store) error = nil")
	}
	if _, err := store.CompleteTaskMerge(context.Background(), application.TaskMergeCompletion{}); err == nil {
		t.Fatal("CompleteTaskMerge(invalid) error = nil")
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := store.BeginTaskMerge(cancelled, reservation); !errors.Is(err, context.Canceled) {
		t.Fatalf("BeginTaskMerge(cancelled) error = %v", err)
	}
	if _, err := store.AuthorizeTaskMerge(cancelled, authorization); !errors.Is(err, context.Canceled) {
		t.Fatalf("AuthorizeTaskMerge(cancelled) error = %v", err)
	}
	if _, err := store.CompleteTaskMerge(cancelled, completion); !errors.Is(err, context.Canceled) {
		t.Fatalf("CompleteTaskMerge(cancelled) error = %v", err)
	}
	if _, err := store.AuthorizeTaskMerge(context.Background(), authorization); !errors.Is(err, application.ErrNotFound) {
		t.Fatalf("AuthorizeTaskMerge(missing) error = %v", err)
	}
	if _, err := store.CompleteTaskMerge(context.Background(), completion); !errors.Is(err, application.ErrNotFound) {
		t.Fatalf("CompleteTaskMerge(missing) error = %v", err)
	}
}

func TestTaskMergeStoreRefusesAlteredApprovalAndCompletionReplays(t *testing.T) {
	ctx := context.Background()
	store, reservation, authorization, completion := openTaskMergeFixture(
		t, filepath.Join(canonicalTempDir(t), "replays.db"), "task-merge-boundary-replay",
	)
	t.Cleanup(func() { _ = store.Close() })
	if _, err := store.BeginTaskMerge(ctx, reservation); err != nil {
		t.Fatal(err)
	}
	for _, mutate := range []func(*application.TaskMergeAuthorization){
		func(request *application.TaskMergeAuthorization) { request.Approval.TaskHandle = "other-task" },
		func(request *application.TaskMergeAuthorization) { request.Approval.ManagedRunID = "other-run" },
		func(request *application.TaskMergeAuthorization) { request.Approval.MCPOperationID = "other-operation" },
		func(request *application.TaskMergeAuthorization) {
			request.Approval.ApprovedHead = strings.Repeat("e", 40)
		},
		func(request *application.TaskMergeAuthorization) { request.Approval.OperatorEnabled = false },
		func(request *application.TaskMergeAuthorization) { request.At = request.Approval.ExpiresAt },
	} {
		changed := authorization
		mutate(&changed)
		if _, err := store.AuthorizeTaskMerge(ctx, changed); !errors.Is(err, application.ErrPrecondition) {
			t.Fatalf("AuthorizeTaskMerge(altered authority) error = %v", err)
		}
	}
	authorized, err := store.AuthorizeTaskMerge(ctx, authorization)
	if err != nil || authorized.State != application.TaskMergeExecutionAuthorized {
		t.Fatalf("AuthorizeTaskMerge() = %#v, %v", authorized, err)
	}
	alteredAuthorization := authorization
	alteredAuthorization.Approval.ResolvingPrincipal = "operator_b"
	if _, err := store.AuthorizeTaskMerge(ctx, alteredAuthorization); !errors.Is(err, application.ErrConflict) {
		t.Fatalf("AuthorizeTaskMerge(altered replay) error = %v", err)
	}
	tooEarly := completion
	tooEarly.At = authorization.Approval.ConsumedAt.Add(-time.Second)
	if _, err := store.CompleteTaskMerge(ctx, tooEarly); !errors.Is(err, application.ErrPrecondition) {
		t.Fatalf("CompleteTaskMerge(too early) error = %v", err)
	}
	for _, mutate := range []func(*application.TaskMergeCompletion){
		func(request *application.TaskMergeCompletion) { request.Receipt.RepositoryID = "other-repository" },
		func(request *application.TaskMergeCompletion) { request.Receipt.PullRequestID = "other-pull-request" },
		func(request *application.TaskMergeCompletion) { request.Receipt.HeadRevision = strings.Repeat("e", 40) },
	} {
		changed := completion
		mutate(&changed)
		if _, err := store.CompleteTaskMerge(ctx, changed); !errors.Is(err, application.ErrPrecondition) {
			t.Fatalf("CompleteTaskMerge(altered authority) error = %v", err)
		}
	}
	completed, err := store.CompleteTaskMerge(ctx, completion)
	if err != nil || completed.State != application.TaskMergeCompleted {
		t.Fatalf("CompleteTaskMerge() = %#v, %v", completed, err)
	}
	changedCompletion := completion
	changedCompletion.Receipt.MergeCommitRevision = strings.Repeat("d", 40)
	if _, err := store.CompleteTaskMerge(ctx, changedCompletion); !errors.Is(err, application.ErrConflict) {
		t.Fatalf("CompleteTaskMerge(altered replay) error = %v", err)
	}
}

func TestTaskMergeStoreRejectsOperationCollisionAndCorruptDurableRows(t *testing.T) {
	t.Run("operation collision", func(t *testing.T) {
		store, reservation, _, _ := openTaskMergeFixture(
			t, filepath.Join(canonicalTempDir(t), "collision.db"), "task-merge-operation-collision",
		)
		defer func() { _ = store.Close() }()
		operation := storeOperation(reservation.OperationID, 1)
		if err := store.RecordOperation(context.Background(), operation); err != nil {
			t.Fatal(err)
		}
		if _, err := store.BeginTaskMerge(context.Background(), reservation); !errors.Is(err, application.ErrConflict) {
			t.Fatalf("BeginTaskMerge(operation collision) error = %v", err)
		}
	})

	for _, test := range []struct {
		name      string
		statement string
		arguments []any
	}{
		{name: "checks syntax", statement: `UPDATE task_merges SET required_checks_json = '{'`},
		{name: "checks empty", statement: `UPDATE task_merges SET required_checks_json = '[]'`},
		{name: "reservation time", statement: `UPDATE task_merges SET reserved_at = 'invalid'`},
		{name: "approval time", statement: `UPDATE task_merges SET approved_at = 'invalid'`},
		{name: "expiry time", statement: `UPDATE task_merges SET expires_at = 'invalid'`},
		{name: "consumed time", statement: `UPDATE task_merges SET consumed_at = 'invalid'`},
		{name: "completion time", statement: `UPDATE task_merges SET completed_at = 'invalid'`},
		{name: "ledger version", statement: `UPDATE operations SET state_version = state_version + 1 WHERE command = 'MergeTask'`},
	} {
		t.Run(test.name, func(t *testing.T) {
			store, reservation, _, _ := openTaskMergeFixture(
				t, filepath.Join(canonicalTempDir(t), test.name+".db"), "task-merge-corrupt-"+strings.ReplaceAll(test.name, " ", "-"),
			)
			defer func() { _ = store.Close() }()
			if _, err := store.BeginTaskMerge(context.Background(), reservation); err != nil {
				t.Fatal(err)
			}
			if _, err := store.db.Exec(test.statement, test.arguments...); err != nil {
				t.Fatal(err)
			}
			if _, err := store.BeginTaskMerge(context.Background(), reservation); err == nil {
				t.Fatal("BeginTaskMerge(corrupt row) error = nil")
			}
		})
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
		reservedAt: now.Add(-2 * time.Minute), stateVersion: 2,
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
