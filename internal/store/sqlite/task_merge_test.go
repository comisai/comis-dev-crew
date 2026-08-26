package sqlite

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/comisai/comis-dev-crew/internal/application"
	"github.com/comisai/comis-dev-crew/internal/domain"
)

func TestTaskMergeStoreCannotReserveDeferredDeliveryMode(t *testing.T) {
	store, err := Open(context.Background(), filepath.Join(canonicalTempDir(t), "devcrew.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	task := candidateEvidenceTask(t, "task-merge-deferred")
	task.DeliveryMode = domain.DeliveryMergeAfterApproval
	if err := store.CreateTask(context.Background(), task); err == nil {
		t.Fatal("CreateTask(merge_after_approval) error = nil")
	}
	request := application.TaskMergeReservation{
		OperationID: "merge-operation-deferred", TaskHandle: task.Handle,
		SubjectDigest: task.BriefRevisionHash, At: task.UpdatedAt,
	}
	if _, err := store.BeginTaskMerge(context.Background(), request); !errors.Is(err, application.ErrNotFound) {
		t.Fatalf("BeginTaskMerge(deferred task) error = %v, want ErrNotFound", err)
	}
}
