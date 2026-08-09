package domain

import (
	"errors"
	"math"
	"reflect"
	"testing"
	"time"
)

func TestTaskAcceptWorkerReport_AppliesClosedE0ReportTransitions(t *testing.T) {
	tests := []struct {
		name      string
		from      TaskState
		kind      WorkerReportKind
		wantState TaskState
	}{
		{name: "launch acknowledgement", from: TaskLaunching, kind: ReportProgress, wantState: TaskWorking},
		{name: "working progress", from: TaskWorking, kind: ReportProgress, wantState: TaskWorking},
		{name: "decision", from: TaskWorking, kind: ReportDecision, wantState: TaskAwaitingDecision},
		{name: "resolution", from: TaskAwaitingDecision, kind: ReportResolution, wantState: TaskWorking},
		{name: "blocked", from: TaskWorking, kind: ReportBlocked, wantState: TaskBlocked},
		{name: "paused", from: TaskWorking, kind: ReportPaused, wantState: TaskPaused},
		{name: "candidate enters validation", from: TaskWorking, kind: ReportCandidateComplete, wantState: TaskValidating},
		{name: "failure", from: TaskWorking, kind: ReportFailed, wantState: TaskFailed},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			task := reportTransitionTask(test.from)
			report := reportForTask(task, test.kind, "report-0001")
			acceptedAt := task.UpdatedAt.Add(time.Minute)
			accepted, err := task.AcceptWorkerReport(report, acceptedAt)
			if err != nil {
				t.Fatalf("AcceptWorkerReport() error = %v", err)
			}
			if accepted.State != test.wantState || accepted.ReportCursor != task.ReportCursor+1 ||
				accepted.StateVersion != task.StateVersion+1 || accepted.UpdatedAt != acceptedAt {
				t.Fatalf("accepted task = %#v, want state %q and one atomic cursor/version step", accepted, test.wantState)
			}
			if err := accepted.Validate(); err != nil {
				t.Fatalf("accepted task validation error = %v", err)
			}
		})
	}
}

func TestTaskAcceptWorkerReport_RejectsStaleIllegalAndExhaustedReportsWithoutMutation(t *testing.T) {
	task := reportTransitionTask(TaskWorking)
	valid := reportForTask(task, ReportProgress, "report-0001")
	tests := []struct {
		name   string
		mutate func(*Task, *WorkerReport, *time.Time)
	}{
		{name: "stale brief revision", mutate: func(_ *Task, report *WorkerReport, _ *time.Time) { report.BriefRevision++ }},
		{name: "stale brief hash", mutate: func(_ *Task, report *WorkerReport, _ *time.Time) {
			report.BriefRevisionHash = validOperation().SubjectDigest
		}},
		{name: "invalid report", mutate: func(_ *Task, report *WorkerReport, _ *time.Time) { report.Summary = "unsafe\nsummary" }},
		{name: "illegal resolution", mutate: func(_ *Task, report *WorkerReport, _ *time.Time) {
			report.Kind, report.ExternalKey = ReportResolution, "decision-0001"
		}},
		{name: "non UTC acceptance", mutate: func(_ *Task, _ *WorkerReport, at *time.Time) { *at = at.In(time.FixedZone("offset", 3600)) }},
		{name: "backward acceptance", mutate: func(task *Task, _ *WorkerReport, at *time.Time) { *at = task.UpdatedAt.Add(-time.Second) }},
		{name: "state version exhausted", mutate: func(task *Task, _ *WorkerReport, _ *time.Time) { task.StateVersion = math.MaxInt64 }},
		{name: "report cursor exhausted", mutate: func(task *Task, _ *WorkerReport, _ *time.Time) { task.ReportCursor = math.MaxInt64 }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			current := task
			report := valid
			acceptedAt := task.UpdatedAt.Add(time.Minute)
			test.mutate(&current, &report, &acceptedAt)
			before := current
			got, err := current.AcceptWorkerReport(report, acceptedAt)
			if err == nil {
				t.Fatal("AcceptWorkerReport() error = nil")
			}
			if !reflect.DeepEqual(got, before) {
				t.Fatalf("rejected task changed: got %#v, want %#v", got, before)
			}
			if test.name == "illegal resolution" && !errors.Is(err, ErrInvalidTransition) {
				t.Fatalf("illegal report error = %v, want ErrInvalidTransition", err)
			}
		})
	}
}

func reportTransitionTask(state TaskState) Task {
	task := validTask(ShapeShip, DeliveryPullRequest)
	task.ManagedRunID = "managed-run-0001"
	task.WorkspaceLeaseID = "workspace-lease-0001"
	task.State = state
	task.ReportCursor = 4
	task.StateVersion = 7
	return task
}

func reportForTask(task Task, kind WorkerReportKind, localID string) WorkerReport {
	report := validWorkerReport(kind)
	report.LocalReportID = localID
	report.BriefRevision = task.BriefRevision
	report.BriefRevisionHash = task.BriefRevisionHash
	return report
}
