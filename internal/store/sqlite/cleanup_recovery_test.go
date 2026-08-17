package sqlite

import (
	"context"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/comisai/comis-dev-crew/internal/application"
)

func TestTaskCleanupStore_ReadsHostReleasedStageAcrossRestart(t *testing.T) {
	databasePath := filepath.Join(canonicalTempDir(t), "devcrew.db")
	store, task, sealed := deliveredCleanupFixture(t, databasePath)
	mutation := cleanupTestMutation(task, "cleanup-recovery-read")
	record, err := store.BeginTaskCleanup(context.Background(), mutation)
	if err != nil {
		t.Fatalf("BeginTaskCleanup() error = %v", err)
	}
	snapshot, truth, receipt := cleanupTestProof(task, sealed, record, mutation.ReleasedAt)
	released, err := store.RecordTaskCleanupHostRelease(context.Background(), application.TaskCleanupHostReleaseMutation{
		OperationID: record.OperationID, SubjectDigest: record.SubjectDigest,
		Snapshot: snapshot, DeliveryTruth: truth, Receipt: receipt, At: mutation.At.Add(time.Minute),
	})
	if err != nil {
		t.Fatalf("RecordTaskCleanupHostRelease() error = %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	restarted, err := Open(context.Background(), databasePath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = restarted.Close() })
	got, found, err := restarted.GetTaskCleanupRecord(context.Background(), task.Handle)
	if err != nil || !found || !reflect.DeepEqual(got, released) {
		t.Fatalf("GetTaskCleanupRecord() = %#v, %t, %v; want %#v", got, found, err, released)
	}
	if _, found, err := restarted.GetTaskCleanupRecord(context.Background(), "task-without-cleanup-record"); err != nil || found {
		t.Fatalf("GetTaskCleanupRecord(missing) found = %t, error = %v", found, err)
	}
}
