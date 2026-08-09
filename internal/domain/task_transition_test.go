package domain

import (
	"errors"
	"math"
	"reflect"
	"testing"
	"time"
)

func TestTaskApplyTransition_AdvancesCanonicalE0Lifecycle(t *testing.T) {
	task := validTask(ShapeShip, DeliveryPullRequest)
	transitions := []struct {
		kind TaskTransition
		want TaskState
	}{
		{kind: TransitionBindAcknowledged, want: TaskReady},
		{kind: TransitionLaunchRequested, want: TaskLaunching},
		{kind: TransitionWorkerAcknowledged, want: TaskWorking},
		{kind: TransitionValidationStarted, want: TaskValidating},
		{kind: TransitionValidationAccepted, want: TaskCandidateComplete},
		{kind: TransitionDeliveryStarted, want: TaskDelivering},
		{kind: TransitionDeliveryAccepted, want: TaskDelivered},
		{kind: TransitionCleanupStarted, want: TaskCleanupHeld},
		{kind: TransitionCleanupAccepted, want: TaskCleaned},
	}

	for index, transition := range transitions {
		at := task.UpdatedAt.Add(time.Duration(index+1) * time.Second)
		previousVersion := task.StateVersion
		var err error
		task, err = task.ApplyTransition(transition.kind, at)
		if err != nil {
			t.Fatalf("ApplyTransition(%q) error = %v", transition.kind, err)
		}
		if task.State != transition.want {
			t.Fatalf("ApplyTransition(%q) state = %q, want %q", transition.kind, task.State, transition.want)
		}
		if task.StateVersion != previousVersion+1 {
			t.Fatalf("ApplyTransition(%q) version = %d, want %d", transition.kind, task.StateVersion, previousVersion+1)
		}
		if !task.UpdatedAt.Equal(at) {
			t.Fatalf("ApplyTransition(%q) updatedAt = %v, want %v", transition.kind, task.UpdatedAt, at)
		}
	}
}

func TestTaskApplyTransition_ModelsDecisionBlockAndPauseWithoutWideningState(t *testing.T) {
	task := transitionTaskToWorking(t)
	transitions := []struct {
		kind TaskTransition
		want TaskState
	}{
		{kind: TransitionDecisionRequested, want: TaskAwaitingDecision},
		{kind: TransitionDecisionAnswered, want: TaskWorking},
		{kind: TransitionBlocked, want: TaskBlocked},
		{kind: TransitionResumed, want: TaskWorking},
		{kind: TransitionPaused, want: TaskPaused},
		{kind: TransitionResumed, want: TaskWorking},
		{kind: TransitionFailureObserved, want: TaskFailed},
		{kind: TransitionCleanupStarted, want: TaskCleanupHeld},
	}
	for _, transition := range transitions {
		var err error
		task, err = task.ApplyTransition(transition.kind, task.UpdatedAt.Add(time.Second))
		if err != nil {
			t.Fatalf("ApplyTransition(%q) error = %v", transition.kind, err)
		}
		if task.State != transition.want {
			t.Fatalf("ApplyTransition(%q) state = %q, want %q", transition.kind, task.State, transition.want)
		}
	}
}

func TestTaskApplyTransition_ReconciliationFailsClosed(t *testing.T) {
	task := transitionTaskToWorking(t)
	var err error
	task, err = task.ApplyTransition(TransitionReconcileRequired, task.UpdatedAt.Add(time.Second))
	if err != nil {
		t.Fatalf("start reconciliation: %v", err)
	}
	if task.State != TaskReconciling {
		t.Fatalf("state = %q, want %q", task.State, TaskReconciling)
	}

	task, err = task.ApplyTransition(TransitionReconciliationUnresolved, task.UpdatedAt.Add(time.Second))
	if err != nil {
		t.Fatalf("record unresolved reconciliation: %v", err)
	}
	if task.State != TaskUnknown {
		t.Fatalf("state = %q, want %q", task.State, TaskUnknown)
	}

	if _, err := task.ApplyTransition(TransitionResumed, task.UpdatedAt.Add(time.Second)); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("resume unknown error = %v, want ErrInvalidTransition", err)
	}
	task, err = task.ApplyTransition(TransitionReconcileRequired, task.UpdatedAt.Add(time.Second))
	if err != nil {
		t.Fatalf("reconcile unknown: %v", err)
	}
	task, err = task.ApplyTransition(TransitionReconciledWorking, task.UpdatedAt.Add(time.Second))
	if err != nil {
		t.Fatalf("apply verified reconciliation: %v", err)
	}
	if task.State != TaskWorking {
		t.Fatalf("verified state = %q, want %q", task.State, TaskWorking)
	}
}

func TestTaskAcknowledgeBinding_RequiresExactHostAndWorkspaceIdentity(t *testing.T) {
	task := validTask(ShapeShip, DeliveryPullRequest)
	binding := TaskBinding{ManagedRunID: "managed-run-0001", WorkspaceLeaseID: "workspace-lease-0001"}
	bound, err := task.AcknowledgeBinding(binding, task.UpdatedAt.Add(time.Second))
	if err != nil {
		t.Fatalf("AcknowledgeBinding() error = %v", err)
	}
	if bound.State != TaskReady || bound.ManagedRunID != binding.ManagedRunID || bound.WorkspaceLeaseID != binding.WorkspaceLeaseID {
		t.Fatalf("bound task = %#v, want ready with exact binding", bound)
	}
	if _, err := task.AcknowledgeBinding(TaskBinding{ManagedRunID: "", WorkspaceLeaseID: binding.WorkspaceLeaseID}, task.UpdatedAt.Add(time.Second)); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("partial binding error = %v, want ErrInvalidTransition", err)
	}
	if _, err := bound.AcknowledgeBinding(TaskBinding{ManagedRunID: "managed-run-other", WorkspaceLeaseID: binding.WorkspaceLeaseID}, bound.UpdatedAt.Add(time.Second)); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("altered binding error = %v, want ErrInvalidTransition", err)
	}
	replayed, err := bound.AcknowledgeBinding(binding, bound.UpdatedAt.Add(time.Second))
	if err != nil {
		t.Fatalf("identical binding replay error = %v", err)
	}
	if !reflect.DeepEqual(replayed, bound) {
		t.Fatalf("identical binding replay mutated task: got %#v want %#v", replayed, bound)
	}
}

func TestTaskApplyTransition_RejectsUnknownIllegalOrNonMonotonicChange(t *testing.T) {
	base := validTask(ShapeShip, DeliveryPullRequest)
	tests := []struct {
		name string
		task Task
		kind TaskTransition
		at   time.Time
	}{
		{name: "unknown transition", task: base, kind: TaskTransition("invented"), at: base.UpdatedAt.Add(time.Second)},
		{name: "skip binding", task: base, kind: TransitionLaunchRequested, at: base.UpdatedAt.Add(time.Second)},
		{name: "time moves backwards", task: base, kind: TransitionBindAcknowledged, at: base.UpdatedAt.Add(-time.Second)},
		{name: "time is not UTC", task: base, kind: TransitionBindAcknowledged, at: base.UpdatedAt.In(time.FixedZone("offset", 3600))},
	}
	exhausted := base
	exhausted.StateVersion = math.MaxInt64
	tests = append(tests, struct {
		name string
		task Task
		kind TaskTransition
		at   time.Time
	}{name: "state version exhausted", task: exhausted, kind: TransitionBindAcknowledged, at: exhausted.UpdatedAt.Add(time.Second)})
	cleaned := base
	cleaned.State = TaskCleaned
	tests = append(tests, struct {
		name string
		task Task
		kind TaskTransition
		at   time.Time
	}{name: "cleaned is terminal", task: cleaned, kind: TransitionReconcileRequired, at: cleaned.UpdatedAt.Add(time.Second)})

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := test.task.ApplyTransition(test.kind, test.at)
			if !errors.Is(err, ErrInvalidTransition) {
				t.Fatalf("ApplyTransition() error = %v, want ErrInvalidTransition", err)
			}
			var transitionError *TransitionError
			if !errors.As(err, &transitionError) || transitionError.Error() == "" {
				t.Fatalf("ApplyTransition() error = %v, want safe typed TransitionError", err)
			}
			if !reflect.DeepEqual(got, test.task) {
				t.Fatalf("failed transition mutated task: got %#v want %#v", got, test.task)
			}
		})
	}
}

func transitionTaskToWorking(t *testing.T) Task {
	t.Helper()
	task := validTask(ShapeShip, DeliveryPullRequest)
	for _, transition := range []TaskTransition{
		TransitionBindAcknowledged,
		TransitionLaunchRequested,
		TransitionWorkerAcknowledged,
	} {
		var err error
		task, err = task.ApplyTransition(transition, task.UpdatedAt.Add(time.Second))
		if err != nil {
			t.Fatalf("ApplyTransition(%q) error = %v", transition, err)
		}
	}
	return task
}
