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
	if err := store.Record(context.Background(), running); err != nil {
		t.Fatalf("Record(running replay) error = %v", err)
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
	if err := store.Record(context.Background(), exited); err != nil {
		t.Fatalf("Record(exited replay) error = %v", err)
	}
	active, err = store.ListActiveValidationProcesses(context.Background())
	if err != nil || len(active) != 0 {
		t.Fatalf("active processes after exit = %#v, %v", active, err)
	}
	absentStarting := starting
	absentStarting.OperationID = "validate-task-absent"
	absentStarting.ObservedAt = startedAt.Add(3 * time.Second)
	if err := store.Record(context.Background(), absentStarting); err != nil {
		t.Fatalf("Record(absent starting) error = %v", err)
	}
	absent := absentStarting
	absent.State = validation.ProcessAbsent
	absent.ObservedAt = startedAt.Add(4 * time.Second)
	if err := store.Record(context.Background(), absent); err != nil {
		t.Fatalf("Record(absent) error = %v", err)
	}
	active, err = store.ListActiveValidationProcesses(context.Background())
	if err != nil || len(active) != 0 {
		t.Fatalf("active processes after absent = %#v, %v", active, err)
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
	if err := store.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if err := store.Record(context.Background(), starting); err == nil {
		t.Fatal("Record(closed store) error = nil")
	}
	if _, err := store.ListActiveValidationProcesses(context.Background()); err == nil {
		t.Fatal("ListActiveValidationProcesses(closed store) error = nil")
	}
}

func TestValidationProcessStore_FailsClosedForUnavailableStoreTaskAndCorruptTimes(t *testing.T) {
	starting := validation.ProcessRecord{
		TaskHandle: "task-validation", OperationID: "validate-task", ProgramID: "go-test",
		ExecutableLabel: "go-test", State: validation.ProcessStarting,
		StartedAt:  time.Date(2026, time.August, 11, 16, 0, 0, 0, time.UTC),
		ObservedAt: time.Date(2026, time.August, 11, 16, 0, 0, 0, time.UTC),
	}
	if err := (*Store)(nil).Record(context.Background(), starting); err == nil {
		t.Fatal("Record(nil store) error = nil")
	}
	if _, err := (*Store)(nil).ListActiveValidationProcesses(context.Background()); err == nil {
		t.Fatal("ListActiveValidationProcesses(nil store) error = nil")
	}
	if err := (&Store{}).Record(context.Background(), starting); err == nil {
		t.Fatal("Record(empty store) error = nil")
	}
	invalid := starting
	invalid.OperationID = "bad operation"
	store, err := Open(context.Background(), filepath.Join(canonicalTempDir(t), "devcrew.db"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := store.Record(context.Background(), invalid); err == nil {
		t.Fatal("Record(invalid process) error = nil")
	}
	//lint:ignore SA1012 The boundary test proves nil is rejected before database work.
	if err := store.Record(nil, starting); err == nil {
		t.Fatal("Record(nil context) error = nil")
	}
	//lint:ignore SA1012 The boundary test proves nil is rejected before database work.
	if _, err := store.ListActiveValidationProcesses(nil); err == nil {
		t.Fatal("ListActiveValidationProcesses(nil context) error = nil")
	}
	prepared := storeTask(starting.TaskHandle, 1)
	if err := store.CreateTask(context.Background(), prepared); err != nil {
		t.Fatalf("CreateTask() error = %v", err)
	}
	if err := store.Record(context.Background(), starting); err == nil {
		t.Fatal("Record(non-validating task) error = nil")
	}
	missingTask := starting
	missingTask.OperationID = "validate-missing"
	missingTask.TaskHandle = "task-missing"
	if err := store.Record(context.Background(), missingTask); err == nil {
		t.Fatal("Record(missing task) error = nil")
	}
	if _, err := store.db.Exec(`UPDATE tasks SET state = 'validating', managed_run_id = 'managed-run-validation', workspace_lease_id = 'workspace-lease-validation' WHERE handle = ?`, starting.TaskHandle); err != nil {
		t.Fatalf("promote validation task fixture: %v", err)
	}
	if _, err := store.db.Exec(`CREATE TRIGGER reject_validation_insert BEFORE INSERT ON validation_processes BEGIN SELECT RAISE(FAIL, 'blocked'); END`); err != nil {
		t.Fatalf("create validation insert trigger: %v", err)
	}
	if err := store.Record(context.Background(), starting); err == nil {
		t.Fatal("Record(blocked insert) error = nil")
	}
	if _, err := store.db.Exec(`DROP TRIGGER reject_validation_insert`); err != nil {
		t.Fatalf("drop validation insert trigger: %v", err)
	}
	if err := store.Record(context.Background(), starting); err != nil {
		t.Fatalf("Record(starting) error = %v", err)
	}
	if _, err := store.db.Exec(`CREATE TRIGGER reject_validation_update BEFORE UPDATE ON validation_processes BEGIN SELECT RAISE(FAIL, 'blocked'); END`); err != nil {
		t.Fatalf("create validation update trigger: %v", err)
	}
	running := starting
	running.PID = 4321
	running.StartIdentity = "start-identity-4321"
	running.ProcessGroupIdentity = "4321"
	running.State = validation.ProcessRunning
	running.ObservedAt = starting.ObservedAt.Add(time.Second)
	if err := store.Record(context.Background(), running); err == nil {
		t.Fatal("Record(blocked update) error = nil")
	}
	if _, err := store.db.Exec(`DROP TRIGGER reject_validation_update`); err != nil {
		t.Fatalf("drop validation update trigger: %v", err)
	}
	if _, err := store.db.Exec(`UPDATE validation_processes SET started_at = 'invalid-time'`); err != nil {
		t.Fatalf("corrupt validation process time: %v", err)
	}
	if _, found, err := findValidationProcess(context.Background(), store.db, starting.OperationID); err == nil || found {
		t.Fatalf("findValidationProcess(corrupt time) found = %t, error = %v", found, err)
	}
	if err := store.Record(context.Background(), starting); err == nil {
		t.Fatal("Record(corrupt prior process) error = nil")
	}
	if _, err := store.ListActiveValidationProcesses(context.Background()); err == nil {
		t.Fatal("ListActiveValidationProcesses(corrupt time) error = nil")
	}
	if _, err := store.db.Exec(`UPDATE validation_processes SET started_at = ?, observed_at = 'invalid-time'`, formatTime(starting.StartedAt)); err != nil {
		t.Fatalf("corrupt validation process observation time: %v", err)
	}
	if _, err := store.ListActiveValidationProcesses(context.Background()); err == nil {
		t.Fatal("ListActiveValidationProcesses(corrupt observation time) error = nil")
	}
	if _, err := store.db.Exec(`UPDATE validation_processes SET observed_at = ?`, formatTime(starting.ObservedAt)); err != nil {
		t.Fatalf("restore validation process observation time: %v", err)
	}
	transaction, err := store.db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatalf("BeginTx() error = %v", err)
	}
	t.Cleanup(func() { _ = transaction.Rollback() })
	missing := starting
	missing.OperationID = "validate-process-missing-update"
	if err := updateValidationProcess(context.Background(), transaction, missing); err == nil {
		t.Fatal("updateValidationProcess(missing) error = nil")
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
