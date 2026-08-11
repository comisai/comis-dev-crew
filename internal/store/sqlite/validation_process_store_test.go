package sqlite

import (
	"context"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/comisai/comis-dev-crew/internal/domain"
	"github.com/comisai/comis-dev-crew/internal/validation"
)

func TestValidationProcessStore_PersistsLifecycleAcrossRestart(t *testing.T) {
	path := filepath.Join(canonicalTempDir(t), "devcrew.db")
	store, err := Open(context.Background(), path)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	task := validationProcessTask(t, "task-validation")
	if err := store.CreateTask(context.Background(), task); err != nil {
		t.Fatalf("CreateTask() error = %v", err)
	}
	startedAt := task.UpdatedAt.Add(time.Minute)
	starting := validation.ProcessRecord{
		TaskHandle: task.Handle, OperationID: "validate-task", ProgramID: "go-test",
		ExecutableLabel: "go-test", State: validation.ProcessStarting,
		StartedAt: startedAt, ObservedAt: startedAt,
	}
	if err := store.Record(context.Background(), starting); err != nil {
		t.Fatalf("Record(starting) error = %v", err)
	}
	running := starting
	running.PID = 4321
	running.StartIdentity = "start-identity-4321"
	running.ProcessGroupIdentity = "4321"
	running.State = validation.ProcessRunning
	running.ObservedAt = startedAt.Add(time.Second)
	if err := store.Record(context.Background(), running); err != nil {
		t.Fatalf("Record(running) error = %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	store, err = Open(context.Background(), path)
	if err != nil {
		t.Fatalf("Open(restart) error = %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	active, err := store.ListActiveValidationProcesses(context.Background())
	if err != nil {
		t.Fatalf("ListActiveValidationProcesses() error = %v", err)
	}
	if !reflect.DeepEqual(active, []validation.ProcessRecord{running}) {
		t.Fatalf("active processes = %#v, want %#v", active, []validation.ProcessRecord{running})
	}
	exitCode := 0
	exited := running
	exited.State = validation.ProcessExited
	exited.ObservedAt = startedAt.Add(2 * time.Second)
	exited.ExitCode = &exitCode
	if err := store.Record(context.Background(), exited); err != nil {
		t.Fatalf("Record(exited) error = %v", err)
	}
	active, err = store.ListActiveValidationProcesses(context.Background())
	if err != nil || len(active) != 0 {
		t.Fatalf("active processes after exit = %#v, %v", active, err)
	}
}

func TestValidationProcessStore_RejectsForgedAndRegressiveLifecycle(t *testing.T) {
	store, err := Open(context.Background(), filepath.Join(canonicalTempDir(t), "devcrew.db"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	task := validationProcessTask(t, "task-validation")
	if err := store.CreateTask(context.Background(), task); err != nil {
		t.Fatalf("CreateTask() error = %v", err)
	}
	startedAt := task.UpdatedAt.Add(time.Minute)
	starting := validation.ProcessRecord{
		TaskHandle: task.Handle, OperationID: "validate-task", ProgramID: "go-test",
		ExecutableLabel: "go-test", State: validation.ProcessStarting,
		StartedAt: startedAt, ObservedAt: startedAt,
	}
	invalidInitial := starting
	invalidInitial.State = validation.ProcessRunning
	invalidInitial.PID = 4321
	invalidInitial.StartIdentity = "start-identity-4321"
	invalidInitial.ProcessGroupIdentity = "4321"
	if err := store.Record(context.Background(), invalidInitial); err == nil {
		t.Fatal("Record(initial running) error = nil")
	}
	if err := store.Record(context.Background(), starting); err != nil {
		t.Fatalf("Record(starting) error = %v", err)
	}
	for _, test := range []struct {
		name   string
		mutate func(*validation.ProcessRecord)
	}{
		{name: "changed task", mutate: func(record *validation.ProcessRecord) { record.TaskHandle = "task-other" }},
		{name: "changed program", mutate: func(record *validation.ProcessRecord) {
			record.ProgramID = "node-test"
			record.ExecutableLabel = "node-test"
		}},
		{name: "missing start identity", mutate: func(record *validation.ProcessRecord) { record.StartIdentity = "" }},
		{name: "regressive observation", mutate: func(record *validation.ProcessRecord) { record.ObservedAt = startedAt.Add(-time.Second) }},
		{name: "premature exit", mutate: func(record *validation.ProcessRecord) { record.State = validation.ProcessExited }},
	} {
		t.Run(test.name, func(t *testing.T) {
			record := invalidInitial
			record.ObservedAt = startedAt.Add(time.Second)
			test.mutate(&record)
			if err := store.Record(context.Background(), record); err == nil {
				t.Fatal("Record(forged transition) error = nil")
			}
		})
	}
	if _, err := store.db.Exec(`UPDATE validation_processes SET state = 'forged'`); err != nil {
		t.Fatalf("corrupt validation process: %v", err)
	}
	if _, err := store.ListActiveValidationProcesses(context.Background()); err == nil {
		t.Fatal("ListActiveValidationProcesses(corrupt) error = nil")
	}
}

func validationProcessTask(t *testing.T, handle string) domain.Task {
	t.Helper()
	task := storeTask(handle, 1)
	task.State = domain.TaskValidating
	task.ManagedRunID = "managed-run-validation"
	task.WorkspaceLeaseID = "workspace-lease-validation"
	if err := task.Validate(); err != nil {
		t.Fatalf("validation task is invalid: %v", err)
	}
	return task
}
