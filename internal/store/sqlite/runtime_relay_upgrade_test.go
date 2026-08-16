package sqlite

import (
	"context"
	"errors"
	"path/filepath"
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
