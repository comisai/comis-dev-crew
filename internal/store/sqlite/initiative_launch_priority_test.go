package sqlite

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/comisai/comis-dev-crew/internal/application"
	"github.com/comisai/comis-dev-crew/internal/domain"
)

func TestInitiativeLaunchAuthorizationPreservesPriorityBeyondFirstPage(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, filepath.Join(canonicalTempDir(t), "priority-pages.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	transaction, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = transaction.Rollback() }()
	created := time.Date(2026, time.August, 24, 0, 0, 0, 0, time.UTC)
	for index := 0; index < maximumInitiativeSchedulingFrontier; index++ {
		producer := schedulingTestTask(fmt.Sprintf("task-priority-%04d-producer", index), domain.TaskPrepared, index)
		consumer := schedulingTestTask(fmt.Sprintf("task-priority-%04d-consumer", index), domain.TaskReady, index)
		if err := insertTask(ctx, transaction, producer); err != nil {
			t.Fatalf("insert producer %d: %v", index, err)
		}
		if err := insertTask(ctx, transaction, consumer); err != nil {
			t.Fatalf("insert consumer %d: %v", index, err)
		}
		initiative := schedulingTestInitiative(
			fmt.Sprintf("initiative-priority-%04d", index), created.Add(time.Duration(index)*time.Second),
			[]string{producer.Handle, consumer.Handle},
		)
		initiative.Edges = []domain.InitiativeEdge{{
			FromTaskHandle: producer.Handle, ToTaskHandle: consumer.Handle, Kind: domain.EdgeBlocksStart,
		}}
		if err := insertInitiative(ctx, transaction, initiative); err != nil {
			t.Fatalf("insert blocked initiative %d: %v", index, err)
		}
	}
	older := schedulingTestTask("task-priority-older-eligible", domain.TaskReady,
		maximumInitiativeSchedulingFrontier)
	target := schedulingTestTask("task-priority-requested", domain.TaskReady,
		maximumInitiativeSchedulingFrontier+1)
	for offset, item := range []struct {
		task       domain.Task
		initiative string
	}{
		{task: older, initiative: "initiative-priority-older-eligible"},
		{task: target, initiative: "initiative-priority-requested"},
	} {
		if err := insertTask(ctx, transaction, item.task); err != nil {
			t.Fatal(err)
		}
		initiative := schedulingTestInitiative(item.initiative,
			created.Add(time.Duration(maximumInitiativeSchedulingFrontier+offset)*time.Second),
			[]string{item.task.Handle})
		if err := insertInitiative(ctx, transaction, initiative); err != nil {
			t.Fatal(err)
		}
	}
	limits := initiativeTestSchedulingLimits(1)
	err = authorizeInitiativeTaskStart(ctx, transaction, target, limits)
	if !errors.Is(err, application.ErrPrecondition) {
		t.Fatalf("authorizeInitiativeTaskStart(later target) error = %v, want queued precondition", err)
	}
}

func schedulingTestTask(handle string, state domain.TaskState, version int) domain.Task {
	task := storeTask(handle, int64(version+1))
	task.State = state
	if state == domain.TaskReady {
		task.ManagedRunID = "managed-run_" + handle
		task.WorkspaceLeaseID = "workspace-lease_" + handle
	}
	return task
}

func schedulingTestInitiative(handle string, created time.Time, tasks []string) domain.DevelopmentInitiative {
	initiative := persistenceInitiative(handle, domain.InitiativeActive, 1)
	initiative.BaseRevisionSet[0].RepositoryID = "product-api"
	initiative.Components = []domain.InitiativeComponent{{
		ComponentHandle: "component-priority", RepositoryID: "product-api",
		ResponsibilityRef: "responsibility-ref-priority", TaskHandles: tasks,
	}}
	initiative.Edges = nil
	initiative.ContractArtifacts = nil
	initiative.IntegrationOwnerTask = ""
	initiative.CreatedAt = created
	initiative.UpdatedAt = created
	return initiative
}
