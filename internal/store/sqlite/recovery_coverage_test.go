package sqlite

import (
	"context"
	"crypto/ed25519"
	"encoding/hex"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/comisai/comis-dev-crew/internal/application"
	"github.com/comisai/comis-dev-crew/internal/domain"
)

func TestRecoveryStoresRejectCanceledAndClosedAuthority(t *testing.T) {
	store, err := Open(context.Background(), filepath.Join(canonicalTempDir(t), "devcrew.db"))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, time.August, 17, 1, 0, 0, 0, time.UTC)
	intent := application.TaskPreparationIntent{
		OperationID: "operation-recovery-coverage", TaskHandle: "task-recovery-coverage",
		SubjectDigest: strings.Repeat("a", 64), CreatedAt: now,
	}
	seed := [32]byte{1}
	privateKey := ed25519.NewKeyFromSeed(seed[:])
	upgrade := application.RuntimeRelayIdentityUpgrade{
		TaskHandle:    intent.TaskHandle,
		RelayIdentity: hex.EncodeToString(privateKey.Public().(ed25519.PublicKey)),
		RelaySeed:     seed,
	}

	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := store.RecordTaskPreparationIntent(canceled, intent); !errors.Is(err, context.Canceled) {
		t.Fatalf("RecordTaskPreparationIntent(canceled) error = %v", err)
	}
	if _, err := store.ListTaskPreparationIntents(canceled); !errors.Is(err, context.Canceled) {
		t.Fatalf("ListTaskPreparationIntents(canceled) error = %v", err)
	}
	if _, err := store.ListRuntimeRelayIdentityUpgrades(canceled); !errors.Is(err, context.Canceled) {
		t.Fatalf("ListRuntimeRelayIdentityUpgrades(canceled) error = %v", err)
	}
	if _, err := store.ListRuntimeRelayIdentityRefusals(canceled); !errors.Is(err, context.Canceled) {
		t.Fatalf("ListRuntimeRelayIdentityRefusals(canceled) error = %v", err)
	}
	if err := store.CompleteRuntimeRelayIdentityUpgrade(canceled, upgrade); !errors.Is(err, context.Canceled) {
		t.Fatalf("CompleteRuntimeRelayIdentityUpgrade(canceled) error = %v", err)
	}
	if err := store.RefuseRuntimeRelayIdentityUpgrade(canceled, upgrade, now); !errors.Is(err, context.Canceled) {
		t.Fatalf("RefuseRuntimeRelayIdentityUpgrade(canceled) error = %v", err)
	}
	if err := store.RefuseRuntimeAttachmentTaskRecovery(canceled, intent.TaskHandle, now); !errors.Is(err, context.Canceled) {
		t.Fatalf("RefuseRuntimeAttachmentTaskRecovery(canceled) error = %v", err)
	}

	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ListTaskPreparationIntents(context.Background()); err == nil {
		t.Fatal("closed store listed preparation intents")
	}
	if _, err := store.ListRuntimeRelayIdentityUpgrades(context.Background()); err == nil {
		t.Fatal("closed store listed relay upgrades")
	}
	if _, err := store.ListRuntimeRelayIdentityRefusals(context.Background()); err == nil {
		t.Fatal("closed store listed relay refusals")
	}
}

func TestPreparationIntentReplaysAndConflictsConservatively(t *testing.T) {
	store, err := Open(context.Background(), filepath.Join(canonicalTempDir(t), "devcrew.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	now := time.Date(2026, time.August, 17, 1, 30, 0, 0, time.UTC)
	intent := application.TaskPreparationIntent{
		OperationID: "operation-intent-coverage", TaskHandle: "task-intent-coverage",
		SubjectDigest: strings.Repeat("b", 64), CreatedAt: now,
	}
	if recorded, err := store.RecordTaskPreparationIntent(context.Background(), intent); err != nil || recorded != intent {
		t.Fatalf("RecordTaskPreparationIntent(first) = %#v, %v", recorded, err)
	}
	if replayed, err := store.RecordTaskPreparationIntent(context.Background(), intent); err != nil || replayed != intent {
		t.Fatalf("RecordTaskPreparationIntent(replay) = %#v, %v", replayed, err)
	}
	altered := intent
	altered.SubjectDigest = strings.Repeat("c", 64)
	if _, err := store.RecordTaskPreparationIntent(context.Background(), altered); !errors.Is(err, application.ErrConflict) {
		t.Fatalf("RecordTaskPreparationIntent(altered replay) error = %v", err)
	}
	collision := intent
	collision.OperationID = "operation-intent-collision"
	if _, err := store.RecordTaskPreparationIntent(context.Background(), collision); !errors.Is(err, application.ErrConflict) {
		t.Fatalf("RecordTaskPreparationIntent(task collision) error = %v", err)
	}
	if intents, err := store.ListTaskPreparationIntents(context.Background()); err != nil || len(intents) != 1 || intents[0] != intent {
		t.Fatalf("ListTaskPreparationIntents() = %#v, %v", intents, err)
	}
	if _, err := store.db.Exec(`UPDATE task_preparation_intents SET created_at = 'invalid' WHERE operation_id = ?`, intent.OperationID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ListTaskPreparationIntents(context.Background()); err == nil {
		t.Fatal("malformed preparation intent was listed")
	}
}

func TestRuntimeRecoveryRefusalIsIdempotentAndTaskScoped(t *testing.T) {
	store, err := Open(context.Background(), filepath.Join(canonicalTempDir(t), "devcrew.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	now := time.Date(2026, time.August, 17, 2, 0, 0, 0, time.UTC)
	if err := store.RefuseRuntimeAttachmentTaskRecovery(context.Background(), "task-missing-coverage", now); !errors.Is(err, application.ErrNotFound) {
		t.Fatalf("RefuseRuntimeAttachmentTaskRecovery(missing) error = %v", err)
	}
	task := storeTask("task-refusal-coverage", 1)
	task.CreatedAt, task.UpdatedAt = now, now
	if err := store.CreateTask(context.Background(), task); err != nil {
		t.Fatal(err)
	}
	if err := store.RefuseRuntimeAttachmentTaskRecovery(context.Background(), task.Handle, now.Add(time.Minute)); err != nil {
		t.Fatalf("RefuseRuntimeAttachmentTaskRecovery(first) error = %v", err)
	}
	if err := store.RefuseRuntimeAttachmentTaskRecovery(context.Background(), task.Handle, now.Add(2*time.Minute)); err != nil {
		t.Fatalf("RefuseRuntimeAttachmentTaskRecovery(replay) error = %v", err)
	}
	refusals, err := store.ListRuntimeRelayIdentityRefusals(context.Background())
	if err != nil || len(refusals) != 1 || refusals[0].TaskHandle != task.Handle {
		t.Fatalf("ListRuntimeRelayIdentityRefusals() = %#v, %v", refusals, err)
	}
	stored, err := store.GetTask(context.Background(), task.Handle)
	if err != nil || stored.State != domain.TaskUnknown {
		t.Fatalf("GetTask(refused) = %#v, %v", stored, err)
	}

	seed := [32]byte{1}
	privateKey := ed25519.NewKeyFromSeed(seed[:])
	missingUpgrade := application.RuntimeRelayIdentityUpgrade{
		TaskHandle:    task.Handle,
		RelayIdentity: hex.EncodeToString(privateKey.Public().(ed25519.PublicKey)),
		RelaySeed:     seed,
	}
	if err := store.CompleteRuntimeRelayIdentityUpgrade(context.Background(), missingUpgrade); err == nil {
		t.Fatal("missing relay upgrade completed")
	}
	if err := store.CompleteRuntimeRelayIdentityUpgrade(context.Background(), application.RuntimeRelayIdentityUpgrade{}); err == nil {
		t.Fatal("invalid relay upgrade completed")
	}
	if err := store.RefuseRuntimeRelayIdentityUpgrade(context.Background(), application.RuntimeRelayIdentityUpgrade{}, now); err == nil {
		t.Fatal("invalid relay upgrade was refused")
	}
	if err := store.RefuseRuntimeAttachmentTaskRecovery(context.Background(), "", now); err == nil {
		t.Fatal("invalid task recovery was refused")
	}
}
