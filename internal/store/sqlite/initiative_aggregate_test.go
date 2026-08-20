package sqlite

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/comisai/comis-dev-crew/internal/application"
	"github.com/comisai/comis-dev-crew/internal/domain"
)

func TestInitiativeAggregateMovesAtomicallyWithMemberState(t *testing.T) {
	ctx := context.Background()
	store, initiativeHandle, activation := preparedInitiativeActivationStore(t)
	activated, err := store.CommitInitiativeActivation(ctx, activation)
	if err != nil {
		t.Fatalf("CommitInitiativeActivation() error = %v", err)
	}

	first, err := store.CommitTaskCancel(ctx, application.TaskCancelMutation{
		TaskHandle: "task-component-a", OperationID: "cancel-component-0001",
		SubjectDigest: strings.Repeat("a", 64), At: activation.At.Add(time.Minute),
	})
	if err != nil {
		t.Fatalf("CommitTaskCancel(component) error = %v", err)
	}
	initiative, err := store.GetInitiative(ctx, initiativeHandle)
	if err != nil {
		t.Fatalf("GetInitiative(blocked) error = %v", err)
	}
	if initiative.State != domain.InitiativeBlocked || initiative.StateVersion != first.Task.StateVersion ||
		!initiative.UpdatedAt.Equal(first.Task.UpdatedAt) {
		t.Fatalf("initiative after dependent cancellation = %#v, task %#v", initiative, first.Task)
	}

	second, err := store.CommitTaskCancel(ctx, application.TaskCancelMutation{
		TaskHandle: "task-integration", OperationID: "cancel-integration-0001",
		SubjectDigest: strings.Repeat("b", 64), At: activation.At.Add(2 * time.Minute),
	})
	if err != nil {
		t.Fatalf("CommitTaskCancel(integration) error = %v", err)
	}
	initiative, err = store.GetInitiative(ctx, initiativeHandle)
	if err != nil {
		t.Fatalf("GetInitiative(cancelled) error = %v", err)
	}
	if initiative.State != domain.InitiativeCancelled || initiative.StateVersion != second.Task.StateVersion ||
		initiative.StateVersion <= activated.Initiative.StateVersion {
		t.Fatalf("cancelled initiative = %#v, second task %#v", initiative, second.Task)
	}
}

func TestInitiativeAggregateFailureRollsBackTheMemberMutation(t *testing.T) {
	ctx := context.Background()
	store, initiativeHandle, activation := preparedInitiativeActivationStore(t)
	if _, err := store.CommitInitiativeActivation(ctx, activation); err != nil {
		t.Fatalf("CommitInitiativeActivation() error = %v", err)
	}
	if _, err := store.db.ExecContext(ctx, `UPDATE tasks SET state = 'candidate_complete'
		WHERE handle = 'task-component-a'`); err != nil {
		t.Fatalf("seed completed predecessor: %v", err)
	}
	if _, err := store.db.ExecContext(ctx, `CREATE TRIGGER refuse_integrating_aggregate
		BEFORE UPDATE ON initiatives WHEN NEW.state = 'integrating'
		BEGIN SELECT RAISE(ABORT, 'injected aggregate failure'); END`); err != nil {
		t.Fatalf("install aggregate failure trigger: %v", err)
	}
	mutation := application.TaskStartMutation{
		TaskHandle: "task-integration", OperationID: "start-integration-0001",
		SubjectDigest: strings.Repeat("c", 64), At: activation.At.Add(time.Minute),
		SchedulingLimits: initiativeTestSchedulingLimits(2),
	}
	if _, err := store.CommitTaskStart(ctx, mutation); err == nil {
		t.Fatal("CommitTaskStart(injected aggregate failure) error = nil")
	}
	task, err := store.GetTask(ctx, mutation.TaskHandle)
	if err != nil || task.State != domain.TaskReady {
		t.Fatalf("integration task after rollback = %#v, %v", task, err)
	}
	initiative, err := store.GetInitiative(ctx, initiativeHandle)
	if err != nil || initiative.State != domain.InitiativeActive {
		t.Fatalf("initiative after rollback = %#v, %v", initiative, err)
	}
	if _, err := store.GetOperation(ctx, mutation.OperationID); !errors.Is(err, application.ErrNotFound) {
		t.Fatalf("start operation after rollback error = %v, want ErrNotFound", err)
	}
}

func TestInitiativeMemberStartRequiresAtomicSchedulerAuthority(t *testing.T) {
	ctx := context.Background()
	store, _, activation := preparedInitiativeActivationStore(t)
	if _, err := store.CommitInitiativeActivation(ctx, activation); err != nil {
		t.Fatalf("CommitInitiativeActivation() error = %v", err)
	}
	mutation := application.TaskStartMutation{
		TaskHandle: "task-integration", OperationID: "start-held-integration-0001",
		SubjectDigest: strings.Repeat("d", 64), At: activation.At.Add(time.Minute),
		SchedulingLimits: initiativeTestSchedulingLimits(2),
	}
	if _, err := store.CommitTaskStart(ctx, mutation); !errors.Is(err, application.ErrPrecondition) {
		t.Fatalf("CommitTaskStart(dependency-held initiative member) error = %v, want ErrPrecondition", err)
	}
	task, err := store.GetTask(ctx, mutation.TaskHandle)
	if err != nil || task.State != domain.TaskReady {
		t.Fatalf("held integration task = %#v, %v", task, err)
	}
	if _, err := store.GetOperation(ctx, mutation.OperationID); !errors.Is(err, application.ErrNotFound) {
		t.Fatalf("held start operation error = %v, want ErrNotFound", err)
	}
	allowed := application.TaskStartMutation{
		TaskHandle: "task-component-a", OperationID: "start-ready-component-0001",
		SubjectDigest: strings.Repeat("e", 64), At: activation.At.Add(2 * time.Minute),
		SchedulingLimits: initiativeTestSchedulingLimits(2),
	}
	started, err := store.CommitTaskStart(ctx, allowed)
	if err != nil || started.Task.State != domain.TaskLaunching {
		t.Fatalf("CommitTaskStart(dependency-ready component) = %#v, %v", started, err)
	}
}

func TestInitiativeMemberStartFailsClosedWithoutCapacityAuthority(t *testing.T) {
	ctx := context.Background()
	store, _, activation := preparedInitiativeActivationStore(t)
	if _, err := store.CommitInitiativeActivation(ctx, activation); err != nil {
		t.Fatalf("CommitInitiativeActivation() error = %v", err)
	}
	withoutLimits := application.TaskStartMutation{
		TaskHandle: "task-component-a", OperationID: "start-without-limits-0001",
		SubjectDigest: strings.Repeat("f", 64), At: activation.At.Add(time.Minute),
	}
	if _, err := store.CommitTaskStart(ctx, withoutLimits); !errors.Is(err, application.ErrPrecondition) {
		t.Fatalf("CommitTaskStart(without limits) error = %v, want ErrPrecondition", err)
	}

	occupied := storeTask("task-standalone-worker", 1)
	occupied.State = domain.TaskWorking
	occupied.ManagedRunID = "managed-run-standalone-worker"
	occupied.WorkspaceLeaseID = "workspace-lease-standalone-worker"
	occupied.CreatedAt = activation.At
	occupied.UpdatedAt = activation.At
	occupied, err := occupied.PinBriefRevision()
	if err != nil {
		t.Fatalf("PinBriefRevision(occupied) error = %v", err)
	}
	if err := store.CreateTask(ctx, occupied); err != nil {
		t.Fatalf("CreateTask(occupied) error = %v", err)
	}
	queued := withoutLimits
	queued.OperationID = "start-capacity-queued-0001"
	queued.SubjectDigest = strings.Repeat("1", 64)
	queued.SchedulingLimits = initiativeTestSchedulingLimits(1)
	if _, err := store.CommitTaskStart(ctx, queued); !errors.Is(err, application.ErrPrecondition) {
		t.Fatalf("CommitTaskStart(capacity queued) error = %v, want ErrPrecondition", err)
	}
}

func initiativeTestSchedulingLimits(maximum int) *application.InitiativeSchedulingLimits {
	return &application.InitiativeSchedulingLimits{
		MaxConcurrentTasks: maximum, MaxConcurrentTasksPerRepository: maximum,
		WorkerProfileLimits: map[string]int{"codex-standard": maximum},
	}
}
