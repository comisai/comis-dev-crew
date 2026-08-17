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

func TestRuntimePreparationRecoveryRejectsConflictingDurableEvidence(t *testing.T) {
	now := time.Date(2026, time.August, 17, 9, 0, 0, 0, time.UTC)
	intent := application.TaskPreparationIntent{
		OperationID:   "operation-preparation-recovery-boundary",
		TaskHandle:    "task-preparation-recovery-boundary",
		SubjectDigest: strings.Repeat("a", 64),
		CreatedAt:     now,
	}
	refusal := application.RuntimeAttachmentRecoveryRefusal{
		OperationID: intent.OperationID, TaskHandle: intent.TaskHandle,
		SubjectDigest: intent.SubjectDigest, Reason: application.RuntimeAttachmentPreparationUnproven,
		RefusedAt: now,
	}
	tests := []struct {
		name      string
		store     *preparationRecoveryBoundaryStore
		clock     func() time.Time
		unproven  bool
		wantError bool
	}{
		{name: "refusal read failure", store: &preparationRecoveryBoundaryStore{refusalErr: errors.New("unavailable")}, wantError: true},
		{name: "invalid refusal", store: &preparationRecoveryBoundaryStore{refusals: []application.RuntimeAttachmentRecoveryRefusal{{}}}, wantError: true},
		{name: "duplicate refusal", store: &preparationRecoveryBoundaryStore{refusals: []application.RuntimeAttachmentRecoveryRefusal{refusal, refusal}}, wantError: true},
		{name: "intent read failure", store: &preparationRecoveryBoundaryStore{intentErr: errors.New("unavailable")}, wantError: true},
		{name: "invalid intent", store: &preparationRecoveryBoundaryStore{intents: []application.TaskPreparationIntent{{}}}, wantError: true},
		{name: "refusal mismatch", store: &preparationRecoveryBoundaryStore{
			refusals: []application.RuntimeAttachmentRecoveryRefusal{refusal},
			intents: []application.TaskPreparationIntent{{
				OperationID: "operation-preparation-recovery-other", TaskHandle: intent.TaskHandle,
				SubjectDigest: intent.SubjectDigest, CreatedAt: now,
			}},
		}, wantError: true},
		{name: "invalid refusal time", store: &preparationRecoveryBoundaryStore{intents: []application.TaskPreparationIntent{intent}},
			clock: func() time.Time { return time.Time{} }, unproven: true, wantError: true},
		{name: "refusal write failure", store: &preparationRecoveryBoundaryStore{
			intents: []application.TaskPreparationIntent{intent}, refuseErr: errors.New("unavailable"),
		}, unproven: true, wantError: true},
		{name: "matching refusal", store: &preparationRecoveryBoundaryStore{
			refusals: []application.RuntimeAttachmentRecoveryRefusal{refusal},
			intents:  []application.TaskPreparationIntent{intent},
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := filepath.Join(shortTempDir(t), "runtime")
			coordinator := runtimeTransitionCoordinator(t, root, test.store, now)
			if test.clock != nil {
				coordinator.clock = test.clock
			}
			if test.unproven {
				if err := os.Mkdir(filepath.Join(root, intent.TaskHandle), 0o700); err != nil {
					t.Fatal(err)
				}
			}
			err := coordinator.recoverTaskPreparationIntents(context.Background())
			if test.wantError && err == nil {
				t.Fatal("recoverTaskPreparationIntents() error = nil")
			}
			if !test.wantError && err != nil {
				t.Fatalf("recoverTaskPreparationIntents() error = %v", err)
			}
		})
	}
}

func TestRuntimeAttachmentRecoveryRejectsIncompleteTaskPosture(t *testing.T) {
	now := time.Date(2026, time.August, 17, 10, 0, 0, 0, time.UTC)
	prepared := runtimeAttachmentRecoverableTask(t, now, "task-recovery-posture-boundary")
	cleaned := prepared
	cleaned.Handle = "task-recovery-cleaned-boundary"
	cleaned.State = domain.TaskCleaned
	released := prepared
	released.Handle = "task-recovery-released-boundary"
	held := prepared
	held.Handle = "task-recovery-held-boundary"
	held.State = domain.TaskCleanupHeld
	tests := []struct {
		name      string
		store     *runtimeRecoveryBoundaryStore
		ctx       func() context.Context
		preloaded bool
		wantOK    bool
	}{
		{name: "preloaded coordinator", store: &runtimeRecoveryBoundaryStore{}, preloaded: true},
		{name: "task read failure", store: &runtimeRecoveryBoundaryStore{listTasksErr: errors.New("unavailable")}},
		{name: "canceled recovery", store: &runtimeRecoveryBoundaryStore{
			runtimeAttachmentRecoveryStore: runtimeAttachmentRecoveryStore{tasks: []domain.Task{prepared}},
		}, ctx: canceledRecoveryContext},
		{name: "cleanup read failure", store: &runtimeRecoveryBoundaryStore{
			runtimeAttachmentRecoveryStore: runtimeAttachmentRecoveryStore{
				tasks: []domain.Task{prepared}, cleanupErr: errors.New("unavailable"),
			},
		}},
		{name: "cleanup target mismatch", store: &runtimeRecoveryBoundaryStore{
			runtimeAttachmentRecoveryStore: runtimeAttachmentRecoveryStore{
				tasks: []domain.Task{prepared}, cleanupFound: true,
				cleanupRecord: application.TaskCleanupRecord{TaskHandle: "task-recovery-other", Stage: application.CleanupPrepared},
			},
		}},
		{name: "cleanup stage invalid", store: &runtimeRecoveryBoundaryStore{
			runtimeAttachmentRecoveryStore: runtimeAttachmentRecoveryStore{
				tasks: []domain.Task{prepared}, cleanupFound: true,
				cleanupRecord: application.TaskCleanupRecord{TaskHandle: prepared.Handle, Stage: application.TaskCleanupStage("invalid")},
			},
		}},
		{name: "held cleanup missing", store: &runtimeRecoveryBoundaryStore{
			runtimeAttachmentRecoveryStore: runtimeAttachmentRecoveryStore{tasks: []domain.Task{held}},
		}},
		{name: "preparation read failure", store: &runtimeRecoveryBoundaryStore{
			runtimeAttachmentRecoveryStore: runtimeAttachmentRecoveryStore{tasks: []domain.Task{prepared}},
			preparationErr:                 errors.New("unavailable"),
		}},
		{name: "cleaned task absent", store: &runtimeRecoveryBoundaryStore{
			runtimeAttachmentRecoveryStore: runtimeAttachmentRecoveryStore{tasks: []domain.Task{cleaned}},
		}, wantOK: true},
		{name: "released task absent", store: &runtimeRecoveryBoundaryStore{
			runtimeAttachmentRecoveryStore: runtimeAttachmentRecoveryStore{
				tasks: []domain.Task{released}, cleanupFound: true,
				cleanupRecord: application.TaskCleanupRecord{TaskHandle: released.Handle, Stage: application.CleanupHostReleased},
			},
		}, wantOK: true},
		{name: "preparation source mismatch", store: &runtimeRecoveryBoundaryStore{
			runtimeAttachmentRecoveryStore: runtimeAttachmentRecoveryStore{tasks: []domain.Task{prepared}},
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			coordinator := runtimeTransitionCoordinator(t, filepath.Join(shortTempDir(t), "runtime"), test.store, now)
			if test.preloaded {
				coordinator.entries[prepared.Handle] = &runtimeAttachmentEntry{}
			}
			ctx := context.Background()
			if test.ctx != nil {
				ctx = test.ctx()
			}
			servers, err := coordinator.recoverRuntimeAttachments(ctx)
			if test.wantOK && (err != nil || len(servers) != 0) {
				t.Fatalf("recoverRuntimeAttachments() = %d, %v", len(servers), err)
			}
			if !test.wantOK && (err == nil || len(servers) != 0) {
				t.Fatalf("recoverRuntimeAttachments() = %d, %v", len(servers), err)
			}
		})
	}
	zeroClock := runtimeTransitionCoordinator(
		t, filepath.Join(shortTempDir(t), "runtime"), &runtimeAttachmentRecoveryStore{}, now,
	)
	zeroClock.clock = func() time.Time { return time.Time{} }
	if err := zeroClock.persistRuntimeAttachmentTaskRefusal(context.Background(), prepared.Handle); err == nil {
		t.Fatal("persistRuntimeAttachmentTaskRefusal accepted zero service time")
	}
	for _, test := range []struct {
		name  string
		store runtimeAttachmentStore
	}{
		{name: "relay recovery", store: &runtimeRelayBoundaryStore{upgradeErr: errors.New("unavailable")}},
		{name: "preparation recovery", store: &preparationRecoveryBoundaryStore{refusalErr: errors.New("unavailable")}},
	} {
		t.Run(test.name, func(t *testing.T) {
			coordinator := runtimeTransitionCoordinator(t, filepath.Join(shortTempDir(t), "runtime"), test.store, now)
			if servers, err := coordinator.recoverRuntimeAttachments(context.Background()); err == nil || len(servers) != 0 {
				t.Fatalf("recoverRuntimeAttachments() = %d, %v", len(servers), err)
			}
		})
	}
}

func canceledRecoveryContext() context.Context {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	return ctx
}

type preparationRecoveryBoundaryStore struct {
	runtimeAttachmentRecoveryStore
	refusals   []application.RuntimeAttachmentRecoveryRefusal
	intents    []application.TaskPreparationIntent
	refusalErr error
	intentErr  error
	refuseErr  error
}

func (store *preparationRecoveryBoundaryStore) ListRuntimeAttachmentRecoveryRefusals(
	context.Context,
) ([]application.RuntimeAttachmentRecoveryRefusal, error) {
	return append([]application.RuntimeAttachmentRecoveryRefusal(nil), store.refusals...), store.refusalErr
}

func (store *preparationRecoveryBoundaryStore) ListTaskPreparationIntents(
	context.Context,
) ([]application.TaskPreparationIntent, error) {
	return append([]application.TaskPreparationIntent(nil), store.intents...), store.intentErr
}

func (store *preparationRecoveryBoundaryStore) RefuseRuntimeAttachmentRecovery(
	context.Context,
	application.TaskPreparationIntent,
	time.Time,
) error {
	return store.refuseErr
}

type runtimeRecoveryBoundaryStore struct {
	runtimeAttachmentRecoveryStore
	listTasksErr   error
	preparationErr error
}

func (store *runtimeRecoveryBoundaryStore) ListTasks(context.Context) ([]domain.Task, error) {
	return append([]domain.Task(nil), store.tasks...), store.listTasksErr
}

func (store *runtimeRecoveryBoundaryStore) GetManagedRunPreparation(
	context.Context,
	string,
) (application.ManagedRunPreparation, error) {
	return application.ManagedRunPreparation{}, store.preparationErr
}
