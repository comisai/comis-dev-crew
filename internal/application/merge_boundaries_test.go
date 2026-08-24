package application

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/comisai/comis-dev-crew/internal/domain"
)

func TestMergeCoordinatorRejectsInvalidCompositionContextIdentityAndClock(t *testing.T) {
	now := time.Date(2026, time.August, 20, 12, 0, 0, 0, time.UTC)
	valid := MergeCoordinatorConfig{
		Store: mergeStoreFixture(), Approvals: &mergeApprovalConsumer{}, Forge: &mergeForge{},
		MergeMethod: PullRequestMergeSquash, Clock: func() time.Time { return now }, OperatorEnabled: true,
	}
	for _, test := range []struct {
		name   string
		mutate func(*MergeCoordinatorConfig)
	}{
		{name: "missing store", mutate: func(config *MergeCoordinatorConfig) { config.Store = nil }},
		{name: "missing approvals", mutate: func(config *MergeCoordinatorConfig) { config.Approvals = nil }},
		{name: "missing forge", mutate: func(config *MergeCoordinatorConfig) { config.Forge = nil }},
		{name: "missing merge method", mutate: func(config *MergeCoordinatorConfig) { config.MergeMethod = "" }},
		{name: "missing clock", mutate: func(config *MergeCoordinatorConfig) { config.Clock = nil }},
	} {
		t.Run(test.name, func(t *testing.T) {
			config := valid
			test.mutate(&config)
			if _, err := NewMergeCoordinator(config); err == nil {
				t.Fatal("NewMergeCoordinator() error = nil")
			}
		})
	}
	var absent *MergeCoordinator
	if _, err := absent.MergeTask(context.Background(), MergeTaskCommand{}); err == nil {
		t.Fatal("MergeTask(nil coordinator) error = nil")
	}
	coordinator, err := NewMergeCoordinator(valid)
	if err != nil {
		t.Fatal(err)
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := coordinator.MergeTask(cancelled, MergeTaskCommand{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("MergeTask(cancelled) error = %v", err)
	}
	for _, command := range []MergeTaskCommand{
		{OperationID: "bad operation", TaskHandle: "task-merge"},
		{OperationID: "merge-operation-0001", TaskHandle: "bad task"},
		{OperationID: "merge-operation-0001", TaskHandle: "task-merge", ApprovalRequestID: "approval-only"},
		{OperationID: "merge-operation-0001", TaskHandle: "task-merge", MCPOperationID: "operation-only"},
		{OperationID: "merge-operation-0001", TaskHandle: "task-merge", ApprovalRequestID: "approval", MCPOperationID: "other-operation"},
	} {
		if _, err := coordinator.MergeTask(context.Background(), command); err == nil {
			t.Fatalf("MergeTask(%#v) error = nil", command)
		}
	}
	invalidClock := valid
	invalidClock.Clock = func() time.Time { return time.Time{} }
	coordinator, err = NewMergeCoordinator(invalidClock)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := coordinator.MergeTask(context.Background(), MergeTaskCommand{
		OperationID: "merge-operation-0001", TaskHandle: "task-merge",
	}); err == nil {
		t.Fatal("MergeTask(invalid clock) error = nil")
	}
}

func TestMergeCoordinatorFailsClosedAtEveryExternalBoundary(t *testing.T) {
	now := time.Date(2026, time.August, 20, 12, 0, 0, 0, time.UTC)
	command := MergeTaskCommand{
		OperationID: "merge-operation-0001", TaskHandle: "task-merge",
		ApprovalRequestID: "00000000-0000-4000-8000-000000000001", MCPOperationID: "merge-operation-0001",
	}
	newCoordinator := func(store *mergeStore, approvals *mergeApprovalConsumer, forge *mergeForge, enabled bool) *MergeCoordinator {
		coordinator, err := NewMergeCoordinator(MergeCoordinatorConfig{
			Store: store, Approvals: approvals, Forge: forge,
			MergeMethod: PullRequestMergeSquash, Clock: func() time.Time { return now }, OperatorEnabled: enabled,
		})
		if err != nil {
			t.Fatal(err)
		}
		return coordinator
	}

	store := mergeStoreFixture()
	store.beginErr = errors.New("store unavailable")
	if _, err := newCoordinator(store, &mergeApprovalConsumer{}, &mergeForge{}, true).MergeTask(context.Background(), command); err == nil {
		t.Fatal("MergeTask(begin failure) error = nil")
	}

	store = mergeStoreFixture()
	store.record.RequiredChecks = nil
	if _, err := newCoordinator(store, &mergeApprovalConsumer{}, &mergeForge{}, true).MergeTask(context.Background(), command); err == nil {
		t.Fatal("MergeTask(invalid reservation) error = nil")
	}

	store = mergeStoreFixture()
	if _, err := newCoordinator(store, &mergeApprovalConsumer{err: errors.New("approval unavailable")}, &mergeForge{}, true).
		MergeTask(context.Background(), command); err == nil {
		t.Fatal("MergeTask(approval failure) error = nil")
	}

	for _, receipt := range []MergeApprovalReceipt{
		{State: MergeApprovalState("unknown"), ApprovalRequestID: command.ApprovalRequestID},
		{State: MergeApprovalConsumed, ApprovalRequestID: "different-approval"},
	} {
		store = mergeStoreFixture()
		if _, err := newCoordinator(store, &mergeApprovalConsumer{receipt: receipt}, &mergeForge{}, true).
			MergeTask(context.Background(), command); err == nil {
			t.Fatalf("MergeTask(receipt %#v) error = nil", receipt)
		}
	}

	store = mergeStoreFixture()
	store.authorizeErr = errors.New("authorization store unavailable")
	if _, err := newCoordinator(store, &mergeApprovalConsumer{receipt: mergeApprovalReceipt(now)}, &mergeForge{}, true).
		MergeTask(context.Background(), command); err == nil {
		t.Fatal("MergeTask(authorize failure) error = nil")
	}

	store = mergeStoreFixture()
	store.record.State = TaskMergeExecutionAuthorized
	store.record.Approval = mergeApprovalReceipt(now).domain(store.record.TaskHandle, store.record.HeadRevision, true)
	store.record.Method = PullRequestMergeSquash
	if _, err := newCoordinator(store, &mergeApprovalConsumer{}, &mergeForge{}, false).MergeTask(context.Background(), command); err == nil {
		t.Fatal("MergeTask(disabled) error = nil")
	}

	store = mergeStoreFixture()
	store.record.State = TaskMergeExecutionAuthorized
	store.record.Approval = mergeApprovalReceipt(now).domain(store.record.TaskHandle, store.record.HeadRevision, true)
	store.record.Method = PullRequestMergeSquash
	if _, err := newCoordinator(store, &mergeApprovalConsumer{}, &mergeForge{err: errors.New("forge unavailable")}, true).
		MergeTask(context.Background(), command); err == nil {
		t.Fatal("MergeTask(forge failure) error = nil")
	}

	store = mergeStoreFixture()
	store.record.State = TaskMergeExecutionAuthorized
	store.record.Approval = mergeApprovalReceipt(now).domain(store.record.TaskHandle, store.record.HeadRevision, true)
	store.record.Method = PullRequestMergeSquash
	store.completeErr = errors.New("completion store unavailable")
	forge := &mergeForge{receipt: PullRequestMergeReceipt{
		RepositoryID: store.record.RepositoryID, PullRequestID: store.record.PullRequestID,
		HeadRevision: store.record.HeadRevision, MergeCommitRevision: strings.Repeat("c", 40),
		Method: PullRequestMergeCommit,
	}}
	if _, err := newCoordinator(store, &mergeApprovalConsumer{}, forge, true).MergeTask(context.Background(), command); err == nil {
		t.Fatal("MergeTask(completion failure) error = nil")
	}
}

func TestTaskMergeRecordValidationRejectsEveryAuthorityShapeMismatch(t *testing.T) {
	now := time.Date(2026, time.August, 20, 12, 0, 0, 0, time.UTC)
	valid := mergeStoreFixture().record
	approval := mergeApprovalReceipt(now)
	valid.Approval = approval.domain(valid.TaskHandle, valid.HeadRevision, true)
	valid.State = TaskMergeExecutionAuthorized
	valid.Method = PullRequestMergeSquash
	for _, test := range []struct {
		name   string
		mutate func(*TaskMergeRecord)
	}{
		{name: "operation", mutate: func(record *TaskMergeRecord) { record.OperationID = "other-operation" }},
		{name: "task", mutate: func(record *TaskMergeRecord) { record.TaskHandle = "other-task" }},
		{name: "digest", mutate: func(record *TaskMergeRecord) { record.SubjectDigest = strings.Repeat("9", 64) }},
		{name: "run", mutate: func(record *TaskMergeRecord) { record.ManagedRunID = "bad run" }},
		{name: "repository", mutate: func(record *TaskMergeRecord) { record.RepositoryID = "bad repository" }},
		{name: "pull request", mutate: func(record *TaskMergeRecord) { record.PullRequestID = "bad pull request" }},
		{name: "branch", mutate: func(record *TaskMergeRecord) { record.Branch = "bad branch" }},
		{name: "head", mutate: func(record *TaskMergeRecord) { record.HeadRevision = "bad" }},
		{name: "evidence", mutate: func(record *TaskMergeRecord) { record.EvidenceDigest = "bad" }},
		{name: "evidence expiry", mutate: func(record *TaskMergeRecord) { record.EvidenceExpiresAt = time.Time{} }},
		{name: "checks missing", mutate: func(record *TaskMergeRecord) { record.RequiredChecks = nil }},
		{name: "check blank", mutate: func(record *TaskMergeRecord) { record.RequiredChecks = []string{""} }},
		{name: "check duplicate", mutate: func(record *TaskMergeRecord) { record.RequiredChecks = []string{"ci", "ci"} }},
		{name: "reservation time", mutate: func(record *TaskMergeRecord) { record.ReservedAt = time.Time{} }},
		{name: "state version", mutate: func(record *TaskMergeRecord) { record.StateVersion = 0 }},
		{name: "unknown state", mutate: func(record *TaskMergeRecord) { record.State = TaskMergeState("unknown") }},
		{name: "awaiting carries approval", mutate: func(record *TaskMergeRecord) { record.State = TaskMergeAwaitingApproval }},
		{name: "authorized carries completion", mutate: func(record *TaskMergeRecord) { record.MergeCommitRevision = strings.Repeat("c", 40) }},
		{name: "authorized missing method", mutate: func(record *TaskMergeRecord) { record.Method = "" }},
	} {
		t.Run(test.name, func(t *testing.T) {
			record := valid
			record.RequiredChecks = append([]string(nil), valid.RequiredChecks...)
			test.mutate(&record)
			if err := validateTaskMergeRecord(record, valid.OperationID, valid.TaskHandle, valid.SubjectDigest); err == nil {
				t.Fatal("validateTaskMergeRecord() error = nil")
			}
		})
	}
	completed := valid
	completed.State = TaskMergeCompleted
	completed.MergeCommitRevision = strings.Repeat("c", 40)
	completed.Method = PullRequestMergeRebase
	completed.CompletedAt = now
	if err := validateTaskMergeRecord(completed, completed.OperationID, completed.TaskHandle, completed.SubjectDigest); err != nil {
		t.Fatalf("validateTaskMergeRecord(completed) error = %v", err)
	}
	completed.Method = PullRequestMergeMethod("unknown")
	if err := validateTaskMergeRecord(completed, completed.OperationID, completed.TaskHandle, completed.SubjectDigest); err == nil {
		t.Fatal("validateTaskMergeRecord(unknown method) error = nil")
	}
	if !validPullRequestMergeMethod(PullRequestMergeCommit) ||
		!validPullRequestMergeMethod(PullRequestMergeSquash) ||
		!validPullRequestMergeMethod(PullRequestMergeRebase) {
		t.Fatal("validPullRequestMergeMethod() rejected a closed method")
	}
}

func TestMergeApprovalReceiptProjectsExactDomainAuthority(t *testing.T) {
	now := time.Date(2026, time.August, 20, 12, 0, 0, 0, time.UTC)
	receipt := mergeApprovalReceipt(now)
	approval := receipt.domain("task-merge", strings.Repeat("a", 40), true)
	if approval.ApprovalID != receipt.ApprovalRequestID || approval.ManagedRunID != receipt.ManagedRunID ||
		approval.MCPOperationID != receipt.MCPOperationID || approval.ResolvingPrincipal != receipt.ResolvingPrincipalID ||
		approval.OperationFingerprint != receipt.OperationFingerprint || approval.ApprovedHead != strings.Repeat("a", 40) ||
		!approval.OperatorEnabled {
		t.Fatalf("receipt.domain() = %#v", approval)
	}
	if approval.AuthorizeMerge(domain.MergeAuthorization{
		ObservedHead: approval.ApprovedHead, ManagedRunID: approval.ManagedRunID,
		MCPOperationID: approval.MCPOperationID, Now: now,
	}) != nil {
		t.Fatal("receipt domain authority did not authorize its exact operation")
	}
}
