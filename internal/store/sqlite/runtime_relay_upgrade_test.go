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

func TestRuntimeRelayIdentityMigrationBackfillsThroughStrictUpgrade(t *testing.T) {
	databasePath := filepath.Join(canonicalTempDir(t), "devcrew.db")
	now := time.Date(2026, time.August, 16, 17, 0, 0, 0, time.UTC)
	store, err := Open(context.Background(), databasePath)
	if err != nil {
		t.Fatal(err)
	}
	task := storeTask("task-runtime-relay-upgrade", 1)
	task.CreatedAt, task.UpdatedAt = now, now
	mutation := application.PreparedTaskMutation{
		Task: task, OperationID: "operation-runtime-relay-upgrade",
		SubjectDigest: strings.Repeat("a", 64), At: now,
		Preparation: application.ManagedRunPreparation{
			ExternalRunRef: task.Handle, RegistrationNonce: "registration-nonce_relay_upgrade",
			RequestedWorkspaceRoot: "/approved/workspaces/runtime-relay-upgrade",
			RequestedAttachment: application.PreparedRuntimeAttachment{
				Kind: application.RuntimeAttachmentUnixSocket, SourcePath: "/approved/runtime/relay-upgrade/attachment.sock",
				RelayIdentity: strings.Repeat("ab", 32),
			},
			ExpiresAt: now.Add(time.Hour), State: application.PreparationOpen,
		},
	}
	if _, err := store.RecordTaskPreparationIntent(context.Background(), application.TaskPreparationIntent{
		OperationID: mutation.OperationID, TaskHandle: task.Handle,
		SubjectDigest: mutation.SubjectDigest, CreatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CommitPreparedTask(context.Background(), mutation); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec(`UPDATE task_preparations
		SET requested_attachment_relay_identity = '' WHERE task_handle = ?`, task.Handle); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec(`DELETE FROM schema_migrations WHERE version = 20`); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	reopened, err := Open(context.Background(), databasePath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	upgrades, err := reopened.ListRuntimeRelayIdentityUpgrades(context.Background())
	if err != nil || len(upgrades) != 1 || upgrades[0].TaskHandle != task.Handle || upgrades[0].Validate() != nil {
		t.Fatalf("ListRuntimeRelayIdentityUpgrades() count = %d, error = %v", len(upgrades), err)
	}
	if _, err := reopened.GetManagedRunPreparation(context.Background(), task.Handle); err == nil {
		t.Fatal("GetManagedRunPreparation(pending relay upgrade) error = nil")
	}
	if err := reopened.CompleteRuntimeRelayIdentityUpgrade(context.Background(), upgrades[0]); err != nil {
		t.Fatal(err)
	}
	preparation, err := reopened.GetManagedRunPreparation(context.Background(), task.Handle)
	if err != nil || preparation.RequestedAttachment.RelayIdentity != upgrades[0].RelayIdentity {
		t.Fatalf("GetManagedRunPreparation(upgraded) = %#v, %v", preparation, err)
	}
}

func TestRuntimeRelayIdentityRefusalClosesOnlyAffectedTaskRecovery(t *testing.T) {
	databasePath := filepath.Join(canonicalTempDir(t), "devcrew.db")
	now := time.Date(2026, time.August, 16, 18, 0, 0, 0, time.UTC)
	store, err := Open(context.Background(), databasePath)
	if err != nil {
		t.Fatal(err)
	}
	task := storeTask("task-runtime-relay-refusal", 1)
	task.CreatedAt, task.UpdatedAt = now, now
	mutation := application.PreparedTaskMutation{
		Task: task, OperationID: "operation-runtime-relay-refusal",
		SubjectDigest: strings.Repeat("b", 64), At: now,
		Preparation: application.ManagedRunPreparation{
			ExternalRunRef: task.Handle, RegistrationNonce: "registration-nonce_relay_refusal",
			RequestedWorkspaceRoot: "/approved/workspaces/runtime-relay-refusal",
			RequestedAttachment: application.PreparedRuntimeAttachment{
				Kind: application.RuntimeAttachmentUnixSocket, SourcePath: "/approved/runtime/relay-refusal/attachment.sock",
				RelayIdentity: strings.Repeat("cd", 32),
			},
			ExpiresAt: now.Add(time.Hour), State: application.PreparationOpen,
		},
	}
	if _, err := store.RecordTaskPreparationIntent(context.Background(), application.TaskPreparationIntent{
		OperationID: mutation.OperationID, TaskHandle: task.Handle,
		SubjectDigest: mutation.SubjectDigest, CreatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CommitPreparedTask(context.Background(), mutation); err != nil {
		t.Fatal(err)
	}
	unaffected := storeTask("task-runtime-relay-unaffected", 2)
	unaffected.CreatedAt, unaffected.UpdatedAt = now, now
	if err := store.CreateTask(context.Background(), unaffected); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec(`UPDATE task_preparations
		SET requested_attachment_relay_identity = '' WHERE task_handle = ?`, task.Handle); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec(`DELETE FROM schema_migrations WHERE version = 20`); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	reopened, err := Open(context.Background(), databasePath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	upgrades, err := reopened.ListRuntimeRelayIdentityUpgrades(context.Background())
	if err != nil || len(upgrades) != 1 {
		t.Fatalf("ListRuntimeRelayIdentityUpgrades() = %#v, %v", upgrades, err)
	}
	refusedAt := now.Add(time.Minute)
	if err := reopened.RefuseRuntimeRelayIdentityUpgrade(context.Background(), upgrades[0], refusedAt); err != nil {
		t.Fatal(err)
	}
	storedTask, err := reopened.GetTask(context.Background(), task.Handle)
	if err != nil || storedTask.State != domain.TaskUnknown || !storedTask.UpdatedAt.Equal(refusedAt) {
		t.Fatalf("GetTask(refused relay) = %#v, %v", storedTask, err)
	}
	storedUnaffected, err := reopened.GetTask(context.Background(), unaffected.Handle)
	if err != nil || storedUnaffected.State != unaffected.State || storedUnaffected.StateVersion != unaffected.StateVersion {
		t.Fatalf("GetTask(unaffected) = %#v, %v", storedUnaffected, err)
	}
	refusals, err := reopened.ListRuntimeRelayIdentityRefusals(context.Background())
	if err != nil || len(refusals) != 1 || refusals[0].TaskHandle != task.Handle ||
		refusals[0].Reason != application.RuntimeRelayIdentityUnproven {
		t.Fatalf("ListRuntimeRelayIdentityRefusals() = %#v, %v", refusals, err)
	}
	evidence, err := reopened.ReadTaskRecoveryEvidence(context.Background(), task.Handle)
	if err != nil || evidence.Kind != application.RecoveryRuntimeRelayIdentityUnproven {
		t.Fatalf("ReadTaskRecoveryEvidence(refused relay) = %#v, %v", evidence, err)
	}
	if _, err := reopened.ReadTaskReconciliationAuthority(context.Background(), task.Handle); !errors.Is(err, application.ErrPrecondition) {
		t.Fatalf("ReadTaskReconciliationAuthority(refused relay) error = %v", err)
	}
}

func TestRuntimeRelayIdentityRefusalPreservesCleanedTaskState(t *testing.T) {
	databasePath := filepath.Join(canonicalTempDir(t), "devcrew.db")
	now := time.Date(2026, time.August, 16, 18, 30, 0, 0, time.UTC)
	store, err := Open(context.Background(), databasePath)
	if err != nil {
		t.Fatal(err)
	}
	task := storeTask("task-runtime-relay-cleaned-refusal", 1)
	task.CreatedAt, task.UpdatedAt = now, now
	mutation := application.PreparedTaskMutation{
		Task: task, OperationID: "operation-runtime-relay-cleaned-refusal",
		SubjectDigest: strings.Repeat("c", 64), At: now,
		Preparation: application.ManagedRunPreparation{
			ExternalRunRef: task.Handle, RegistrationNonce: "registration-nonce_relay_cleaned_refusal",
			RequestedWorkspaceRoot: "/approved/workspaces/runtime-relay-cleaned-refusal",
			RequestedAttachment: application.PreparedRuntimeAttachment{
				Kind: application.RuntimeAttachmentUnixSocket, SourcePath: "/approved/runtime/relay-cleaned-refusal/attachment.sock",
				RelayIdentity: strings.Repeat("ef", 32),
			},
			ExpiresAt: now.Add(time.Hour), State: application.PreparationOpen,
		},
	}
	if _, err := store.RecordTaskPreparationIntent(context.Background(), application.TaskPreparationIntent{
		OperationID: mutation.OperationID, TaskHandle: task.Handle,
		SubjectDigest: mutation.SubjectDigest, CreatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CommitPreparedTask(context.Background(), mutation); err != nil {
		t.Fatal(err)
	}
	cleanedAt := now.Add(time.Minute)
	if _, err := store.db.Exec(`UPDATE tasks SET state = 'cleaned', state_version = state_version + 1,
		updated_at = ? WHERE handle = ?`, formatTime(cleanedAt), task.Handle); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec(`UPDATE task_preparations
		SET requested_attachment_relay_identity = '' WHERE task_handle = ?`, task.Handle); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec(`DELETE FROM schema_migrations WHERE version = 20`); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	reopened, err := Open(context.Background(), databasePath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	upgrades, err := reopened.ListRuntimeRelayIdentityUpgrades(context.Background())
	if err != nil || len(upgrades) != 1 {
		t.Fatalf("ListRuntimeRelayIdentityUpgrades() = %#v, %v", upgrades, err)
	}
	before, err := reopened.GetTask(context.Background(), task.Handle)
	if err != nil || before.State != domain.TaskCleaned {
		t.Fatalf("GetTask(before refusal) = %#v, %v", before, err)
	}
	if err := reopened.RefuseRuntimeRelayIdentityUpgrade(context.Background(), upgrades[0], now.Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	after, err := reopened.GetTask(context.Background(), task.Handle)
	if err != nil || !reflect.DeepEqual(after, before) {
		t.Fatalf("GetTask(after refusal) = %#v, %v, want %#v", after, err, before)
	}
	refusals, err := reopened.ListRuntimeRelayIdentityRefusals(context.Background())
	if err != nil || len(refusals) != 1 || refusals[0].TaskHandle != task.Handle {
		t.Fatalf("ListRuntimeRelayIdentityRefusals() = %#v, %v", refusals, err)
	}
}

func TestRuntimeAttachmentTaskRefusalSurvivesRestartAndIsolatesTask(t *testing.T) {
	databasePath := filepath.Join(canonicalTempDir(t), "devcrew.db")
	now := time.Date(2026, time.August, 17, 10, 0, 0, 0, time.UTC)
	store, err := Open(context.Background(), databasePath)
	if err != nil {
		t.Fatal(err)
	}
	affected := storeTask("task-runtime-ownership-refusal", 1)
	affected.CreatedAt, affected.UpdatedAt = now, now
	unaffected := storeTask("task-runtime-ownership-unaffected", 2)
	unaffected.CreatedAt, unaffected.UpdatedAt = now, now
	if err := store.CreateTask(context.Background(), affected); err != nil {
		t.Fatal(err)
	}
	if err := store.CreateTask(context.Background(), unaffected); err != nil {
		t.Fatal(err)
	}
	refusedAt := now.Add(time.Minute)
	if err := store.RefuseRuntimeAttachmentTaskRecovery(context.Background(), affected.Handle, refusedAt); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(context.Background(), databasePath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	storedAffected, err := reopened.GetTask(context.Background(), affected.Handle)
	if err != nil || storedAffected.State != domain.TaskUnknown || !storedAffected.UpdatedAt.Equal(refusedAt) {
		t.Fatalf("GetTask(affected) = %#v, %v", storedAffected, err)
	}
	storedUnaffected, err := reopened.GetTask(context.Background(), unaffected.Handle)
	if err != nil || storedUnaffected.State != unaffected.State || storedUnaffected.StateVersion != unaffected.StateVersion {
		t.Fatalf("GetTask(unaffected) = %#v, %v", storedUnaffected, err)
	}
	refusals, err := reopened.ListRuntimeRelayIdentityRefusals(context.Background())
	if err != nil || len(refusals) != 1 || refusals[0].TaskHandle != affected.Handle ||
		refusals[0].Reason != application.RuntimeRelayIdentityUnproven {
		t.Fatalf("ListRuntimeRelayIdentityRefusals() = %#v, %v", refusals, err)
	}
}
