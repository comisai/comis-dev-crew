package sqlite

import (
	"context"
	"testing"
	"time"

	"github.com/comisai/comis-dev-crew/internal/application"
	"github.com/comisai/comis-dev-crew/internal/domain"
)

// The three sources answer different questions and are never blended: what the
// worker said, what the service did, and what validation ran. Merging them would
// make an operator unable to tell a worker's claim from a service fact, which is
// the distinction the whole precedence model rests on.
func TestReadTaskLogs_SeparatesWorkerServiceAndValidationSources(t *testing.T) {
	store, task := attestationFixture(t, domain.ShapeScout)
	at := task.UpdatedAt.Add(time.Minute)
	reportDecision(t, store, task, "schema-choice", at)

	worker, err := store.ReadTaskLogs(context.Background(), task.Handle, application.LogSourceWorker, 0, 50)
	if err != nil {
		t.Fatalf("ReadTaskLogs(worker) error = %v", err)
	}
	if len(worker) != 1 {
		t.Fatalf("worker entries = %#v", worker)
	}
	if worker[0].Source != application.LogSourceWorker || worker[0].Label != string(domain.ReportDecision) {
		t.Errorf("worker entry = %#v", worker[0])
	}
	if worker[0].Detail != "which migration order applies" {
		t.Errorf("worker detail = %q, want the report summary", worker[0].Detail)
	}

	service, err := store.ReadTaskLogs(context.Background(), task.Handle, application.LogSourceService, 0, 50)
	if err != nil {
		t.Fatalf("ReadTaskLogs(service) error = %v", err)
	}
	if len(service) == 0 {
		t.Fatal("service source carried no entries")
	}
	for _, entry := range service {
		if entry.Source != application.LogSourceService {
			t.Errorf("service entry = %#v", entry)
		}
	}

	validation, err := store.ReadTaskLogs(context.Background(), task.Handle, application.LogSourceValidation, 0, 50)
	if err != nil {
		t.Fatalf("ReadTaskLogs(validation) error = %v", err)
	}
	if len(validation) != 0 {
		t.Fatalf("a task that ran no validation reported %#v", validation)
	}
}

// Every source exposes the same monotonic cursor, so --follow works identically
// whichever one an operator is watching.
func TestReadTaskLogs_ResumeFromACursorOnEverySource(t *testing.T) {
	store, task := attestationFixture(t, domain.ShapeScout)
	at := task.UpdatedAt.Add(time.Minute)
	reportDecision(t, store, task, "schema-choice", at)

	for _, source := range []application.TaskLogSource{
		application.LogSourceWorker, application.LogSourceService,
	} {
		entries, err := store.ReadTaskLogs(context.Background(), task.Handle, source, 0, 50)
		if err != nil {
			t.Fatalf("ReadTaskLogs(%s) error = %v", source, err)
		}
		if len(entries) == 0 {
			t.Fatalf("%s carried no entries", source)
		}
		for index := 1; index < len(entries); index++ {
			if entries[index].Sequence <= entries[index-1].Sequence {
				t.Fatalf("%s sequences are not increasing: %#v", source, entries)
			}
		}
		drained, err := store.ReadTaskLogs(
			context.Background(), task.Handle, source, entries[len(entries)-1].Sequence, 50,
		)
		if err != nil {
			t.Fatalf("ReadTaskLogs(%s drained) error = %v", source, err)
		}
		if len(drained) != 0 {
			t.Fatalf("%s drained cursor returned %#v", source, drained)
		}
	}
}

// A task's private detail belongs to that task alone: naming one task must never
// return another's worker text.
func TestReadTaskLogs_ScopeToTheNamedTask(t *testing.T) {
	store, task := attestationFixture(t, domain.ShapeScout)
	at := task.UpdatedAt.Add(time.Minute)
	reportDecision(t, store, task, "schema-choice", at)

	other, err := store.ReadTaskLogs(context.Background(), "task-elsewhere-0001", application.LogSourceWorker, 0, 50)
	if err != nil {
		t.Fatalf("ReadTaskLogs(other task) error = %v", err)
	}
	if len(other) != 0 {
		t.Fatalf("another task's logs leaked: %#v", other)
	}
}

// An unreadable page is a failure, never an empty one, and an unknown source is
// refused rather than silently answered from some default.
func TestReadTaskLogs_RefuseInvalidRequestsAndUnavailableReads(t *testing.T) {
	store, task := attestationFixture(t, domain.ShapeScout)
	canceled, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := store.ReadTaskLogs(canceled, task.Handle, application.LogSourceWorker, 0, 10); err == nil {
		t.Error("ReadTaskLogs(canceled) error = nil")
	}
	if _, err := store.ReadTaskLogs(missingStoreContext(), task.Handle, application.LogSourceWorker, 0, 10); err == nil {
		t.Error("ReadTaskLogs(no context) error = nil")
	}
	if _, err := store.ReadTaskLogs(context.Background(), task.Handle, application.TaskLogSource("terminal"), 0, 10); err == nil {
		t.Error("ReadTaskLogs(unknown source) error = nil")
	}
	if _, err := store.ReadTaskLogs(context.Background(), task.Handle, application.LogSourceWorker, -1, 10); err == nil {
		t.Error("ReadTaskLogs(negative cursor) error = nil")
	}
	if _, err := store.ReadTaskLogs(context.Background(), task.Handle, application.LogSourceWorker, 0, 0); err == nil {
		t.Error("ReadTaskLogs(zero page) error = nil")
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	for _, source := range []application.TaskLogSource{
		application.LogSourceWorker, application.LogSourceService, application.LogSourceValidation,
	} {
		if _, err := store.ReadTaskLogs(context.Background(), task.Handle, source, 0, 10); err == nil {
			t.Errorf("ReadTaskLogs(%s, closed store) error = nil", source)
		}
	}
}
