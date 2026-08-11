package application

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/comisai/comis-dev-crew/internal/domain"
)

func TestCleanupCoordinator_ReleasesHostThenRemovesExactDeliveredWorkspace(t *testing.T) {
	now := time.Date(2026, time.August, 11, 20, 0, 0, 0, time.UTC)
	head := strings.Repeat("b", 40)
	record := cleanupFixtureRecord(head)
	store := &cleanupStoreFixture{record: record}
	workspace := &cleanupWorkspaceFixture{snapshot: cleanupFixtureSnapshot(record, head)}
	forge := &cleanupForgeFixture{truth: PullRequestDeliveryTruth{
		RepositoryID: record.RepositoryID, PullRequestID: record.PullRequestID,
		HeadRevision: head, Checks: []ForgeCheckTruth{{Name: "ci/unit", Conclusion: domain.CheckPassed}},
	}}
	releaser := &cleanupReleaseFixture{}
	remover := &cleanupRemovalFixture{}
	coordinator, err := NewCleanupCoordinator(CleanupCoordinatorConfig{
		Store: store, Workspaces: workspace, Forge: forge, Releaser: releaser,
		Remover: remover, Clock: func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("NewCleanupCoordinator() error = %v", err)
	}
	result, err := coordinator.CleanupTask(context.Background(), CleanupTaskCommand{
		OperationID: record.OperationID, TaskHandle: record.TaskHandle,
	})
	if err != nil {
		t.Fatalf("CleanupTask() error = %v", err)
	}
	if result.Task.State != domain.TaskCleaned || store.beginCalls != 1 || store.releaseCalls != 1 ||
		store.authorizeCalls != 1 || store.completeCalls != 1 || workspace.calls != 2 || forge.calls != 2 ||
		releaser.calls != 1 || remover.calls != 1 {
		t.Fatalf("cleanup flow = result %#v store %#v workspace=%d forge=%d release=%d remove=%d",
			result, store, workspace.calls, forge.calls, releaser.calls, remover.calls)
	}
	wantRelease := ManagedRunReleaseRequest{
		OperationID: record.ReleaseOperationID, ManagedRunID: record.ManagedRunID,
		WorkspaceLeaseID: record.WorkspaceLeaseID, Disposition: ManagedRunReleaseReapSafe,
		ReleasedAt: record.ReleasedAt,
	}
	if !reflect.DeepEqual(releaser.request, wantRelease) {
		t.Fatalf("release request = %#v, want %#v", releaser.request, wantRelease)
	}
	if remover.request.HeadRevision != head || remover.request.WorktreePath != record.WorktreePath ||
		remover.request.PreparationOperationID != record.PreparationOperationID {
		t.Fatalf("removal request = %#v", remover.request)
	}
}

func TestCleanupCoordinator_RefusesDirtyWorkspaceBeforeHostRelease(t *testing.T) {
	head := strings.Repeat("b", 40)
	record := cleanupFixtureRecord(head)
	store := &cleanupStoreFixture{record: record}
	snapshot := cleanupFixtureSnapshot(record, head)
	snapshot.Cleanliness = WorkspaceDirty
	workspace := &cleanupWorkspaceFixture{snapshot: snapshot}
	releaser := &cleanupReleaseFixture{}
	remover := &cleanupRemovalFixture{}
	coordinator, err := NewCleanupCoordinator(CleanupCoordinatorConfig{
		Store: store, Workspaces: workspace, Forge: &cleanupForgeFixture{}, Releaser: releaser,
		Remover: remover, Clock: func() time.Time { return record.ReleasedAt },
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := coordinator.CleanupTask(context.Background(), CleanupTaskCommand{
		OperationID: record.OperationID, TaskHandle: record.TaskHandle,
	}); err == nil {
		t.Fatal("CleanupTask(dirty workspace) error = nil")
	}
	if releaser.calls != 0 || remover.calls != 0 || store.releaseCalls != 0 {
		t.Fatalf("unsafe side effects: release=%d remove=%d recorded=%d", releaser.calls, remover.calls, store.releaseCalls)
	}
}

func cleanupFixtureRecord(head string) TaskCleanupRecord {
	return TaskCleanupRecord{
		OperationID: "cleanup-task-0001", SubjectDigest: strings.Repeat("a", 64),
		TaskHandle: "task-cleanup-0001", PreparationOperationID: "prepare-task-0001",
		ManagedRunID: "managed-run-0001", WorkspaceLeaseID: "workspace-lease-0001",
		RepositoryID: "fixture-repository", WorktreePath: "/approved/worktrees/task-cleanup-0001",
		HeadRevision: head, EvidenceDigest: strings.Repeat("c", 64),
		PullRequestID: "github-pr-17", RequiredForgeChecks: []string{"ci/unit"},
		Stage: CleanupPrepared, ReleaseOperationID: "release-task-0001",
		ReleasedAt: time.Date(2026, time.August, 11, 20, 0, 0, 0, time.UTC),
	}
}

func cleanupFixtureSnapshot(record TaskCleanupRecord, head string) WorkspaceSnapshot {
	return WorkspaceSnapshot{
		TaskHandle: record.TaskHandle, RepositoryID: record.RepositoryID,
		WorktreePath: record.WorktreePath, Branch: "devcrew/task-cleanup",
		HeadRevision: head, Cleanliness: WorkspaceClean,
	}
}

type cleanupStoreFixture struct {
	record                                   TaskCleanupRecord
	beginCalls, releaseCalls, authorizeCalls int
	completeCalls                            int
}

func (store *cleanupStoreFixture) BeginTaskCleanup(_ context.Context, mutation TaskCleanupMutation) (TaskCleanupRecord, error) {
	store.beginCalls++
	store.record.SubjectDigest = mutation.SubjectDigest
	return store.record, nil
}

func (store *cleanupStoreFixture) RecordTaskCleanupHostRelease(_ context.Context, mutation TaskCleanupHostReleaseMutation) (TaskCleanupRecord, error) {
	store.releaseCalls++
	store.record.Stage = CleanupHostReleased
	store.record.Snapshot = mutation.Snapshot
	return store.record, nil
}

func (store *cleanupStoreFixture) AuthorizeTaskCleanupRemoval(_ context.Context, mutation TaskCleanupRemovalAuthorization) (TaskCleanupRecord, error) {
	store.authorizeCalls++
	store.record.Stage = CleanupRemovalAuthorized
	store.record.Snapshot = mutation.Snapshot
	return store.record, nil
}

func (store *cleanupStoreFixture) CompleteTaskCleanup(_ context.Context, _ TaskCleanupCompletion) (MutationResult, error) {
	store.completeCalls++
	task := domain.Task{State: domain.TaskCleaned}
	return MutationResult{Task: task}, nil
}

type cleanupWorkspaceFixture struct {
	snapshot WorkspaceSnapshot
	calls    int
}

func (workspace *cleanupWorkspaceFixture) InspectWorkspace(context.Context, WorkspaceSnapshotRequest) (WorkspaceSnapshot, error) {
	workspace.calls++
	return workspace.snapshot, nil
}

type cleanupForgeFixture struct {
	truth PullRequestDeliveryTruth
	err   error
	calls int
}

func (forge *cleanupForgeFixture) VerifyPullRequestDelivery(context.Context, PullRequestDeliveryVerification) (PullRequestDeliveryTruth, error) {
	forge.calls++
	return forge.truth, forge.err
}

type cleanupReleaseFixture struct {
	request ManagedRunReleaseRequest
	calls   int
}

func (release *cleanupReleaseFixture) ReleaseManagedRun(_ context.Context, request ManagedRunReleaseRequest) (ManagedRunReleaseReceipt, error) {
	release.calls++
	release.request = request
	return ManagedRunReleaseReceipt{
		ManagedRunID: request.ManagedRunID, WorkspaceLeaseID: request.WorkspaceLeaseID,
		Disposition: request.Disposition, ReleasedAt: request.ReleasedAt, State: ManagedRunReleased,
	}, nil
}

type cleanupRemovalFixture struct {
	request DeliveredWorkspaceRemoval
	calls   int
}

func (removal *cleanupRemovalFixture) RemoveDeliveredWorkspace(_ context.Context, request DeliveredWorkspaceRemoval) error {
	removal.calls++
	removal.request = request
	return nil
}
