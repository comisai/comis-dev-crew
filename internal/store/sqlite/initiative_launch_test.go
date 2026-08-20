package sqlite

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/comisai/comis-dev-crew/internal/domain"
)

func TestInitiativeLaunchAuthorizationDoesNotClaimStandaloneTasks(t *testing.T) {
	store, err := Open(context.Background(), filepath.Join(canonicalTempDir(t), "devcrew.db"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	task := storeTask("task-standalone-launch", 1)
	task.State = domain.TaskReady
	task.ManagedRunID = "managed-run-standalone-launch"
	task.WorkspaceLeaseID = "workspace-lease-standalone-launch"
	if err := store.CreateTask(context.Background(), task); err != nil {
		t.Fatalf("CreateTask() error = %v", err)
	}
	transaction, err := store.db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatalf("BeginTx() error = %v", err)
	}
	defer func() { _ = transaction.Rollback() }()
	if err := authorizeInitiativeTaskStart(context.Background(), transaction, task, nil); err != nil {
		t.Fatalf("authorizeInitiativeTaskStart(standalone) error = %v", err)
	}
}
