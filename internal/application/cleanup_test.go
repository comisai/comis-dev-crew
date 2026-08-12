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
	_, cleanupErr := coordinator.CleanupTask(context.Background(), CleanupTaskCommand{
		OperationID: record.OperationID, TaskHandle: record.TaskHandle,
	})
	var failure *domain.Failure
	if !errors.As(cleanupErr, &failure) || failure.Code != domain.ErrorPrecondition {
		t.Fatalf("CleanupTask(dirty workspace) error = %v, want precondition failure", cleanupErr)
	}
	if cleanupErr == nil {
		t.Fatal("CleanupTask(dirty workspace) error = nil")
	}
	if releaser.calls != 0 || remover.calls != 0 || store.releaseCalls != 0 {
		t.Fatalf("unsafe side effects: release=%d remove=%d recorded=%d", releaser.calls, remover.calls, store.releaseCalls)
	}
}

func TestCleanupCoordinator_ClassifiesOperatorActionableDependencyFailures(t *testing.T) {
	now := time.Date(2026, time.August, 11, 20, 30, 0, 0, time.UTC)
	head := strings.Repeat("b", 40)
	tests := []struct {
		name         string
		workspaceErr error
		forgeErr     error
		wantMessage  string
		wantHint     string
	}{
		{
			name: "workspace inspection", workspaceErr: errors.New("private workspace detail"),
			wantMessage: "cleanup workspace inspection failed",
			wantHint:    "inspect the exact task worktree and configured repository before retrying",
		},
		{
			name: "pull request verification", forgeErr: errors.New("private forge detail"),
			wantMessage: "cleanup pull request verification failed",
			wantHint:    "inspect the recorded pull request, head, and required checks before retrying",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			record := cleanupFixtureRecord(head)
			workspace := &cleanupWorkspaceFixture{snapshot: cleanupFixtureSnapshot(record, head), err: test.workspaceErr}
			forge := &cleanupForgeFixture{truth: PullRequestDeliveryTruth{
				RepositoryID: record.RepositoryID, PullRequestID: record.PullRequestID,
				HeadRevision: head, Checks: []ForgeCheckTruth{{Name: "ci/unit", Conclusion: domain.CheckPassed}},
			}, err: test.forgeErr}
			coordinator, err := NewCleanupCoordinator(CleanupCoordinatorConfig{
				Store: &cleanupStoreFixture{record: record}, Workspaces: workspace, Forge: forge,
				Releaser: &cleanupReleaseFixture{}, Remover: &cleanupRemovalFixture{},
				Clock: func() time.Time { return now },
			})
			if err != nil {
				t.Fatal(err)
			}
			_, cleanupErr := coordinator.CleanupTask(context.Background(), CleanupTaskCommand{
				OperationID: record.OperationID, TaskHandle: record.TaskHandle,
			})
			var failure *domain.Failure
			if !errors.As(cleanupErr, &failure) || failure.Code != domain.ErrorUnavailable || !failure.Retryable ||
				failure.Message != test.wantMessage || failure.Hint != test.wantHint {
				t.Fatalf("CleanupTask() error = %#v, want actionable dependency failure", cleanupErr)
			}
		})
	}
}

func TestCleanupCoordinator_FailsClosedAcrossCleanupBoundaries(t *testing.T) {
	now := time.Date(2026, time.August, 11, 20, 0, 0, 0, time.UTC)
	head := strings.Repeat("b", 40)
	newCoordinator := func(record TaskCleanupRecord) (*CleanupCoordinator, *cleanupStoreFixture, *cleanupWorkspaceFixture, *cleanupForgeFixture, *cleanupReleaseFixture, *cleanupRemovalFixture) {
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
			t.Fatal(err)
		}
		return coordinator, store, workspace, forge, releaser, remover
	}
	command := CleanupTaskCommand{OperationID: "cleanup-task-0001", TaskHandle: "task-cleanup-0001"}
	runFailure := func(t *testing.T, coordinator *CleanupCoordinator) {
		t.Helper()
		if _, err := coordinator.CleanupTask(context.Background(), command); err == nil {
			t.Fatal("CleanupTask() error = nil")
		}
	}

	if _, err := NewCleanupCoordinator(CleanupCoordinatorConfig{}); err == nil {
		t.Fatal("NewCleanupCoordinator(incomplete) error = nil")
	}
	var nilCoordinator *CleanupCoordinator
	runFailure(t, nilCoordinator)
	coordinator, _, _, _, _, _ := newCoordinator(cleanupFixtureRecord(head))
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := coordinator.CleanupTask(canceled, command); !errors.Is(err, context.Canceled) {
		t.Fatalf("CleanupTask(canceled) error = %v", err)
	}
	if _, err := coordinator.CleanupTask(context.Background(), CleanupTaskCommand{}); err == nil {
		t.Fatal("CleanupTask(invalid) error = nil")
	}

	t.Run("begin failure", func(t *testing.T) {
		coordinator, store, _, _, _, _ := newCoordinator(cleanupFixtureRecord(head))
		store.beginErr = errors.New("store unavailable")
		runFailure(t, coordinator)
	})
	t.Run("durable identity mismatch", func(t *testing.T) {
		record := cleanupFixtureRecord(head)
		record.OperationID = "cleanup-task-other"
		coordinator, _, _, _, _, _ := newCoordinator(record)
		runFailure(t, coordinator)
	})
	t.Run("release failure", func(t *testing.T) {
		coordinator, _, _, _, releaser, _ := newCoordinator(cleanupFixtureRecord(head))
		releaser.err = errors.New("release unavailable")
		runFailure(t, coordinator)
	})
	t.Run("release receipt mismatch", func(t *testing.T) {
		coordinator, _, _, _, releaser, _ := newCoordinator(cleanupFixtureRecord(head))
		releaser.receipt = &ManagedRunReleaseReceipt{State: ManagedRunReleased}
		runFailure(t, coordinator)
	})
	t.Run("release record failure", func(t *testing.T) {
		coordinator, store, _, _, _, _ := newCoordinator(cleanupFixtureRecord(head))
		store.releaseErr = errors.New("release record unavailable")
		runFailure(t, coordinator)
	})
	t.Run("workspace inspection failure", func(t *testing.T) {
		coordinator, _, workspace, _, _, _ := newCoordinator(cleanupFixtureRecord(head))
		workspace.err = errors.New("workspace unavailable")
		runFailure(t, coordinator)
	})
	t.Run("forge verification failure", func(t *testing.T) {
		coordinator, _, _, forge, _, _ := newCoordinator(cleanupFixtureRecord(head))
		forge.err = errors.New("forge unavailable")
		runFailure(t, coordinator)
	})
	t.Run("forge truth mismatch", func(t *testing.T) {
		coordinator, _, _, forge, _, _ := newCoordinator(cleanupFixtureRecord(head))
		forge.truth.HeadRevision = strings.Repeat("d", 40)
		runFailure(t, coordinator)
	})
	t.Run("authorization record failure", func(t *testing.T) {
		coordinator, store, _, _, _, _ := newCoordinator(cleanupFixtureRecord(head))
		store.authorizeErr = errors.New("authorization record unavailable")
		runFailure(t, coordinator)
	})
	t.Run("workspace removal failure", func(t *testing.T) {
		coordinator, _, _, _, _, remover := newCoordinator(cleanupFixtureRecord(head))
		remover.err = errors.New("removal unavailable")
		runFailure(t, coordinator)
	})
	t.Run("unknown durable stage", func(t *testing.T) {
		record := cleanupFixtureRecord(head)
		record.Stage = TaskCleanupStage("invented")
		coordinator, _, _, _, _, _ := newCoordinator(record)
		runFailure(t, coordinator)
	})
	t.Run("completed replay", func(t *testing.T) {
		record := cleanupFixtureRecord(head)
		record.Stage = CleanupCompleted
		coordinator, store, _, _, _, _ := newCoordinator(record)
		if result, err := coordinator.CleanupTask(context.Background(), command); err != nil || result.Task.State != domain.TaskCleaned || store.completeCalls != 1 {
			t.Fatalf("CleanupTask(completed) = %#v, %v", result, err)
		}
	})
	t.Run("report artifact delivery", func(t *testing.T) {
		record := cleanupFixtureRecord(head)
		record.PullRequestID = ""
		record.RequiredForgeChecks = nil
		record.ReportArtifactHash = strings.Repeat("e", 64)
		coordinator, _, _, forge, _, _ := newCoordinator(record)
		if _, err := coordinator.CleanupTask(context.Background(), command); err != nil || forge.calls != 0 {
			t.Fatalf("CleanupTask(report) error = %v forge calls = %d", err, forge.calls)
		}
	})
	t.Run("missing report artifact", func(t *testing.T) {
		record := cleanupFixtureRecord(head)
		record.PullRequestID = ""
		record.RequiredForgeChecks = nil
		coordinator, _, _, _, _, _ := newCoordinator(record)
		runFailure(t, coordinator)
	})
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
	beginErr, releaseErr, authorizeErr       error
}

func (store *cleanupStoreFixture) BeginTaskCleanup(_ context.Context, mutation TaskCleanupMutation) (TaskCleanupRecord, error) {
	store.beginCalls++
	if store.beginErr != nil {
		return TaskCleanupRecord{}, store.beginErr
	}
	store.record.SubjectDigest = mutation.SubjectDigest
	return store.record, nil
}

func (store *cleanupStoreFixture) RecordTaskCleanupHostRelease(_ context.Context, mutation TaskCleanupHostReleaseMutation) (TaskCleanupRecord, error) {
	store.releaseCalls++
	if store.releaseErr != nil {
		return TaskCleanupRecord{}, store.releaseErr
	}
	store.record.Stage = CleanupHostReleased
	store.record.Snapshot = mutation.Snapshot
	return store.record, nil
}

func (store *cleanupStoreFixture) AuthorizeTaskCleanupRemoval(_ context.Context, mutation TaskCleanupRemovalAuthorization) (TaskCleanupRecord, error) {
	store.authorizeCalls++
	if store.authorizeErr != nil {
		return TaskCleanupRecord{}, store.authorizeErr
	}
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
	err      error
}

func (workspace *cleanupWorkspaceFixture) InspectWorkspace(context.Context, WorkspaceSnapshotRequest) (WorkspaceSnapshot, error) {
	workspace.calls++
	if workspace.err != nil {
		return WorkspaceSnapshot{}, workspace.err
	}
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
	receipt *ManagedRunReleaseReceipt
	err     error
}

func (release *cleanupReleaseFixture) ReleaseManagedRun(_ context.Context, request ManagedRunReleaseRequest) (ManagedRunReleaseReceipt, error) {
	release.calls++
	release.request = request
	if release.err != nil {
		return ManagedRunReleaseReceipt{}, release.err
	}
	if release.receipt != nil {
		return *release.receipt, nil
	}
	return ManagedRunReleaseReceipt{
		ManagedRunID: request.ManagedRunID, WorkspaceLeaseID: request.WorkspaceLeaseID,
		Disposition: request.Disposition, ReleasedAt: request.ReleasedAt, State: ManagedRunReleased,
	}, nil
}

type cleanupRemovalFixture struct {
	request DeliveredWorkspaceRemoval
	calls   int
	err     error
}

func (removal *cleanupRemovalFixture) RemoveDeliveredWorkspace(_ context.Context, request DeliveredWorkspaceRemoval) error {
	removal.calls++
	removal.request = request
	return removal.err
}
