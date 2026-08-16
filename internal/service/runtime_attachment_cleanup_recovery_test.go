package service

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/comisai/comis-dev-crew/internal/application"
	"github.com/comisai/comis-dev-crew/internal/domain"
)

func TestRuntimeAttachmentCoordinator_DoesNotRestoreDurablyReleasedAttachment(t *testing.T) {
	for _, stage := range []application.TaskCleanupStage{
		application.CleanupHostReleased,
		application.CleanupRemovalAuthorized,
		application.CleanupCompleted,
	} {
		t.Run(string(stage), func(t *testing.T) {
			root := shortTempDir(t)
			runtimeRoot := filepath.Join(root, "runtime")
			now := time.Date(2026, time.August, 16, 12, 0, 0, 0, time.UTC)
			task := runtimeAttachmentCleanupHeldTask(t, now, "task-runtime-cleanup-recovery")
			store := &runtimeAttachmentRecoveryStore{
				tasks: []domain.Task{task}, cleanupFound: true,
				cleanupRecord: application.TaskCleanupRecord{TaskHandle: task.Handle, Stage: stage},
			}
			coordinator, err := newRuntimeAttachmentCoordinator(runtimeAttachmentCoordinatorConfig{
				RuntimeRoot: runtimeRoot, Store: store, Clock: func() time.Time { return now },
				NewCredential:           func() (string, error) { return "unused-cleanup-credential-0123456789", nil },
				NewAttentionOperationID: runtimeAttentionOperationID,
			})
			if err != nil {
				t.Fatal(err)
			}
			taskRoot := filepath.Join(runtimeRoot, task.Handle)
			if err := os.Mkdir(taskRoot, 0o700); err != nil {
				t.Fatal(err)
			}
			servers, err := coordinator.recoverRuntimeAttachments(context.Background())
			if err != nil || len(servers) != 0 {
				t.Fatalf("recoverRuntimeAttachments(%s) = %d servers, %v", stage, len(servers), err)
			}
			if store.cleanupReads != 1 || store.preparationReads != 0 || len(coordinator.entries) != 0 {
				t.Fatalf("released recovery reads: cleanup=%d preparation=%d entries=%d",
					store.cleanupReads, store.preparationReads, len(coordinator.entries))
			}
			if _, err := os.Lstat(taskRoot); !os.IsNotExist(err) {
				t.Fatalf("released task runtime root error = %v, want not exist", err)
			}
		})
	}
}

func TestRuntimeAttachmentCoordinator_RefusesUnknownHeldCleanupPosture(t *testing.T) {
	root := shortTempDir(t)
	now := time.Date(2026, time.August, 16, 12, 30, 0, 0, time.UTC)
	task := runtimeAttachmentCleanupHeldTask(t, now, "task-runtime-cleanup-unknown")
	for _, test := range []struct {
		name  string
		store *runtimeAttachmentRecoveryStore
	}{
		{name: "missing", store: &runtimeAttachmentRecoveryStore{tasks: []domain.Task{task}}},
		{name: "read failure", store: &runtimeAttachmentRecoveryStore{
			tasks: []domain.Task{task}, cleanupErr: errors.New("cleanup posture unavailable"),
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			coordinator, err := newRuntimeAttachmentCoordinator(runtimeAttachmentCoordinatorConfig{
				RuntimeRoot: filepath.Join(root, test.name), Store: test.store, Clock: func() time.Time { return now },
				NewCredential:           func() (string, error) { return "unused-unknown-credential-0123456789", nil },
				NewAttentionOperationID: runtimeAttentionOperationID,
			})
			if err != nil {
				t.Fatal(err)
			}
			if servers, err := coordinator.recoverRuntimeAttachments(context.Background()); err == nil || len(servers) != 0 {
				t.Fatalf("recoverRuntimeAttachments(%s) = %d servers, %v", test.name, len(servers), err)
			}
			if test.store.cleanupReads != 1 || test.store.preparationReads != 0 {
				t.Fatalf("unknown recovery reads: cleanup=%d preparation=%d",
					test.store.cleanupReads, test.store.preparationReads)
			}
		})
	}
}

func runtimeAttachmentCleanupHeldTask(t *testing.T, now time.Time, taskHandle string) domain.Task {
	t.Helper()
	task := domain.Task{
		SchemaVersion: 1, Handle: taskHandle, State: domain.TaskCleanupHeld,
		ServiceInstanceID: "service-instance-runtime-cleanup", ManagedRunID: "managed-run.runtime-cleanup",
		WorkspaceLeaseID: "workspace-lease.runtime-cleanup", Shape: domain.ShapeScout,
		RepositoryID: "product-api", BaseRevision: strings.Repeat("a", 40), BriefRevision: 1,
		AcceptanceCriteria: []string{"Do not restore a released reporter."}, ValidationProfile: "go-default",
		DeliveryMode: domain.DeliveryReport, WorkerProfileID: "codex-reviewed",
		StateVersion: 3, CreatedAt: now.Add(-time.Hour), UpdatedAt: now,
	}
	pinned, err := task.PinBriefRevision()
	if err != nil || pinned.Validate() != nil {
		t.Fatalf("cleanup-held task = %#v, %v", pinned, err)
	}
	return pinned
}
