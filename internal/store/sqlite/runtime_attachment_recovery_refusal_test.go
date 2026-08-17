package sqlite

import (
	"context"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/comisai/comis-dev-crew/internal/application"
)

func TestRuntimeAttachmentRecoveryRefusalSurvivesRestart(t *testing.T) {
	databasePath := filepath.Join(canonicalTempDir(t), "devcrew.db")
	now := time.Date(2026, time.August, 16, 16, 10, 0, 0, time.UTC)
	intent := application.TaskPreparationIntent{
		OperationID: "operation-runtime-recovery-refusal", TaskHandle: "task-runtime-recovery-refusal",
		SubjectDigest: strings.Repeat("b", 64), CreatedAt: now,
	}
	store, err := Open(context.Background(), databasePath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.RecordTaskPreparationIntent(context.Background(), intent); err != nil {
		t.Fatal(err)
	}
	if err := store.RefuseRuntimeAttachmentRecovery(context.Background(), intent, now.Add(time.Minute)); err != nil {
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
	want := []application.RuntimeAttachmentRecoveryRefusal{{
		OperationID: intent.OperationID, TaskHandle: intent.TaskHandle, SubjectDigest: intent.SubjectDigest,
		Reason: application.RuntimeAttachmentPreparationUnproven, RefusedAt: now.Add(time.Minute),
	}}
	refusals, err := reopened.ListRuntimeAttachmentRecoveryRefusals(context.Background())
	if err != nil || !reflect.DeepEqual(refusals, want) {
		t.Fatalf("ListRuntimeAttachmentRecoveryRefusals() = %#v, %v", refusals, err)
	}
	intents, err := reopened.ListTaskPreparationIntents(context.Background())
	if err != nil || !reflect.DeepEqual(intents, []application.TaskPreparationIntent{intent}) {
		t.Fatalf("ListTaskPreparationIntents(refused) = %#v, %v", intents, err)
	}
}
