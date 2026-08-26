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
		MergeMethod: PullRequestMergeSquash, OperatorEnabled: true,
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
	wantEvents := []string{
		"begin", "consume-approval", "persist-approval", "reconcile-forge",
		"revalidate-evidence", "merge-forge", "complete",
	}
	if !reflect.DeepEqual(events, wantEvents) {
		t.Fatalf("events = %#v, want %#v", events, wantEvents)
	}
	if approvals.request.ManagedRunID != store.record.ManagedRunID ||
		approvals.request.MCPOperationID != "merge-operation-0001" {
		t.Fatalf("approval consume request = %#v", approvals.request)
	}
	if forge.reconcileRequest.Method != PullRequestMergeSquash || forge.request.Method != PullRequestMergeSquash {
		t.Fatalf("forge requests lost persisted method: reconcile=%#v merge=%#v", forge.reconcileRequest, forge.request)
	}
}

func TestMergeCoordinator_LeavesCLIRequestPendingWithoutApprovalAuthority(t *testing.T) {
	store := mergeStoreFixture()
	approvals := &mergeApprovalConsumer{}
	forge := &mergeForge{}
	coordinator, err := NewMergeCoordinator(MergeCoordinatorConfig{
		Store: store, Approvals: approvals, Forge: forge,
		Clock:           func() time.Time { return time.Date(2026, time.August, 20, 12, 0, 0, 0, time.UTC) },
		MergeMethod:     PullRequestMergeSquash,
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
				MergeMethod: PullRequestMergeSquash, OperatorEnabled: true,
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
			store.record.Method = PullRequestMergeSquash
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
				Store: store, Approvals: approvals, Forge: forge, MergeMethod: PullRequestMergeSquash,
				Clock: func() time.Time { return now }, OperatorEnabled: true,
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
			if state == TaskMergeExecutionAuthorized && forge.reconcileRequest.Method != store.record.Method {
				t.Fatalf("reconcile request method = %q, want persisted %q", forge.reconcileRequest.Method, store.record.Method)
			}
		})
	}
}

func TestMergeCoordinator_RejectsExpiredAuthorizedReplayBeforeForge(t *testing.T) {
	now := time.Date(2026, time.August, 20, 12, 0, 0, 0, time.UTC)
	store := mergeStoreFixture()
	store.record.State = TaskMergeExecutionAuthorized
	approval := mergeApprovalReceipt(now)
	store.record.Approval = approval.domain(store.record.TaskHandle, store.record.HeadRevision, true)
	store.record.Method = PullRequestMergeSquash
	forge := &mergeForge{}
	coordinator, err := NewMergeCoordinator(MergeCoordinatorConfig{
		Store: store, Approvals: &mergeApprovalConsumer{}, Forge: forge,
		MergeMethod: PullRequestMergeSquash,
		Clock:       func() time.Time { return store.record.Approval.ExpiresAt }, OperatorEnabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = coordinator.MergeTask(context.Background(), MergeTaskCommand{
		OperationID: store.record.OperationID, TaskHandle: store.record.TaskHandle,
		ApprovalRequestID: approval.ApprovalRequestID, MCPOperationID: approval.MCPOperationID,
	})
	if !domain.IsMergeRefusal(err, domain.MergeRefusedApprovalExpired) || forge.calls != 0 || forge.reconcileCalls != 1 {
		t.Fatalf("MergeTask(expired authorized replay) error = %v, forge = %#v", err, forge)
	}
}

func TestMergeCoordinator_ReconcilesExpiredAuthorizedOutcomeWithoutRemerging(t *testing.T) {
	now := time.Date(2026, time.August, 20, 12, 0, 0, 0, time.UTC)
	store := mergeStoreFixture()
	store.record.State = TaskMergeExecutionAuthorized
	approval := mergeApprovalReceipt(now)
	store.record.Approval = approval.domain(store.record.TaskHandle, store.record.HeadRevision, true)
	store.record.Method = PullRequestMergeSquash
	forge := &mergeForge{reconciled: true, reconcileReceipt: PullRequestMergeReceipt{
		RepositoryID: store.record.RepositoryID, PullRequestID: store.record.PullRequestID,
		HeadRevision: store.record.HeadRevision, MergeCommitRevision: strings.Repeat("d", 40),
		Method: PullRequestMergeSquash,
	}}
	coordinator, err := NewMergeCoordinator(MergeCoordinatorConfig{
		Store: store, Approvals: &mergeApprovalConsumer{}, Forge: forge,
		MergeMethod: PullRequestMergeSquash,
		Clock:       func() time.Time { return store.record.Approval.ExpiresAt }, OperatorEnabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := coordinator.MergeTask(context.Background(), MergeTaskCommand{
		OperationID: store.record.OperationID, TaskHandle: store.record.TaskHandle,
		ApprovalRequestID: approval.ApprovalRequestID, MCPOperationID: approval.MCPOperationID,
	})
	if err != nil || result.State != TaskMergeCompleted || forge.calls != 0 || forge.reconcileCalls != 1 {
		t.Fatalf("MergeTask(reconciled expired outcome) = %#v, %v, forge = %#v", result, err, forge)
	}
}

func TestMergeCoordinator_RevalidatesEvidenceAfterOpenOutcomeReconciliation(t *testing.T) {
	now := time.Date(2026, time.August, 20, 12, 0, 0, 0, time.UTC)
	store := mergeStoreFixture()
	approval := mergeApprovalReceipt(now)
	store.record.State = TaskMergeExecutionAuthorized
	store.record.Approval = approval.domain(store.record.TaskHandle, store.record.HeadRevision, true)
	store.record.Method = PullRequestMergeSquash
	store.revalidateErr = ErrPrecondition
	forge := &mergeForge{}
	events := make([]string, 0, 3)
	store.events, forge.events = &events, &events
	coordinator, err := NewMergeCoordinator(MergeCoordinatorConfig{
		Store: store, Approvals: &mergeApprovalConsumer{}, Forge: forge,
		MergeMethod: PullRequestMergeSquash, Clock: func() time.Time { return now }, OperatorEnabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = coordinator.MergeTask(context.Background(), MergeTaskCommand{
		OperationID: store.record.OperationID, TaskHandle: store.record.TaskHandle,
		ApprovalRequestID: approval.ApprovalRequestID, MCPOperationID: approval.MCPOperationID,
	})
	wantEvents := []string{"begin", "reconcile-forge", "revalidate-evidence"}
	if !errors.Is(err, ErrPrecondition) || forge.calls != 0 || store.revalidateCalls != 1 ||
		!reflect.DeepEqual(events, wantEvents) {
		t.Fatalf("MergeTask(stale evidence) error=%v, store=%#v, forge=%#v, events=%#v", err, store, forge, events)
	}
}

func TestMergeCoordinator_CarriesEarliestAuthorityDeadlineToForge(t *testing.T) {
	now := time.Date(2026, time.August, 20, 12, 0, 0, 0, time.UTC)
	store := mergeStoreFixture()
	approval := mergeApprovalReceipt(now)
	store.record.State = TaskMergeExecutionAuthorized
	store.record.Approval = approval.domain(store.record.TaskHandle, store.record.HeadRevision, true)
	store.record.Method = PullRequestMergeSquash
	store.record.EvidenceExpiresAt = now.Add(time.Minute)
	forge := &mergeForge{err: errors.New("stop after observing authority")}
	coordinator, err := NewMergeCoordinator(MergeCoordinatorConfig{
		Store: store, Approvals: &mergeApprovalConsumer{}, Forge: forge,
		MergeMethod: PullRequestMergeSquash, Clock: func() time.Time { return now }, OperatorEnabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = coordinator.MergeTask(context.Background(), MergeTaskCommand{
		OperationID: store.record.OperationID, TaskHandle: store.record.TaskHandle,
		ApprovalRequestID: approval.ApprovalRequestID, MCPOperationID: approval.MCPOperationID,
	})
	if err == nil || forge.calls != 1 || !forge.request.AuthorityExpiresAt.Equal(store.record.EvidenceExpiresAt) {
		t.Fatalf("MergeTask(deadline) error=%v, forge=%#v", err, forge)
	}
}

func TestMergeCoordinator_RejectsAlteredAuthorizedApprovalBeforeForge(t *testing.T) {
	now := time.Date(2026, time.August, 20, 12, 0, 0, 0, time.UTC)
	for _, test := range []struct {
		name    string
		command MergeTaskCommand
	}{
		{name: "missing approval", command: MergeTaskCommand{
			OperationID: "merge-operation-0001", TaskHandle: "task-merge",
		}},
		{name: "different approval", command: MergeTaskCommand{
			OperationID: "merge-operation-0001", TaskHandle: "task-merge",
			ApprovalRequestID: "00000000-0000-4000-8000-000000000002", MCPOperationID: "merge-operation-0001",
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			store := mergeStoreFixture()
			approval := mergeApprovalReceipt(now)
			store.record.State = TaskMergeExecutionAuthorized
			store.record.Approval = approval.domain(store.record.TaskHandle, store.record.HeadRevision, true)
			store.record.Method = PullRequestMergeSquash
			forge := &mergeForge{}
			coordinator, err := NewMergeCoordinator(MergeCoordinatorConfig{
				Store: store, Approvals: &mergeApprovalConsumer{}, Forge: forge,
				MergeMethod: PullRequestMergeSquash, Clock: func() time.Time { return now }, OperatorEnabled: true,
			})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := coordinator.MergeTask(context.Background(), test.command); !errors.Is(err, ErrPrecondition) ||
				forge.reconcileCalls != 0 || forge.calls != 0 || store.revalidateCalls != 0 {
				t.Fatalf("MergeTask(%s) error=%v, store=%#v, forge=%#v", test.name, err, store, forge)
			}
		})
	}
}

type mergeStore struct {
	record          TaskMergeRecord
	events          *[]string
	authorizeCalls  int
	revalidateCalls int
	beginErr        error
	authorizeErr    error
	revalidateErr   error
	completeErr     error
}

func mergeStoreFixture() *mergeStore {
	return &mergeStore{record: TaskMergeRecord{
		OperationID: "merge-operation-0001", SubjectDigest: strings.Repeat("1", 64),
		TaskHandle: "task-merge", ManagedRunID: "managed-run-merge", RepositoryID: "repository-merge",
		PullRequestID: "github-pr-31", Branch: "devcrew/task-merge", HeadRevision: strings.Repeat("a", 40),
		EvidenceDigest:    strings.Repeat("b", 64),
		EvidenceExpiresAt: time.Date(2026, time.August, 20, 13, 0, 0, 0, time.UTC), RequiredChecks: []string{"ci/unit"},
		State: TaskMergeAwaitingApproval, ReservedAt: time.Date(2026, time.August, 20, 11, 0, 0, 0, time.UTC),
		StateVersion: 1,
	}}
}

func (store *mergeStore) BeginTaskMerge(_ context.Context, request TaskMergeReservation) (TaskMergeRecord, error) {
	if store.events != nil {
		*store.events = append(*store.events, "begin")
	}
	store.record.OperationID = request.OperationID
	store.record.SubjectDigest = request.SubjectDigest
	return store.record, store.beginErr
}

func (store *mergeStore) AuthorizeTaskMerge(_ context.Context, request TaskMergeAuthorization) (TaskMergeRecord, error) {
	if store.record.State == TaskMergeExecutionAuthorized {
		store.revalidateCalls++
		if store.events != nil {
			*store.events = append(*store.events, "revalidate-evidence")
		}
		if store.revalidateErr != nil {
			return TaskMergeRecord{}, store.revalidateErr
		}
		return store.record, nil
	}
	store.authorizeCalls++
	if store.events != nil {
		*store.events = append(*store.events, "persist-approval")
	}
	if store.authorizeErr != nil {
		return TaskMergeRecord{}, store.authorizeErr
	}
	store.record.Approval = request.Approval
	store.record.Method = request.Method
	store.record.State = TaskMergeExecutionAuthorized
	store.record.StateVersion++
	return store.record, nil
}

func (store *mergeStore) CompleteTaskMerge(_ context.Context, request TaskMergeCompletion) (TaskMergeRecord, error) {
	if store.events != nil {
		*store.events = append(*store.events, "complete")
	}
	if store.completeErr != nil {
		return TaskMergeRecord{}, store.completeErr
	}
	store.record.State = TaskMergeCompleted
	store.record.MergeCommitRevision = request.Receipt.MergeCommitRevision
	store.record.Method = request.Receipt.Method
	store.record.CompletedAt = request.At
	store.record.StateVersion++
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
	receipt          PullRequestMergeReceipt
	reconcileReceipt PullRequestMergeReceipt
	reconciled       bool
	request          PullRequestMergeRequest
	reconcileRequest PullRequestMergeRequest
	events           *[]string
	calls            int
	reconcileCalls   int
	err              error
	reconcileErr     error
}

func (adapter *mergeForge) ReconcileApprovedPullRequest(
	_ context.Context,
	request PullRequestMergeRequest,
) (PullRequestMergeReceipt, bool, error) {
	adapter.reconcileCalls++
	adapter.reconcileRequest = request
	if adapter.events != nil {
		*adapter.events = append(*adapter.events, "reconcile-forge")
	}
	return adapter.reconcileReceipt, adapter.reconciled, adapter.reconcileErr
}

func (adapter *mergeForge) MergeApprovedPullRequest(
	_ context.Context,
	request PullRequestMergeRequest,
) (PullRequestMergeReceipt, error) {
	adapter.calls++
	adapter.request = request
	if adapter.events != nil {
		*adapter.events = append(*adapter.events, "merge-forge")
	}
	return adapter.receipt, adapter.err
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
