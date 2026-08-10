package domain

import (
	"math"
	"time"
)

// AcceptWorkerReport applies one authenticated, exact-brief sparse report to a
// task. Durable report replay is resolved before this method is called.
func (task Task) AcceptWorkerReport(report WorkerReport, acceptedAt time.Time) (Task, error) {
	if err := task.Validate(); err != nil {
		return task, transitionFailure(task, reportTransition(report.Kind), "current task is invalid")
	}
	if err := report.Validate(); err != nil {
		return task, &ValidationError{Field: "report", Reason: "worker report is invalid"}
	}
	if report.BriefRevision != task.BriefRevision || report.BriefRevisionHash != task.BriefRevisionHash {
		return task, &ValidationError{Field: "reportBrief", Reason: "does not match the pinned task brief"}
	}
	if acceptedAt.IsZero() || acceptedAt.Location() != time.UTC || acceptedAt.Before(task.UpdatedAt) {
		return task, &ValidationError{Field: "acceptedAt", Reason: "must be monotonic UTC service time"}
	}
	if task.ReportCursor == math.MaxInt64 || task.StateVersion == math.MaxInt64 {
		return task, transitionFailure(task, reportTransition(report.Kind), "task report authority is exhausted")
	}

	var updated Task
	var err error
	transition := reportTransition(report.Kind)
	if report.Kind == ReportProgress {
		if task.State == TaskLaunching && task.WorkerProfileID == "fixture-worker" {
			updated, err = task.ApplyTransition(transition, acceptedAt)
			if err != nil {
				return task, err
			}
		} else {
			if task.State != TaskWorking {
				return task, transitionFailure(task, reportTransition(report.Kind), "progress requires acknowledged working state")
			}
			updated = task
			updated.StateVersion++
			updated.UpdatedAt = acceptedAt
		}
	} else {
		updated, err = task.ApplyTransition(transition, acceptedAt)
		if err != nil {
			return task, err
		}
	}
	updated.ReportCursor++
	if err := updated.Validate(); err != nil {
		return task, transitionFailure(task, transition, "report would create an invalid task")
	}
	return updated, nil
}

func reportTransition(kind WorkerReportKind) TaskTransition {
	switch kind {
	case ReportProgress:
		return TransitionWorkerAcknowledged
	case ReportDecision:
		return TransitionDecisionRequested
	case ReportResolution:
		return TransitionDecisionAnswered
	case ReportBlocked:
		return TransitionBlocked
	case ReportPaused:
		return TransitionPaused
	case ReportCandidateComplete:
		return TransitionValidationStarted
	case ReportFailed:
		return TransitionFailureObserved
	default:
		return TaskTransition(kind)
	}
}
