package domain

import (
	"errors"
	"fmt"
	"math"
	"time"
)

// ErrInvalidTransition identifies a lifecycle change that is unknown, unsafe,
// or not valid from the task's current state.
var ErrInvalidTransition = errors.New("invalid task transition")

// TaskTransition is the closed E0 lifecycle event vocabulary. It deliberately
// excludes initiative contract-staleness and operator-custody events.
type TaskTransition string

const (
	TransitionBindAcknowledged         TaskTransition = "bind_acknowledged"
	TransitionPreparationPreserved     TaskTransition = "preparation_preserved"
	TransitionPreparationAbandoned     TaskTransition = "preparation_abandoned"
	TransitionLaunchRequested          TaskTransition = "launch_requested"
	TransitionWorkerAcknowledged       TaskTransition = "worker_acknowledged"
	TransitionDecisionRequested        TaskTransition = "decision_requested"
	TransitionDecisionAnswered         TaskTransition = "decision_answered"
	TransitionBlocked                  TaskTransition = "blocked"
	TransitionResumed                  TaskTransition = "resumed"
	TransitionPaused                   TaskTransition = "paused"
	TransitionValidationStarted        TaskTransition = "validation_started"
	TransitionValidationAccepted       TaskTransition = "validation_accepted"
	TransitionDeliveryStarted          TaskTransition = "delivery_started"
	TransitionDeliveryAccepted         TaskTransition = "delivery_accepted"
	TransitionFailureObserved          TaskTransition = "failure_observed"
	TransitionCancelRequested          TaskTransition = "cancel_requested"
	TransitionCleanupStarted           TaskTransition = "cleanup_started"
	TransitionCleanupAccepted          TaskTransition = "cleanup_accepted"
	TransitionReconcileRequired        TaskTransition = "reconcile_required"
	TransitionReconciliationUnresolved TaskTransition = "reconciliation_unresolved"
	TransitionReconciledPrepared       TaskTransition = "reconciled_prepared"
	TransitionReconciledReady          TaskTransition = "reconciled_ready"
	TransitionReconciledLaunching      TaskTransition = "reconciled_launching"
	TransitionReconciledWorking        TaskTransition = "reconciled_working"
	TransitionReconciledDecision       TaskTransition = "reconciled_awaiting_decision"
	TransitionReconciledBlocked        TaskTransition = "reconciled_blocked"
	TransitionReconciledPaused         TaskTransition = "reconciled_paused"
	TransitionReconciledValidating     TaskTransition = "reconciled_validating"
	TransitionReconciledCandidate      TaskTransition = "reconciled_candidate_complete"
	TransitionReconciledDelivering     TaskTransition = "reconciled_delivering"
	TransitionReconciledDelivered      TaskTransition = "reconciled_delivered"
	TransitionReconciledFailed         TaskTransition = "reconciled_failed"
	TransitionReconciledCancelled      TaskTransition = "reconciled_cancelled"
	TransitionReconciledCleanupHeld    TaskTransition = "reconciled_cleanup_held"
)

// TransitionError reports only closed lifecycle identities. It never includes
// task content or an adapter cause.
type TransitionError struct {
	From       TaskState
	Transition TaskTransition
	Reason     string
}

func (failure *TransitionError) Error() string {
	return fmt.Sprintf("%v: %s from %s: %s", ErrInvalidTransition, failure.Transition, failure.From, failure.Reason)
}

func (failure *TransitionError) Unwrap() error { return ErrInvalidTransition }

// ApplyTransition returns a new validated record with a monotonic state version.
// A failed transition returns the original record unchanged.
func (task Task) ApplyTransition(transition TaskTransition, occurredAt time.Time) (Task, error) {
	if err := task.Validate(); err != nil {
		return task, transitionFailure(task, transition, "current task is invalid")
	}
	if occurredAt.Location() != time.UTC || occurredAt.Before(task.UpdatedAt) {
		return task, transitionFailure(task, transition, "transition time must be monotonic UTC")
	}
	if task.StateVersion == math.MaxInt64 {
		return task, transitionFailure(task, transition, "state version is exhausted")
	}

	next, ok := nextTaskState(task.State, transition)
	if !ok {
		return task, transitionFailure(task, transition, "transition is not valid from current state")
	}
	updated := task
	updated.State = next
	updated.StateVersion++
	updated.UpdatedAt = occurredAt
	if err := updated.Validate(); err != nil {
		return task, transitionFailure(task, transition, "transition would create an invalid task")
	}
	return updated, nil
}

func transitionFailure(task Task, transition TaskTransition, reason string) error {
	return &TransitionError{From: task.State, Transition: transition, Reason: reason}
}

func nextTaskState(current TaskState, transition TaskTransition) (TaskState, bool) {
	if transition == TransitionReconcileRequired {
		if current != TaskCleaned && current != TaskReconciling {
			return TaskReconciling, true
		}
		return "", false
	}
	if current == TaskReconciling {
		return reconciledTaskState(transition)
	}

	switch transition {
	case TransitionBindAcknowledged:
		return requiredTaskState(current, TaskPrepared, TaskReady)
	case TransitionPreparationPreserved:
		return requiredTaskState(current, TaskPrepared, TaskPrepared)
	case TransitionPreparationAbandoned:
		return requiredTaskState(current, TaskPrepared, TaskCancelled)
	case TransitionLaunchRequested:
		return requiredTaskState(current, TaskReady, TaskLaunching)
	case TransitionWorkerAcknowledged:
		return requiredTaskState(current, TaskLaunching, TaskWorking)
	case TransitionDecisionRequested:
		return requiredTaskState(current, TaskWorking, TaskAwaitingDecision)
	case TransitionDecisionAnswered:
		return requiredTaskState(current, TaskAwaitingDecision, TaskWorking)
	case TransitionBlocked:
		return oneOfTaskStates(current, TaskBlocked, TaskWorking, TaskAwaitingDecision)
	case TransitionPaused:
		return oneOfTaskStates(current, TaskPaused, TaskReady, TaskWorking, TaskAwaitingDecision, TaskBlocked)
	case TransitionResumed:
		return oneOfTaskStates(current, TaskWorking, TaskPaused, TaskBlocked)
	case TransitionValidationStarted:
		return requiredTaskState(current, TaskWorking, TaskValidating)
	case TransitionValidationAccepted:
		return requiredTaskState(current, TaskValidating, TaskCandidateComplete)
	case TransitionDeliveryStarted:
		return requiredTaskState(current, TaskCandidateComplete, TaskDelivering)
	case TransitionDeliveryAccepted:
		return requiredTaskState(current, TaskDelivering, TaskDelivered)
	case TransitionFailureObserved:
		return oneOfTaskStates(current, TaskFailed, TaskPrepared, TaskReady, TaskLaunching, TaskWorking,
			TaskAwaitingDecision, TaskBlocked, TaskPaused, TaskValidating, TaskCandidateComplete, TaskDelivering)
	case TransitionCancelRequested:
		return oneOfTaskStates(current, TaskCancelled, TaskPrepared, TaskReady, TaskLaunching, TaskWorking,
			TaskAwaitingDecision, TaskBlocked, TaskPaused, TaskValidating, TaskCandidateComplete)
	case TransitionCleanupStarted:
		return oneOfTaskStates(current, TaskCleanupHeld, TaskDelivered, TaskFailed, TaskCancelled)
	case TransitionCleanupAccepted:
		return requiredTaskState(current, TaskCleanupHeld, TaskCleaned)
	default:
		return "", false
	}
}

func reconciledTaskState(transition TaskTransition) (TaskState, bool) {
	states := map[TaskTransition]TaskState{
		TransitionReconciliationUnresolved: TaskUnknown,
		TransitionReconciledPrepared:       TaskPrepared,
		TransitionReconciledReady:          TaskReady,
		TransitionReconciledLaunching:      TaskLaunching,
		TransitionReconciledWorking:        TaskWorking,
		TransitionReconciledDecision:       TaskAwaitingDecision,
		TransitionReconciledBlocked:        TaskBlocked,
		TransitionReconciledPaused:         TaskPaused,
		TransitionReconciledValidating:     TaskValidating,
		TransitionReconciledCandidate:      TaskCandidateComplete,
		TransitionReconciledDelivering:     TaskDelivering,
		TransitionReconciledDelivered:      TaskDelivered,
		TransitionReconciledFailed:         TaskFailed,
		TransitionReconciledCancelled:      TaskCancelled,
		TransitionReconciledCleanupHeld:    TaskCleanupHeld,
	}
	next, ok := states[transition]
	return next, ok
}

func requiredTaskState(current, required, next TaskState) (TaskState, bool) {
	if current != required {
		return "", false
	}
	return next, true
}

func oneOfTaskStates(current, next TaskState, allowed ...TaskState) (TaskState, bool) {
	for _, state := range allowed {
		if current == state {
			return next, true
		}
	}
	return "", false
}
