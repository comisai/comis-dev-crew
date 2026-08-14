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
	attachments := &cleanupAttachmentReleaseFixture{}
	remover := &cleanupRemovalFixture{}
	coordinator, err := NewCleanupCoordinator(CleanupCoordinatorConfig{
		Store: store, Workspaces: workspace, Forge: forge, Releaser: releaser,
		Attachments: attachments, Remover: remover, Clock: func() time.Time { return now },
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
		releaser.calls != 1 || attachments.calls != 1 || remover.calls != 1 {
		t.Fatalf("cleanup flow = result %#v store %#v workspace=%d forge=%d release=%d attachments=%d remove=%d",
			result, store, workspace.calls, forge.calls, releaser.calls, attachments.calls, remover.calls)
	}
	wantRelease := ManagedRunReleaseRequest{
		OperationID: record.ReleaseOperationID, ManagedRunID: record.ManagedRunID,
		WorkspaceLeaseID: record.WorkspaceLeaseID, Disposition: ManagedRunReleaseReapSafe,
		ReleasedAt: record.ReleasedAt,
	}
	if !reflect.DeepEqual(releaser.request, wantRelease) {
		t.Fatalf("release request = %#v, want %#v", releaser.request, wantRelease)
	}
	if attachments.taskHandle != record.TaskHandle {
		t.Fatalf("attachment release task = %q, want %q", attachments.taskHandle, record.TaskHandle)
	}
	if remover.request.HeadRevision != head || remover.request.WorktreePath != record.WorktreePath ||
		remover.request.PreparationOperationID != record.PreparationOperationID {
		t.Fatalf("removal request = %#v", remover.request)
	}
}

func TestCleanupCoordinator_StopsBeforeHostReleaseRecordWhenAttachmentReleaseFails(t *testing.T) {
	record := cleanupFixtureRecord(strings.Repeat("b", 40))
	store := &cleanupStoreFixture{record: record}
	attachments := &cleanupAttachmentReleaseFixture{err: errors.New("attachment unavailable")}
	coordinator, err := NewCleanupCoordinator(CleanupCoordinatorConfig{
		Store: store,
		Workspaces: &cleanupWorkspaceFixture{snapshot: cleanupFixtureSnapshot(record, record.HeadRevision)},
		Forge: &cleanupForgeFixture{truth: PullRequestDeliveryTruth{
			RepositoryID: record.RepositoryID, PullRequestID: record.PullRequestID,
			HeadRevision: record.HeadRevision, Checks: []ForgeCheckTruth{{Name: "ci/unit", Conclusion: domain.CheckPassed}},
		}},
		Releaser: &cleanupReleaseFixture{}, Attachments: attachments, Remover: &cleanupRemovalFixture{},
		Clock: func() time.Time { return record.ReleasedAt },
	})
	if err != nil {
		t.Fatal(err)
	}
	_, cleanupErr := coordinator.CleanupTask(context.Background(), CleanupTaskCommand{
		OperationID: record.OperationID, TaskHandle: record.TaskHandle,
	})
	var failure *domain.Failure
	if !errors.As(cleanupErr, &failure) || failure.Code != domain.ErrorUnavailable || !failure.Retryable ||
		failure.Message != "runtime attachment release failed" ||
		failure.Hint != "inspect the exact task runtime attachment before retrying cleanup" {
		t.Fatalf("CleanupTask(attachment release) error = %#v", cleanupErr)
	}
	if attachments.calls != 1 || store.releaseCalls != 0 || store.authorizeCalls != 0 || store.completeCalls != 0 {
		t.Fatalf("attachment failure advanced cleanup: attachments=%d store=%#v", attachments.calls, store)
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
	if !errors.As(cleanupErr, &failure) || failure.Code != domain.ErrorPrecondition || !failure.Retryable ||
		failure.Message != CleanupDirtyWorkspaceMessage ||
		failure.Hint != "remove uncommitted changes from the exact task worktree, then retry cleanup" {
		t.Fatalf("CleanupTask(dirty workspace) error = %v, want precondition failure", cleanupErr)
	}
	if cleanupErr == nil {
		t.Fatal("CleanupTask(dirty workspace) error = nil")
	}
	if releaser.calls != 0 || remover.calls != 0 || store.releaseCalls != 0 {
		t.Fatalf("unsafe side effects: release=%d remove=%d recorded=%d", releaser.calls, remover.calls, store.releaseCalls)
	}
}

func TestCleanupCoordinator_ReportsOpenHoldBeforeHostRelease(t *testing.T) {
	record := cleanupFixtureRecord(strings.Repeat("b", 40))
	store := &cleanupStoreFixture{record: record, beginErr: ErrCleanupOpenHold}
	releaser := &cleanupReleaseFixture{}
	remover := &cleanupRemovalFixture{}
	coordinator, err := NewCleanupCoordinator(CleanupCoordinatorConfig{
		Store: store, Workspaces: &cleanupWorkspaceFixture{}, Forge: &cleanupForgeFixture{},
		Releaser: releaser, Remover: remover, Clock: func() time.Time { return record.ReleasedAt },
	})
	if err != nil {
		t.Fatal(err)
	}
	_, cleanupErr := coordinator.CleanupTask(context.Background(), CleanupTaskCommand{
		OperationID: record.OperationID, TaskHandle: record.TaskHandle,
	})
	var failure *domain.Failure
	if !errors.As(cleanupErr, &failure) || failure.Code != domain.ErrorPrecondition || !failure.Retryable ||
		failure.Message != CleanupOpenHoldMessage ||
		failure.Hint != "close the exact task cleanup hold, then retry cleanup" {
		t.Fatalf("CleanupTask(open hold) error = %v, want reason-coded precondition failure", cleanupErr)
	}
	if releaser.calls != 0 || remover.calls != 0 || store.releaseCalls != 0 {
		t.Fatalf("unsafe side effects: release=%d remove=%d recorded=%d", releaser.calls, remover.calls, store.releaseCalls)
	}
}

func TestCleanupCoordinator_ReportsUnresolvedDecisionBeforeHostRelease(t *testing.T) {
	record := cleanupFixtureRecord(strings.Repeat("b", 40))
	store := &cleanupStoreFixture{record: record, beginErr: ErrCleanupOpenDecision}
	releaser := &cleanupReleaseFixture{}
	remover := &cleanupRemovalFixture{}
	coordinator, err := NewCleanupCoordinator(CleanupCoordinatorConfig{
		Store: store, Workspaces: &cleanupWorkspaceFixture{}, Forge: &cleanupForgeFixture{},
		Releaser: releaser, Remover: remover, Clock: func() time.Time { return record.ReleasedAt },
	})
	if err != nil {
		t.Fatal(err)
	}
	_, cleanupErr := coordinator.CleanupTask(context.Background(), CleanupTaskCommand{
		OperationID: record.OperationID, TaskHandle: record.TaskHandle,
	})
	var failure *domain.Failure
	if !errors.As(cleanupErr, &failure) || failure.Code != domain.ErrorPrecondition || !failure.Retryable ||
		failure.Message != CleanupOpenDecisionMessage ||
		failure.Hint != "resolve the exact open task decision, then retry cleanup" {
		t.Fatalf("CleanupTask(unresolved decision) error = %v, want reason-coded precondition failure", cleanupErr)
	}
	if releaser.calls != 0 || remover.calls != 0 || store.releaseCalls != 0 {
		t.Fatalf("unsafe side effects: release=%d remove=%d recorded=%d", releaser.calls, remover.calls, store.releaseCalls)
	}
}

func TestCleanupCoordinator_ReportsExecutionBlockersBeforeHostRelease(t *testing.T) {
	tests := []struct {
		name        string
		cause       error
		wantMessage string
		wantHint    string
	}{
		{
			name: "active execution", cause: ErrCleanupActiveExecution,
			wantMessage: CleanupActiveExecutionMessage,
			wantHint:    "wait for the exact task execution and validation processes to settle, then retry cleanup",
		},
		{
			name: "unknown execution", cause: ErrCleanupUnknownExecution,
			wantMessage: CleanupUnknownExecutionMessage,
			wantHint:    "reconcile the exact terminal, managed run, and workspace lease before retrying cleanup",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			record := cleanupFixtureRecord(strings.Repeat("b", 40))
			store := &cleanupStoreFixture{record: record, beginErr: test.cause}
			releaser := &cleanupReleaseFixture{}
			remover := &cleanupRemovalFixture{}
			coordinator, err := NewCleanupCoordinator(CleanupCoordinatorConfig{
				Store: store, Workspaces: &cleanupWorkspaceFixture{}, Forge: &cleanupForgeFixture{},
				Releaser: releaser, Remover: remover, Clock: func() time.Time { return record.ReleasedAt },
			})
			if err != nil {
				t.Fatal(err)
			}
			_, cleanupErr := coordinator.CleanupTask(context.Background(), CleanupTaskCommand{
				OperationID: record.OperationID, TaskHandle: record.TaskHandle,
			})
			var failure *domain.Failure
			if !errors.As(cleanupErr, &failure) || failure.Code != domain.ErrorPrecondition || !failure.Retryable ||
				failure.Message != test.wantMessage || failure.Hint != test.wantHint {
				t.Fatalf("CleanupTask(%s) error = %v, want reason-coded precondition failure", test.name, cleanupErr)
			}
			if releaser.calls != 0 || remover.calls != 0 || store.releaseCalls != 0 {
				t.Fatalf("unsafe side effects: release=%d remove=%d recorded=%d", releaser.calls, remover.calls, store.releaseCalls)
			}
		})
	}
}

func TestCleanupCoordinator_ReportsStaleForgeTruthBeforeHostRelease(t *testing.T) {
	record := cleanupFixtureRecord(strings.Repeat("b", 40))
	forge := &cleanupForgeFixture{truth: PullRequestDeliveryTruth{
		RepositoryID: record.RepositoryID, PullRequestID: record.PullRequestID,
		HeadRevision: strings.Repeat("d", 40),
		Checks:       []ForgeCheckTruth{{Name: "ci/unit", Conclusion: domain.CheckPassed}},
	}}
	releaser := &cleanupReleaseFixture{}
	remover := &cleanupRemovalFixture{}
	coordinator, err := NewCleanupCoordinator(CleanupCoordinatorConfig{
		Store:      &cleanupStoreFixture{record: record},
		Workspaces: &cleanupWorkspaceFixture{snapshot: cleanupFixtureSnapshot(record, record.HeadRevision)},
		Forge:      forge, Releaser: releaser, Remover: remover, Clock: func() time.Time { return record.ReleasedAt },
	})
	if err != nil {
		t.Fatal(err)
	}
	_, cleanupErr := coordinator.CleanupTask(context.Background(), CleanupTaskCommand{
		OperationID: record.OperationID, TaskHandle: record.TaskHandle,
	})
	var failure *domain.Failure
	if !errors.As(cleanupErr, &failure) || failure.Code != domain.ErrorPrecondition || !failure.Retryable ||
		failure.Message != CleanupStaleForgeTruthMessage ||
		failure.Hint != "refresh the exact pull request head and required checks, then retry cleanup" {
		t.Fatalf("CleanupTask(stale forge truth) error = %v, want reason-coded precondition failure", cleanupErr)
	}
	if releaser.calls != 0 || remover.calls != 0 {
		t.Fatalf("unsafe side effects: release=%d remove=%d", releaser.calls, remover.calls)
	}
}

func TestCleanupCoordinator_ClassifiesForgeIdentityDriftBeforeHostRelease(t *testing.T) {
	record := cleanupFixtureRecord(strings.Repeat("b", 40))
	releaser := &cleanupReleaseFixture{}
	remover := &cleanupRemovalFixture{}
	coordinator, err := NewCleanupCoordinator(CleanupCoordinatorConfig{
		Store:      &cleanupStoreFixture{record: record},
		Workspaces: &cleanupWorkspaceFixture{snapshot: cleanupFixtureSnapshot(record, record.HeadRevision)},
		Forge:      &cleanupForgeFixture{err: ErrCleanupStaleForgeTruth},
		Releaser:   releaser,
		Remover:    remover,
		Clock:      func() time.Time { return record.ReleasedAt },
	})
	if err != nil {
		t.Fatal(err)
	}
	_, cleanupErr := coordinator.CleanupTask(context.Background(), CleanupTaskCommand{
		OperationID: record.OperationID, TaskHandle: record.TaskHandle,
	})
	var failure *domain.Failure
	if !errors.As(cleanupErr, &failure) || failure.Code != domain.ErrorPrecondition || !failure.Retryable ||
		failure.Message != CleanupStaleForgeTruthMessage ||
		failure.Hint != "refresh the exact pull request head and required checks, then retry cleanup" {
		t.Fatalf("CleanupTask(forge identity drift) error = %v, want stale-forge precondition", cleanupErr)
	}
	if releaser.calls != 0 || remover.calls != 0 {
		t.Fatalf("unsafe side effects: release=%d remove=%d", releaser.calls, remover.calls)
	}
}

func TestCleanupCoordinator_ResumesHeldCleanupWithFreshCallerOperation(t *testing.T) {
	now := time.Date(2026, time.August, 11, 20, 0, 0, 0, time.UTC)
	head := strings.Repeat("b", 40)
	record := cleanupFixtureRecord(head)
	store := &cleanupStoreFixture{record: record}
	workspace := &cleanupWorkspaceFixture{snapshot: cleanupFixtureSnapshot(record, head)}
	forge := &cleanupForgeFixture{truth: PullRequestDeliveryTruth{
		RepositoryID: record.RepositoryID, PullRequestID: record.PullRequestID,
		HeadRevision: head, Checks: []ForgeCheckTruth{{Name: "ci/unit", Conclusion: domain.CheckPassed}},
	}}
	coordinator, err := NewCleanupCoordinator(CleanupCoordinatorConfig{
		Store: store, Workspaces: workspace, Forge: forge, Releaser: &cleanupReleaseFixture{},
		Remover: &cleanupRemovalFixture{}, Clock: func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := coordinator.CleanupTask(context.Background(), CleanupTaskCommand{
		OperationID: "cleanup-task-retry", TaskHandle: record.TaskHandle,
	})
	if err != nil {
		t.Fatalf("CleanupTask(fresh retry operation) error = %v", err)
	}
	if result.Task.State != domain.TaskCleaned || store.completeCalls != 1 {
		t.Fatalf("CleanupTask(fresh retry operation) = %#v, store = %#v", result, store)
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

func TestCleanupFailureClassifierFailsClosedForInvalidOperatorFields(t *testing.T) {
	dependencyFailure := cleanupDependencyFailure("", "", errors.New("private dependency detail"))
	if dependencyFailure == nil || dependencyFailure.Error() != "cleanup dependency failure classification failed" {
		t.Fatalf("cleanupDependencyFailure(invalid fields) = %v", dependencyFailure)
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
	t.Run("durable task identity mismatch", func(t *testing.T) {
		record := cleanupFixtureRecord(head)
		record.TaskHandle = "task-cleanup-other"
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

type cleanupAttachmentReleaseFixture struct {
	taskHandle string
	calls      int
	err        error
}

func (release *cleanupAttachmentReleaseFixture) ReleaseRuntimeAttachment(_ context.Context, taskHandle string) error {
	release.calls++
	release.taskHandle = taskHandle
	return release.err
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
