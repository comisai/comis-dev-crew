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

func TestTaskPreparationIntentSurvivesRestartAndCompletesAtomically(t *testing.T) {
	databasePath := filepath.Join(canonicalTempDir(t), "devcrew.db")
	now := time.Date(2026, time.August, 16, 15, 0, 0, 0, time.UTC)
	intent := application.TaskPreparationIntent{
		OperationID: "operation-preparation-intent-0001", TaskHandle: "task-preparation-intent-0001",
		SubjectDigest: strings.Repeat("a", 64), CreatedAt: now,
	}
	store, err := Open(context.Background(), databasePath)
	if err != nil {
		t.Fatal(err)
	}
	recorded, err := store.RecordTaskPreparationIntent(context.Background(), intent)
	if err != nil || recorded != intent {
		t.Fatalf("RecordTaskPreparationIntent() = %#v, %v", recorded, err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(context.Background(), databasePath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	intents, err := reopened.ListTaskPreparationIntents(context.Background())
	if err != nil || !reflect.DeepEqual(intents, []application.TaskPreparationIntent{intent}) {
		t.Fatalf("ListTaskPreparationIntents() = %#v, %v", intents, err)
	}
	mutation := application.PreparedTaskMutation{
		Task: storeTask(intent.TaskHandle, 1), OperationID: intent.OperationID,
		SubjectDigest: intent.SubjectDigest, At: now,
		Preparation: application.ManagedRunPreparation{
			ExternalRunRef: intent.TaskHandle, RegistrationNonce: "registration-nonce_preparation_intent",
			RequestedWorkspaceRoot: "/approved/workspaces/task-preparation-intent-0001",
			RequestedAttachment: application.PreparedRuntimeAttachment{
				Kind:       application.RuntimeAttachmentUnixSocket,
				SourcePath: "/approved/runtime/task-preparation-intent-0001/attachment.sock",
			},
			ExpiresAt: now.Add(time.Hour), State: application.PreparationOpen,
		},
	}
	mutation.Task.CreatedAt = now
	mutation.Task.UpdatedAt = now
	if _, err := reopened.CommitPreparedTask(context.Background(), mutation); err != nil {
		t.Fatal(err)
	}
	intents, err = reopened.ListTaskPreparationIntents(context.Background())
	if err != nil || len(intents) != 0 {
		t.Fatalf("ListTaskPreparationIntents(completed) = %#v, %v", intents, err)
	}
}
