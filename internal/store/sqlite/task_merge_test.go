package sqlite

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/comisai/comis-dev-crew/internal/application"
	"github.com/comisai/comis-dev-crew/internal/domain"
)

func TestTaskMergeStorePersistsApprovalIntentAndExactCompletionAcrossRestarts(t *testing.T) {
	ctx := context.Background()
	databasePath := filepath.Join(canonicalTempDir(t), "devcrew.db")
	store, reservation, approval, completion := openTaskMergeFixture(t, databasePath, "task-merge-restart")

	pending, err := store.BeginTaskMerge(ctx, reservation)
	if err != nil || pending.State != application.TaskMergeAwaitingApproval ||
		pending.ManagedRunID != approval.Approval.ManagedRunID || pending.Branch != "devcrew/task-evidence" ||
		pending.HeadRevision != completion.Receipt.HeadRevision || pending.StateVersion < 1 {
		t.Fatalf("BeginTaskMerge() = %#v, %v", pending, err)
	}
	accepted, err := store.GetOperation(ctx, reservation.OperationID)
	if err != nil || accepted.Status != domain.OperationAccepted || accepted.StateVersion != pending.StateVersion {
		t.Fatalf("GetOperation(reserved) = %#v, %v", accepted, err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Close(reserved) error = %v", err)
	}

	store = reopenTaskMergeStore(t, databasePath)
	reconciliation, err := store.ReconcileStartup(ctx, reservation.At.Add(time.Second))
	if err != nil || reconciliation.OperationsMarkedUnknown != 0 {
		t.Fatalf("ReconcileStartup(pending merge) = %#v, %v", reconciliation, err)
	}
	pendingReplay, err := store.BeginTaskMerge(ctx, reservation)
	if err != nil || !reflect.DeepEqual(pendingReplay, pending) {
		t.Fatalf("BeginTaskMerge(restart replay) = %#v, %v", pendingReplay, err)
	}
	authorized, err := store.AuthorizeTaskMerge(ctx, approval)
	if err != nil || authorized.State != application.TaskMergeExecutionAuthorized ||
		authorized.Approval.ApprovalID != approval.Approval.ApprovalID ||
		authorized.Method != application.PullRequestMergeSquash ||
		authorized.StateVersion <= pending.StateVersion {
		t.Fatalf("AuthorizeTaskMerge() = %#v, %v", authorized, err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Close(authorized) error = %v", err)
	}

	store = reopenTaskMergeStore(t, databasePath)
	t.Cleanup(func() { _ = store.Close() })
	lateReplay := reservation
	lateReplay.At = approval.Approval.ExpiresAt.Add(time.Hour)
	reconciliation, err = store.ReconcileStartup(ctx, lateReplay.At)
	if err != nil || reconciliation.OperationsMarkedUnknown != 0 {
		t.Fatalf("ReconcileStartup(authorized merge) = %#v, %v", reconciliation, err)
	}
	authorizedReplay, err := store.BeginTaskMerge(ctx, lateReplay)
	if err != nil || !reflect.DeepEqual(authorizedReplay, authorized) {
		t.Fatalf("BeginTaskMerge(authorized restart) = %#v, %v", authorizedReplay, err)
	}
	completion.At = lateReplay.At
	completed, err := store.CompleteTaskMerge(ctx, completion)
	if err != nil || completed.State != application.TaskMergeCompleted ||
		completed.MergeCommitRevision != completion.Receipt.MergeCommitRevision ||
		completed.Method != application.PullRequestMergeSquash || completed.StateVersion <= authorized.StateVersion {
		t.Fatalf("CompleteTaskMerge() = %#v, %v", completed, err)
	}
	completedReplay := completion
	completedReplay.At = completion.At.Add(time.Minute)
	replayed, err := store.CompleteTaskMerge(ctx, completedReplay)
	if err != nil || !reflect.DeepEqual(replayed, completed) {
		t.Fatalf("CompleteTaskMerge(replay) = %#v, %v", replayed, err)
	}
	operation, err := store.GetOperation(ctx, reservation.OperationID)
	if err != nil || operation.Status != domain.OperationCompleted || operation.StateVersion != completed.StateVersion {
		t.Fatalf("GetOperation(completed) = %#v, %v", operation, err)
	}
}

func TestTaskMergeStoreRejectsChangedStaleAndIneligibleReservations(t *testing.T) {
	ctx := context.Background()
	store, reservation, _, _ := openTaskMergeFixture(
		t, filepath.Join(canonicalTempDir(t), "devcrew.db"), "task-merge-boundaries",
	)
	t.Cleanup(func() { _ = store.Close() })
	if _, err := store.BeginTaskMerge(ctx, reservation); err != nil {
		t.Fatalf("BeginTaskMerge() error = %v", err)
	}
	altered := reservation
	altered.SubjectDigest = strings.Repeat("9", 64)
	if _, err := store.BeginTaskMerge(ctx, altered); !errors.Is(err, application.ErrConflict) {
		t.Fatalf("BeginTaskMerge(altered replay) error = %v, want ErrConflict", err)
	}
	stale := reservation
	stale.At = reservation.At.Add(20 * time.Minute)
	if _, err := store.BeginTaskMerge(ctx, stale); !errors.Is(err, application.ErrPrecondition) {
		t.Fatalf("BeginTaskMerge(stale evidence) error = %v, want ErrPrecondition", err)
	}

	ineligible := candidateEvidenceTask(t, "task-merge-ineligible")
	if err := store.CreateTask(ctx, ineligible); err != nil {
		t.Fatalf("CreateTask(ineligible) error = %v", err)
	}
	request := application.TaskMergeReservation{
		OperationID: "merge-operation-ineligible", TaskHandle: ineligible.Handle,
		SubjectDigest: strings.Repeat("8", 64), At: ineligible.UpdatedAt.Add(time.Minute),
	}
	if _, err := store.BeginTaskMerge(ctx, request); !errors.Is(err, application.ErrPrecondition) {
		t.Fatalf("BeginTaskMerge(ineligible task) error = %v, want ErrPrecondition", err)
	}
	if _, err := store.GetOperation(ctx, request.OperationID); !errors.Is(err, application.ErrNotFound) {
		t.Fatalf("GetOperation(ineligible task) error = %v, want ErrNotFound", err)
	}
}

func TestTaskMergeStoreRollsBackEverySplitLedgerFailure(t *testing.T) {
	t.Run("reservation", func(t *testing.T) {
		store, reservation, _, _ := openTaskMergeFixture(
			t, filepath.Join(canonicalTempDir(t), "reservation.db"), "task-merge-fault-reserve",
		)
		defer func() { _ = store.Close() }()
		if _, err := store.db.Exec(`CREATE TRIGGER refuse_task_merge_insert BEFORE INSERT ON task_merges
			BEGIN SELECT RAISE(ABORT, 'injected task merge insert failure'); END`); err != nil {
			t.Fatalf("create reservation fault: %v", err)
		}
		if _, err := store.BeginTaskMerge(context.Background(), reservation); err == nil {
			t.Fatal("BeginTaskMerge(fault) error = nil")
		}
		if _, err := store.GetOperation(context.Background(), reservation.OperationID); !errors.Is(err, application.ErrNotFound) {
			t.Fatalf("GetOperation(after reservation fault) error = %v, want ErrNotFound", err)
		}
	})

	t.Run("authorization", func(t *testing.T) {
		store, reservation, approval, _ := openTaskMergeFixture(
			t, filepath.Join(canonicalTempDir(t), "authorization.db"), "task-merge-fault-authorize",
		)
		defer func() { _ = store.Close() }()
		if _, err := store.BeginTaskMerge(context.Background(), reservation); err != nil {
			t.Fatalf("BeginTaskMerge() error = %v", err)
		}
		if _, err := store.db.Exec(`CREATE TRIGGER refuse_task_merge_authority_ledger BEFORE UPDATE ON operations
			WHEN NEW.command = 'MergeTask' AND NEW.state_version > OLD.state_version
			BEGIN SELECT RAISE(ABORT, 'injected authority ledger failure'); END`); err != nil {
			t.Fatalf("create authorization fault: %v", err)
		}
		if _, err := store.AuthorizeTaskMerge(context.Background(), approval); err == nil {
			t.Fatal("AuthorizeTaskMerge(fault) error = nil")
		}
		row, found, err := findTaskMerge(context.Background(), store.db, reservation.OperationID)
		if err != nil || !found || row.state != application.TaskMergeAwaitingApproval || row.approvalRequestID != "" {
			t.Fatalf("task merge after authorization fault = %#v, %v, found %v", row, err, found)
		}
	})

	t.Run("authorization record", func(t *testing.T) {
		store, reservation, approval, _ := openTaskMergeFixture(
			t, filepath.Join(canonicalTempDir(t), "authorization-record.db"), "task-merge-fault-authorize-record",
		)
		defer func() { _ = store.Close() }()
		if _, err := store.BeginTaskMerge(context.Background(), reservation); err != nil {
			t.Fatalf("BeginTaskMerge() error = %v", err)
		}
		if _, err := store.db.Exec(`CREATE TRIGGER refuse_task_merge_authority_record BEFORE UPDATE ON task_merges
			WHEN NEW.state = 'execution_authorized'
			BEGIN SELECT RAISE(ABORT, 'injected authority record failure'); END`); err != nil {
			t.Fatalf("create authorization record fault: %v", err)
		}
		if _, err := store.AuthorizeTaskMerge(context.Background(), approval); err == nil {
			t.Fatal("AuthorizeTaskMerge(record fault) error = nil")
		}
	})

	t.Run("completion", func(t *testing.T) {
		store, reservation, approval, completion := openTaskMergeFixture(
			t, filepath.Join(canonicalTempDir(t), "completion.db"), "task-merge-fault-complete",
		)
		defer func() { _ = store.Close() }()
		if _, err := store.BeginTaskMerge(context.Background(), reservation); err != nil {
			t.Fatalf("BeginTaskMerge() error = %v", err)
		}
		authorized, err := store.AuthorizeTaskMerge(context.Background(), approval)
		if err != nil {
			t.Fatalf("AuthorizeTaskMerge() error = %v", err)
		}
		if _, err := store.db.Exec(`CREATE TRIGGER refuse_task_merge_completion_ledger BEFORE UPDATE ON operations
			WHEN NEW.command = 'MergeTask' AND NEW.status = 'completed'
			BEGIN SELECT RAISE(ABORT, 'injected completion ledger failure'); END`); err != nil {
			t.Fatalf("create completion fault: %v", err)
		}
		if _, err := store.CompleteTaskMerge(context.Background(), completion); err == nil {
			t.Fatal("CompleteTaskMerge(fault) error = nil")
		}
		row, found, err := findTaskMerge(context.Background(), store.db, reservation.OperationID)
		if err != nil || !found || row.state != application.TaskMergeExecutionAuthorized ||
			row.mergeCommitRevision != "" || row.stateVersion != authorized.StateVersion {
			t.Fatalf("task merge after completion fault = %#v, %v, found %v", row, err, found)
		}
	})

	t.Run("completion record", func(t *testing.T) {
		store, reservation, approval, completion := openTaskMergeFixture(
			t, filepath.Join(canonicalTempDir(t), "completion-record.db"), "task-merge-fault-complete-record",
		)
		defer func() { _ = store.Close() }()
		if _, err := store.BeginTaskMerge(context.Background(), reservation); err != nil {
			t.Fatalf("BeginTaskMerge() error = %v", err)
		}
		if _, err := store.AuthorizeTaskMerge(context.Background(), approval); err != nil {
			t.Fatalf("AuthorizeTaskMerge() error = %v", err)
		}
		if _, err := store.db.Exec(`CREATE TRIGGER refuse_task_merge_completion_record BEFORE UPDATE ON task_merges
			WHEN NEW.state = 'completed'
			BEGIN SELECT RAISE(ABORT, 'injected completion record failure'); END`); err != nil {
			t.Fatalf("create completion record fault: %v", err)
		}
		if _, err := store.CompleteTaskMerge(context.Background(), completion); err == nil {
			t.Fatal("CompleteTaskMerge(record fault) error = nil")
		}
	})
}

func openTaskMergeFixture(
	t *testing.T,
	databasePath, taskHandle string,
) (*Store, application.TaskMergeReservation, application.TaskMergeAuthorization, application.TaskMergeCompletion) {
	t.Helper()
	ctx := context.Background()
	store, err := Open(ctx, databasePath)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	task := candidateEvidenceTask(t, taskHandle)
	task.DeliveryMode = domain.DeliveryMergeAfterApproval
	task, err = task.PinBriefRevision()
	if err != nil {
		_ = store.Close()
		t.Fatalf("PinBriefRevision() error = %v", err)
	}
	if err := store.CreateTask(ctx, task); err != nil {
		_ = store.Close()
		t.Fatalf("CreateTask() error = %v", err)
	}
	head := strings.Repeat("b", 40)
	sealed := candidateEvidence(t, task, head)
	judgedAt := task.UpdatedAt.Add(5 * time.Minute)
	candidate, judgment, err := store.CommitCandidateEvidence(
		ctx, task.Handle, sealed, []string{"unit"}, []string{"ci/unit"}, judgedAt,
		candidateEvidencePublications(t, task, sealed),
	)
	if err != nil || judgment.Outcome != domain.CandidateAccepted {
		_ = store.Close()
		t.Fatalf("CommitCandidateEvidence() = %#v, %v", judgment, err)
	}
	delivering, err := candidate.ApplyTransition(domain.TransitionDeliveryStarted, judgedAt.Add(time.Second))
	if err != nil {
		_ = store.Close()
		t.Fatalf("ApplyTransition(delivering) error = %v", err)
	}
	delivered, err := delivering.ApplyTransition(domain.TransitionDeliveryAccepted, judgedAt.Add(2*time.Second))
	if err != nil {
		_ = store.Close()
		t.Fatalf("ApplyTransition(delivered) error = %v", err)
	}
	transaction, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		_ = store.Close()
		t.Fatalf("BeginTx(delivered fixture) error = %v", err)
	}
	version, err := nextMutationStateVersion(ctx, transaction)
	if err == nil {
		delivered.StateVersion = version
		err = updateTaskState(ctx, transaction, delivered)
	}
	if err == nil {
		err = transaction.Commit()
	} else {
		_ = transaction.Rollback()
	}
	if err != nil {
		_ = store.Close()
		t.Fatalf("persist delivered fixture: %v", err)
	}
	reservedAt := judgedAt.Add(time.Minute)
	operationID := "merge-operation-" + taskHandle
	reservation := application.TaskMergeReservation{
		OperationID: operationID, TaskHandle: taskHandle,
		SubjectDigest: strings.Repeat("1", 64), At: reservedAt,
	}
	approvedAt := reservedAt.Add(30 * time.Second)
	consumedAt := approvedAt.Add(time.Minute)
	approval := application.TaskMergeAuthorization{
		OperationID: operationID, Method: application.PullRequestMergeSquash, At: consumedAt,
		Approval: domain.MergeApproval{
			TaskHandle: taskHandle, ApprovalID: "approval-request-" + taskHandle,
			ManagedRunID: task.ManagedRunID, MCPOperationID: operationID,
			ResolvingPrincipal: "operator_a", OperationFingerprint: strings.Repeat("a", 64),
			ApprovedHead: head, ApprovedAt: approvedAt,
			ExpiresAt: approvedAt.Add(domain.MaximumMergeApprovalTTL), ConsumedAt: consumedAt,
			OperatorEnabled: true,
		},
	}
	completion := application.TaskMergeCompletion{
		OperationID: operationID, At: consumedAt.Add(time.Minute),
		Receipt: application.PullRequestMergeReceipt{
			RepositoryID: task.RepositoryID, PullRequestID: "pull-request-evidence", HeadRevision: head,
			MergeCommitRevision: strings.Repeat("c", 40), Method: application.PullRequestMergeSquash,
		},
	}
	return store, reservation, approval, completion
}

func reopenTaskMergeStore(t *testing.T, databasePath string) *Store {
	t.Helper()
	store, err := Open(context.Background(), databasePath)
	if err != nil {
		t.Fatalf("Open(restart) error = %v", err)
	}
	return store
}
