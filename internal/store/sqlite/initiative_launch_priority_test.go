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
	if err := authorizeInitiativeTaskStart(ctx, transaction, older, limits); err != nil {
		t.Fatalf("authorizeInitiativeTaskStart(oldest eligible) error = %v", err)
	}
	err = authorizeInitiativeTaskStart(ctx, transaction, target, limits)
	if !errors.Is(err, application.ErrPrecondition) {
		t.Fatalf("authorizeInitiativeTaskStart(later target) error = %v, want queued precondition", err)
	}
}

func TestInitiativeLaunchAuthorizationSkipsPagedIneligibleHistory(t *testing.T) {
	for _, kind := range []string{"dependency", "resource"} {
		t.Run(kind, func(t *testing.T) {
			ctx := context.Background()
			store, err := Open(ctx, filepath.Join(canonicalTempDir(t), "paged-ineligible.db"))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = store.Close() })
			transaction, err := store.db.BeginTx(ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = transaction.Rollback() }()
			created := time.Date(2026, time.August, 25, 0, 0, 0, 0, time.UTC)
			limits := initiativeTestSchedulingLimits(1)
			if kind == "resource" {
				occupied := schedulingTestTask("task-paged-resource-occupied", domain.TaskWorking, 0)
				occupied.RepositoryID = "repo-capped"
				occupied.ManagedRunID = "managed-run-paged-resource"
				occupied.WorkspaceLeaseID = "workspace-lease-paged-resource"
				occupied, err = occupied.PinBriefRevision()
				if err != nil {
					t.Fatal(err)
				}
				if err := insertTask(ctx, transaction, occupied); err != nil {
					t.Fatal(err)
				}
				limits = initiativeTestSchedulingLimits(2)
				limits.MaxConcurrentTasksPerRepository = 1
			}
			prefix := "initiative-paged-" + kind + "-ineligible-"
			for index := 0; index <= initiativeSchedulingPageSize; index++ {
				handle := fmt.Sprintf("%s%04d", prefix, index)
				if kind == "dependency" {
					producer := schedulingTestTask(fmt.Sprintf("task-paged-dependency-%04d-producer", index), domain.TaskPrepared, index*2)
					consumer := schedulingTestTask(fmt.Sprintf("task-paged-dependency-%04d-consumer", index), domain.TaskReady, index*2+1)
					if err := insertTask(ctx, transaction, producer); err != nil {
						t.Fatal(err)
					}
					if err := insertTask(ctx, transaction, consumer); err != nil {
						t.Fatal(err)
					}
					initiative := schedulingTestInitiative(handle, created.Add(time.Duration(index)*time.Second), []string{producer.Handle, consumer.Handle})
					initiative.Edges = []domain.InitiativeEdge{{
						FromTaskHandle: producer.Handle, ToTaskHandle: consumer.Handle, Kind: domain.EdgeBlocksStart,
					}}
					if err := insertInitiative(ctx, transaction, initiative); err != nil {
						t.Fatal(err)
					}
					continue
				}
				task := schedulingTestTask(fmt.Sprintf("task-paged-resource-%04d", index), domain.TaskReady, index)
				task.RepositoryID = "repo-capped"
				task, err = task.PinBriefRevision()
				if err != nil {
					t.Fatal(err)
				}
				if err := insertTask(ctx, transaction, task); err != nil {
					t.Fatal(err)
				}
				initiative := schedulingTestInitiative(handle, created.Add(time.Duration(index)*time.Second), []string{task.Handle})
				initiative.BaseRevisionSet[0].RepositoryID = task.RepositoryID
				initiative.Components[0].RepositoryID = task.RepositoryID
				if err := insertInitiative(ctx, transaction, initiative); err != nil {
					t.Fatal(err)
				}
			}
			older := schedulingTestTask("task-paged-"+kind+"-oldest-eligible", domain.TaskReady, 1000)
			target := schedulingTestTask("task-paged-"+kind+"-requested", domain.TaskReady, 1001)
			for offset, item := range []struct {
				task       domain.Task
				initiative string
			}{
				{task: older, initiative: "initiative-paged-" + kind + "-oldest-eligible"},
				{task: target, initiative: "initiative-paged-" + kind + "-requested"},
			} {
				if err := insertTask(ctx, transaction, item.task); err != nil {
					t.Fatal(err)
				}
				initiative := schedulingTestInitiative(item.initiative,
					created.Add(time.Duration(initiativeSchedulingPageSize+1+offset)*time.Second), []string{item.task.Handle})
				if err := insertInitiative(ctx, transaction, initiative); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := transaction.ExecContext(ctx,
				`UPDATE initiatives SET components_json = '{' WHERE handle LIKE ?`, prefix+"%",
			); err != nil {
				t.Fatal(err)
			}
			if kind == "resource" {
				var heads int
				if err := transaction.QueryRowContext(ctx, `SELECT COUNT(*)
					FROM initiative_launch_resource_heads WHERE repository_id = 'repo-capped'`).Scan(&heads); err != nil {
					t.Fatalf("read persisted capped-resource frontier: %v", err)
				}
				if heads != 1 {
					t.Fatalf("persisted capped-resource frontier heads = %d, want 1", heads)
				}
			}
			if err := authorizeInitiativeTaskStart(ctx, transaction, older, limits); err != nil {
				t.Fatalf("authorizeInitiativeTaskStart(oldest eligible after %s history) error = %v", kind, err)
			}
			err = authorizeInitiativeTaskStart(ctx, transaction, target, limits)
			if !errors.Is(err, application.ErrPrecondition) {
				t.Fatalf("authorizeInitiativeTaskStart(later target after %s history) error = %v, want queued precondition", kind, err)
			}
		})
	}
}

func TestInitiativeLaunchAuthorizationAdvancesWithinResourceForEverySlot(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, filepath.Join(canonicalTempDir(t), "resource-frontier-slots.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	transaction, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = transaction.Rollback() }()
	created := time.Date(2026, time.August, 25, 12, 0, 0, 0, time.UTC)
	items := []struct {
		task       domain.Task
		initiative string
		repository string
	}{
		{task: schedulingTestTask("task-resource-slot-first", domain.TaskReady, 1), initiative: "initiative-resource-slot-first", repository: "product-api"},
		{task: schedulingTestTask("task-resource-slot-second", domain.TaskReady, 2), initiative: "initiative-resource-slot-second", repository: "product-api"},
		{task: schedulingTestTask("task-resource-slot-later", domain.TaskReady, 3), initiative: "initiative-resource-slot-later", repository: "other-api"},
	}
	for index := range items {
		items[index].task.RepositoryID = items[index].repository
		items[index].task, err = items[index].task.PinBriefRevision()
		if err != nil {
			t.Fatal(err)
		}
		if err := insertTask(ctx, transaction, items[index].task); err != nil {
			t.Fatal(err)
		}
		initiative := schedulingTestInitiative(items[index].initiative,
			created.Add(time.Duration(index)*time.Second), []string{items[index].task.Handle})
		initiative.BaseRevisionSet[0].RepositoryID = items[index].repository
		initiative.Components[0].RepositoryID = items[index].repository
		if err := insertInitiative(ctx, transaction, initiative); err != nil {
			t.Fatal(err)
		}
	}
	limits := initiativeTestSchedulingLimits(2)
	limits.MaxConcurrentTasksPerRepository = 2
	if err := authorizeInitiativeTaskStart(ctx, transaction, items[1].task, limits); err != nil {
		t.Fatalf("authorizeInitiativeTaskStart(second older resource task) error = %v", err)
	}
	if err := authorizeInitiativeTaskStart(ctx, transaction, items[2].task, limits); !errors.Is(err, application.ErrPrecondition) {
		t.Fatalf("authorizeInitiativeTaskStart(later other-resource task) error = %v, want queued", err)
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
