package sqlite

import (
	"context"
	"testing"
	"time"

	"github.com/comisai/comis-dev-crew/internal/application"
	"github.com/comisai/comis-dev-crew/internal/domain"
)

// The append refuses anything it could not later render honestly. An event with
// no kind or no observation time would reach a follower as a row it has to guess
// at, and the log's whole value is that it never asks anyone to guess.
func TestAppendServiceEvent_RefusesAnEventItCouldNotRender(t *testing.T) {
	store, _ := attestationFixture(t, domain.ShapeScout)
	transaction, err := store.db.Begin()
	if err != nil {
		t.Fatalf("Begin() error = %v", err)
	}
	defer func() { _ = transaction.Rollback() }()

	for name, event := range map[string]application.ServiceEvent{
		"no kind":      {OccurredAt: fixedAttestationTime()},
		"unknown kind": {OccurredAt: fixedAttestationTime(), Kind: application.ServiceEventKind("invented")},
		"no time":      {Kind: application.EventTaskStateChanged},
	} {
		t.Run(name, func(t *testing.T) {
			if err := appendServiceEvent(context.Background(), transaction, event); err == nil {
				t.Fatal("appendServiceEvent() error = nil, want a refusal")
			}
		})
	}
}

// A row the reader cannot decode is a failure, never a skipped event. Silently
// dropping it would advance the follower's cursor past something that happened.
func TestReadServiceEvents_RefusesRowsItCannotDecode(t *testing.T) {
	for name, corrupt := range map[string]string{
		"unreadable time": `UPDATE service_events SET occurred_at = 'not-a-time'`,
		"unknown kind":    `UPDATE service_events SET kind = 'invented'`,
		"unreadable seq":  `UPDATE service_events SET state_version = 'not-a-number'`,
	} {
		t.Run(name, func(t *testing.T) {
			store, _ := attestationFixture(t, domain.ShapeScout)
			if _, err := store.db.Exec(corrupt); err != nil {
				t.Fatalf("corrupt %s: %v", name, err)
			}
			if _, err := store.ReadServiceEvents(context.Background(), 0, 10); err == nil {
				t.Fatalf("ReadServiceEvents(corrupt %s) error = nil, want a refusal", name)
			}
		})
	}
}

// Creating a task and recording its event commit together, so a refused event
// leaves no task behind.
func TestCreateTask_RefusesWhenItsEventCannotBeRecorded(t *testing.T) {
	store, _ := attestationFixture(t, domain.ShapeScout)
	task := storeTask("task-eventless-0001", 1)
	task.UpdatedAt = time.Time{}

	if err := store.CreateTask(context.Background(), task); err == nil {
		t.Fatal("CreateTask(unrecordable event) error = nil")
	}
	var present int
	if err := store.db.QueryRow(
		`SELECT COUNT(*) FROM tasks WHERE handle = ?`, task.Handle,
	).Scan(&present); err != nil {
		t.Fatalf("count tasks: %v", err)
	}
	if present != 0 {
		t.Fatal("a task survived the refusal of its own event")
	}
}

// If the log itself is unavailable, the change it would have described is
// refused rather than committed unobserved. A state nobody can see the service
// enter is worse than a refused mutation the operator can retry.
func TestStateChange_RefusesWhenTheEventLogIsUnavailable(t *testing.T) {
	store, task := attestationFixture(t, domain.ShapeScout)
	if _, err := store.db.Exec(`DROP TABLE service_events`); err != nil {
		t.Fatalf("drop event log: %v", err)
	}

	if err := store.CreateTask(context.Background(), storeTask("task-unlogged-0001", 1)); err == nil {
		t.Error("CreateTask(no event log) error = nil")
	}
	at := task.UpdatedAt.Add(time.Minute)
	decision := sqliteWorkerReport(task, "report-decision-0009", domain.ReportDecision)
	decision.ExternalKey = "schema-choice"
	if _, err := store.CommitReport(context.Background(), directReportMutation(task, decision, at)); err == nil {
		t.Error("CommitReport(no event log) error = nil")
	}
	if _, err := store.ReadServiceEvents(context.Background(), 0, 10); err == nil {
		t.Error("ReadServiceEvents(no event log) error = nil")
	}
}
