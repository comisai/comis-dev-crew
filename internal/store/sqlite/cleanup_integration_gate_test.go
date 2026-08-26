package sqlite

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/comisai/comis-dev-crew/internal/application"
	"github.com/comisai/comis-dev-crew/internal/domain"
)

func TestCleanupBlocksCandidateStillOwedToIntegrationOwner(t *testing.T) {
	store, task, _ := deliveredCleanupFixture(t, filepath.Join(canonicalTempDir(t), "cleanup-integration.db"))
	initiative := persistenceInitiative("initiative-cleanup-integration", domain.InitiativeActive, task.StateVersion)
	initiative.BaseRevisionSet[0].RepositoryID = task.RepositoryID
	initiative.Components[0].RepositoryID = task.RepositoryID
	initiative.Components[0].TaskHandles = []string{task.Handle}
	initiative.Components[1].RepositoryID = task.RepositoryID
	initiative.Components[1].TaskHandles = []string{"task-cleanup-integration"}
	initiative.Edges[0].FromTaskHandle = task.Handle
	initiative.Edges[0].ToTaskHandle = "task-cleanup-integration"
	initiative.IntegrationOwnerTask = "task-cleanup-integration"
	if err := store.CreateInitiative(context.Background(), initiative); err != nil {
		t.Fatalf("CreateInitiative() error = %v", err)
	}

	_, err := store.BeginTaskCleanup(context.Background(), cleanupTestMutation(task, "cleanup-before-integration"))
	if !errors.Is(err, application.ErrPrecondition) {
		t.Fatalf("BeginTaskCleanup(without applied integration) error = %v", err)
	}
	current, readErr := store.GetTask(context.Background(), task.Handle)
	if readErr != nil || current.State != domain.TaskDelivered {
		t.Fatalf("task after refused cleanup = %#v, %v", current, readErr)
	}
}
