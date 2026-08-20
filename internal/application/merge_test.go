package application

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/comisai/comis-dev-crew/internal/domain"
)

func TestMergeCoordinator_PersistsApprovalBeforeExactForgeMutation(t *testing.T) {
	now := time.Date(2026, time.August, 20, 12, 0, 0, 0, time.UTC)
	store := mergeStoreFixture()
	approvals := &mergeApprovalConsumer{receipt: mergeApprovalReceipt(now)}
	forge := &mergeForge{receipt: PullRequestMergeReceipt{
		RepositoryID: store.record.RepositoryID, PullRequestID: store.record.PullRequestID,
		HeadRevision: store.record.HeadRevision, MergeCommitRevision: strings.Repeat("c", 40),
		Method: PullRequestMergeSquash,
	}}
	events := make([]string, 0, 4)
	store.events, approvals.events, forge.events = &events, &events, &events
	coordinator, err := NewMergeCoordinator(MergeCoordinatorConfig{
		Store: store, Approvals: approvals, Forge: forge, Clock: func() time.Time { return now },
		OperatorEnabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := coordinator.MergeTask(context.Background(), MergeTaskCommand{
		OperationID: "merge-operation-0001", TaskHandle: store.record.TaskHandle,
		ApprovalRequestID: approvals.receipt.ApprovalRequestID, MCPOperationID: "merge-operation-0001",
	})
	if err != nil {
		t.Fatalf("MergeTask() error = %v", err)
	}
	if result.State != TaskMergeCompleted || result.MergeCommitRevision != forge.receipt.MergeCommitRevision ||
		result.ApprovalRequestID != approvals.receipt.ApprovalRequestID {
		t.Fatalf("MergeTask() = %#v", result)
	}
	wantEvents := []string{"begin", "consume-approval", "persist-approval", "merge-forge", "complete"}
	if !reflect.DeepEqual(events, wantEvents) {
		t.Fatalf("events = %#v, want %#v", events, wantEvents)
	}
	if approvals.request.ManagedRunID != store.record.ManagedRunID ||
		approvals.request.MCPOperationID != "merge-operation-0001" {
		t.Fatalf("approval consume request = %#v", approvals.request)
	}
}

func TestMergeCoordinator_LeavesCLIRequestPendingWithoutApprovalAuthority(t *testing.T) {
	store := mergeStoreFixture()
	approvals := &mergeApprovalConsumer{}
	forge := &mergeForge{}
	coordinator, err := NewMergeCoordinator(MergeCoordinatorConfig{
		Store: store, Approvals: approvals, Forge: forge,
		Clock:           func() time.Time { return time.Date(2026, time.August, 20, 12, 0, 0, 0, time.UTC) },
		OperatorEnabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := coordinator.MergeTask(context.Background(), MergeTaskCommand{
		OperationID: "merge-operation-cli", TaskHandle: store.record.TaskHandle,
	})
	if err != nil || result.State != TaskMergeAwaitingApproval || approvals.calls != 0 || forge.calls != 0 {
		t.Fatalf("MergeTask(CLI) = %#v, approvals=%d, forge=%d, error=%v", result, approvals.calls, forge.calls, err)
	}
}

func TestMergeCoordinator_RefusesExpiredOrMismatchedReceiptBeforeForge(t *testing.T) {
	now := time.Date(2026, time.August, 20, 12, 0, 0, 0, time.UTC)
	for _, test := range []struct {
		name   string
		mutate func(*MergeApprovalReceipt)
	}{
		{name: "expired", mutate: func(receipt *MergeApprovalReceipt) { receipt.ExpiresAt = now }},
		{name: "different run", mutate: func(receipt *MergeApprovalReceipt) { receipt.ManagedRunID = "managed-run-other" }},
		{name: "different operation", mutate: func(receipt *MergeApprovalReceipt) { receipt.MCPOperationID = "merge-operation-other" }},
	} {
		t.Run(test.name, func(t *testing.T) {
			store := mergeStoreFixture()
			receipt := mergeApprovalReceipt(now)
			test.mutate(&receipt)
			approvals := &mergeApprovalConsumer{receipt: receipt}
			forge := &mergeForge{}
			coordinator, err := NewMergeCoordinator(MergeCoordinatorConfig{
				Store: store, Approvals: approvals, Forge: forge, Clock: func() time.Time { return now },
				OperatorEnabled: true,
			})
			if err != nil {
				t.Fatal(err)
			}
			_, err = coordinator.MergeTask(context.Background(), MergeTaskCommand{
				OperationID: "merge-operation-0001", TaskHandle: store.record.TaskHandle,
				ApprovalRequestID: "00000000-0000-4000-8000-000000000001", MCPOperationID: "merge-operation-0001",
			})
			if err == nil || forge.calls != 0 || store.authorizeCalls != 0 {
				t.Fatalf("MergeTask(invalid receipt) error=%v, forge=%d, persisted=%d", err, forge.calls, store.authorizeCalls)
			}
		})
	}
}

func TestMergeCoordinator_ReconcilesDurablyAuthorizedAndCompletedReplays(t *testing.T) {
	now := time.Date(2026, time.August, 20, 12, 0, 0, 0, time.UTC)
	for _, state := range []TaskMergeState{TaskMergeExecutionAuthorized, TaskMergeCompleted} {
		t.Run(string(state), func(t *testing.T) {
			store := mergeStoreFixture()
			store.record.State = state
			approval := mergeApprovalReceipt(now)
			store.record.Approval = approval.domain(store.record.TaskHandle, store.record.HeadRevision, true)
			if state == TaskMergeCompleted {
				store.record.MergeCommitRevision = strings.Repeat("d", 40)
				store.record.Method = PullRequestMergeSquash
				store.record.CompletedAt = now
			}
			approvals := &mergeApprovalConsumer{err: errors.New("must not consume twice")}
			forge := &mergeForge{receipt: PullRequestMergeReceipt{
				RepositoryID: store.record.RepositoryID, PullRequestID: store.record.PullRequestID,
				HeadRevision: store.record.HeadRevision, MergeCommitRevision: strings.Repeat("d", 40),
				Method: PullRequestMergeSquash,
			}}
			coordinator, err := NewMergeCoordinator(MergeCoordinatorConfig{
				Store: store, Approvals: approvals, Forge: forge, Clock: func() time.Time { return now }, OperatorEnabled: true,
			})
			if err != nil {
				t.Fatal(err)
			}
			result, err := coordinator.MergeTask(context.Background(), MergeTaskCommand{
				OperationID: "merge-operation-0001", TaskHandle: store.record.TaskHandle,
				ApprovalRequestID: approval.ApprovalRequestID, MCPOperationID: approval.MCPOperationID,
			})
			if err != nil || result.State != TaskMergeCompleted || approvals.calls != 0 {
				t.Fatalf("MergeTask(%s) = %#v, approval calls=%d, error=%v", state, result, approvals.calls, err)
			}
			wantForgeCalls := 1
			if state == TaskMergeCompleted {
				wantForgeCalls = 0
			}
			if forge.calls != wantForgeCalls {
				t.Fatalf("forge calls = %d, want %d", forge.calls, wantForgeCalls)
			}
		})
	}
}

type mergeStore struct {
	record         TaskMergeRecord
	events         *[]string
	authorizeCalls int
}

func mergeStoreFixture() *mergeStore {
	return &mergeStore{record: TaskMergeRecord{
		OperationID: "merge-operation-0001", SubjectDigest: strings.Repeat("1", 64),
		TaskHandle: "task-merge", ManagedRunID: "managed-run-merge", RepositoryID: "repository-merge",
		PullRequestID: "github-pr-31", Branch: "devcrew/task-merge", HeadRevision: strings.Repeat("a", 40),
		EvidenceDigest: strings.Repeat("b", 64), RequiredChecks: []string{"ci/unit"},
		State: TaskMergeAwaitingApproval,
	}}
}

func (store *mergeStore) BeginTaskMerge(_ context.Context, request TaskMergeReservation) (TaskMergeRecord, error) {
	if store.events != nil {
		*store.events = append(*store.events, "begin")
	}
	store.record.OperationID = request.OperationID
	store.record.SubjectDigest = request.SubjectDigest
	return store.record, nil
}

func (store *mergeStore) AuthorizeTaskMerge(_ context.Context, request TaskMergeAuthorization) (TaskMergeRecord, error) {
	store.authorizeCalls++
	if store.events != nil {
		*store.events = append(*store.events, "persist-approval")
	}
	store.record.Approval = request.Approval
	store.record.State = TaskMergeExecutionAuthorized
	return store.record, nil
}

func (store *mergeStore) CompleteTaskMerge(_ context.Context, request TaskMergeCompletion) (TaskMergeRecord, error) {
	if store.events != nil {
		*store.events = append(*store.events, "complete")
	}
	store.record.State = TaskMergeCompleted
	store.record.MergeCommitRevision = request.Receipt.MergeCommitRevision
	store.record.Method = request.Receipt.Method
	store.record.CompletedAt = request.At
	return store.record, nil
}

type mergeApprovalConsumer struct {
	receipt MergeApprovalReceipt
	request MergeApprovalConsumeRequest
	events  *[]string
	calls   int
	err     error
}

func (consumer *mergeApprovalConsumer) ConsumeMergeApproval(
	_ context.Context,
	request MergeApprovalConsumeRequest,
) (MergeApprovalReceipt, error) {
	consumer.calls++
	consumer.request = request
	if consumer.events != nil {
		*consumer.events = append(*consumer.events, "consume-approval")
	}
	return consumer.receipt, consumer.err
}

type mergeForge struct {
	receipt PullRequestMergeReceipt
	events  *[]string
	calls   int
}

func (adapter *mergeForge) MergeApprovedPullRequest(
	_ context.Context,
	_ PullRequestMergeRequest,
) (PullRequestMergeReceipt, error) {
	adapter.calls++
	if adapter.events != nil {
		*adapter.events = append(*adapter.events, "merge-forge")
	}
	return adapter.receipt, nil
}

func mergeApprovalReceipt(now time.Time) MergeApprovalReceipt {
	approvedAt := now.Add(-time.Minute)
	return MergeApprovalReceipt{
		State: MergeApprovalConsumed, ApprovalRequestID: "00000000-0000-4000-8000-000000000001",
		ManagedRunID: "managed-run-merge", MCPOperationID: "merge-operation-0001",
		ResolvingPrincipalID: "principal-merge", OperationFingerprint: strings.Repeat("f", 64),
		ApprovedAt: approvedAt, ExpiresAt: approvedAt.Add(domain.MaximumMergeApprovalTTL), ConsumedAt: now,
	}
}
