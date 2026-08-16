package service

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/comisai/comis-dev-crew/internal/application"
	"golang.org/x/sys/unix"
)

func TestRuntimeAttachmentRecoveryIsolatesUnprovenPreparation(t *testing.T) {
	root := shortTempDir(t)
	runtimeRoot := filepath.Join(root, "runtime")
	now := time.Date(2026, time.August, 16, 16, 0, 0, 0, time.UTC)
	intent := application.TaskPreparationIntent{
		OperationID: "operation-runtime-unproven-recovery", TaskHandle: "task-runtime-unproven-recovery",
		SubjectDigest: strings.Repeat("a", 64), CreatedAt: now,
	}
	store := &runtimeAttachmentRecoveryStore{preparationIntents: []application.TaskPreparationIntent{intent}}
	coordinator := runtimeTransitionCoordinator(t, runtimeRoot, store, now)
	descriptor, err := coordinator.pinRuntimeRoot()
	if err != nil {
		t.Fatal(err)
	}
	generation, generationID, err := createRuntimeAttachmentGeneration(descriptor, intent.TaskHandle)
	if err != nil {
		t.Fatal(err)
	}
	record := runtimeAttachmentIdentityRecord{
		Stage: runtimeAttachmentCreatingIntent, Generation: generation,
		GenerationID: generationID, RelaySeed: runtimeRelaySeedForTest(0x38),
	}
	if _, err := publishRuntimeAttachmentIdentity(descriptor, intent.TaskHandle, record, nil, nil); err != nil {
		t.Fatal(err)
	}
	stagingName := runtimeAttachmentCreationName(intent.TaskHandle)
	if err := unix.Mkdirat(descriptor, stagingName, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := closeRuntimeRootDescriptor(descriptor); err != nil {
		t.Fatal(err)
	}

	servers, err := coordinator.recoverRuntimeAttachments(context.Background())
	if err != nil || len(servers) != 0 {
		t.Fatalf("recoverRuntimeAttachments(unproven preparation) = %d, %v", len(servers), err)
	}
	if store.taskReads != 1 || len(store.preparationRefusals) != 1 {
		t.Fatalf("recovery progress = task reads %d, refusals %#v", store.taskReads, store.preparationRefusals)
	}
	refusal := store.preparationRefusals[0]
	if refusal.OperationID != intent.OperationID || refusal.TaskHandle != intent.TaskHandle ||
		refusal.SubjectDigest != intent.SubjectDigest || refusal.Reason != application.RuntimeAttachmentPreparationUnproven {
		t.Fatalf("preparation refusal = %#v", refusal)
	}
	if info, err := os.Lstat(filepath.Join(runtimeRoot, stagingName)); err != nil || !info.IsDir() {
		t.Fatalf("unproven staging path = %#v, %v", info, err)
	}

	restarted := runtimeTransitionCoordinator(t, runtimeRoot, store, now.Add(time.Minute))
	servers, err = restarted.recoverRuntimeAttachments(context.Background())
	if err != nil || len(servers) != 0 {
		t.Fatalf("recoverRuntimeAttachments(durable refusal) = %d, %v", len(servers), err)
	}
	if store.taskReads != 2 || len(store.preparationRefusals) != 1 {
		t.Fatalf("replayed recovery progress = task reads %d, refusals %#v", store.taskReads, store.preparationRefusals)
	}
}
