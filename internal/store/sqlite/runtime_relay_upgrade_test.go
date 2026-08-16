package sqlite

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/comisai/comis-dev-crew/internal/application"
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
