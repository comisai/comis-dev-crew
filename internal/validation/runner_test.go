package validation

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestRunner_RecordsRealValidationProcessAndBoundedReceipt(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable() error = %v", err)
	}
	catalog := runnerCatalog(t, executable, []string{
		"-test.run=TestValidationHelperProcess", "--", "success",
	})
	store := &recordingProcessStore{}
	runner, err := NewRunner(RunnerConfig{Catalog: catalog, Processes: store, MaxOutputBytes: 128})
	if err != nil {
		t.Fatalf("NewRunner() error = %v", err)
	}
	receipt, err := runner.Run(context.Background(), RunRequest{
		OperationID: "validate-alpha", TaskHandle: "task-alpha", ProfileID: "fixture-default", CheckID: "unit",
		Fields: TaskFields{TaskHandle: "task-alpha", WorktreePath: t.TempDir(), BaseRevision: strings.Repeat("a", 40), HeadRevision: strings.Repeat("b", 40)},
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if receipt.ExitCode != 0 || !receipt.Passed || receipt.OutputHash == "" || receipt.OutputBytes == 0 || receipt.OutputBytes > 128 ||
		receipt.TaskHandle != "task-alpha" || receipt.OperationID != "validate-alpha" || receipt.ProgramID != "fixture-program" {
		t.Fatalf("receipt = %#v", receipt)
	}
	records := store.snapshot()
	if len(records) < 3 || records[0].State != ProcessStarting || records[1].State != ProcessRunning || records[len(records)-1].State != ProcessExited {
		t.Fatalf("process records = %#v", records)
	}
	running := records[1]
	if running.PID < 1 || running.ProcessGroupIdentity == "" || running.StartIdentity == "" || running.ExecutableLabel != filepath.Base(executable) {
		t.Fatalf("running record = %#v", running)
	}
	if strings.Contains(running.StartIdentity, "success") {
		t.Fatalf("process record leaked arguments: %#v", running)
	}
}

func TestRunner_RecoveryRechecksOSIdentityAndFailsClosed(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable() error = %v", err)
	}
	catalog := runnerCatalog(t, executable, []string{"-test.run=TestValidationHelperProcess", "--", "success"})
	startedAt := time.Date(2026, time.August, 11, 17, 0, 0, 0, time.UTC)
	running := ProcessRecord{
		TaskHandle: "task-alpha", OperationID: "validate-alpha", ProgramID: "fixture-program",
		ExecutableLabel: filepath.Base(executable), PID: 4321, StartIdentity: "darwin-100-200",
		ProcessGroupIdentity: "4321", State: ProcessRunning, StartedAt: startedAt, ObservedAt: startedAt,
	}
	tests := []struct {
		name              string
		record            ProcessRecord
		observe           ProcessObservation
		observeErr        error
		executablePresent bool
		scanErr           error
		wantState         ProcessState
	}{
		{name: "matching process stays running", record: running, observe: ProcessObservation{
			PID: 4321, StartIdentity: "darwin-100-200", ProcessGroupIdentity: "4321", ExecutableLabel: filepath.Base(executable),
		}, wantState: ProcessRunning},
		{name: "reused pid becomes unknown", record: running, observe: ProcessObservation{
			PID: 4321, StartIdentity: "darwin-999-999", ProcessGroupIdentity: "4321", ExecutableLabel: filepath.Base(executable),
		}, wantState: ProcessUnknown},
		{name: "absent process becomes exited without invented code", record: running, observeErr: ErrProcessAbsent, wantState: ProcessExited},
		{name: "pre-start record becomes absent only after exact executable scan", record: ProcessRecord{
			TaskHandle: "task-alpha", OperationID: "validate-alpha", ProgramID: "fixture-program",
			ExecutableLabel: filepath.Base(executable), State: ProcessStarting, StartedAt: startedAt, ObservedAt: startedAt,
		}, wantState: ProcessAbsent},
		{name: "pre-start matching executable remains unknown", record: ProcessRecord{
			TaskHandle: "task-alpha", OperationID: "validate-alpha", ProgramID: "fixture-program",
			ExecutableLabel: filepath.Base(executable), State: ProcessStarting, StartedAt: startedAt, ObservedAt: startedAt,
		}, executablePresent: true, wantState: ProcessUnknown},
		{name: "pre-start unavailable scan remains unknown", record: ProcessRecord{
			TaskHandle: "task-alpha", OperationID: "validate-alpha", ProgramID: "fixture-program",
			ExecutableLabel: filepath.Base(executable), State: ProcessStarting, StartedAt: startedAt, ObservedAt: startedAt,
		}, scanErr: errors.New("process scan unavailable"), wantState: ProcessUnknown},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			store := &recordingProcessStore{active: []ProcessRecord{test.record}}
			runner, runErr := NewRunner(RunnerConfig{
				Catalog: catalog, Processes: store, MaxOutputBytes: 128,
				Clock: func() time.Time { return startedAt.Add(time.Minute) },
				ObserveProcess: func(context.Context, int) (ProcessObservation, error) {
					return test.observe, test.observeErr
				},
				ObserveExecutableLabel: func(context.Context, string) (bool, error) {
					return test.executablePresent, test.scanErr
				},
			})
			if runErr != nil {
				t.Fatalf("NewRunner() error = %v", runErr)
			}
			result, recoverErr := runner.Recover(context.Background())
			if recoverErr != nil {
				t.Fatalf("Recover() error = %v", recoverErr)
			}
			records := store.snapshot()
			if len(records) != 1 || records[0].State != test.wantState || records[0].ObservedAt != startedAt.Add(time.Minute) {
				t.Fatalf("recovered records = %#v", records)
			}
			if test.wantState == ProcessExited && records[0].ExitCode != nil {
				t.Fatalf("recovered exit invented a code: %#v", records[0])
			}
			if result.Observed != 1 {
				t.Fatalf("Recover() = %#v", result)
			}
		})
	}
}

func TestRunner_RecordsAbsentWhenFixedProgramCannotStart(t *testing.T) {
	executable := filepath.Join(t.TempDir(), "missing-validation-program")
	if err := os.WriteFile(executable, []byte("not an executable image\n"), 0o700); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	catalog := runnerCatalog(t, executable, []string{"ignored"})
	store := &recordingProcessStore{}
	runner, err := NewRunner(RunnerConfig{Catalog: catalog, Processes: store, MaxOutputBytes: 128})
	if err != nil {
		t.Fatalf("NewRunner() error = %v", err)
	}
	_, err = runner.Run(context.Background(), RunRequest{
		OperationID: "validate-missing", TaskHandle: "task-alpha", ProfileID: "fixture-default", CheckID: "unit",
		Fields: TaskFields{TaskHandle: "task-alpha", WorktreePath: t.TempDir(), BaseRevision: strings.Repeat("a", 40), HeadRevision: strings.Repeat("b", 40)},
	})
	if err == nil {
		t.Fatal("Run(missing program) error = nil")
	}
	records := store.snapshot()
	if len(records) != 2 || records[0].State != ProcessStarting || records[1].State != ProcessAbsent {
		t.Fatalf("process records = %#v, want starting then absent", records)
	}
}

func TestRunner_RecoveryRejectsUnavailableStoreAndObserver(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable() error = %v", err)
	}
	catalog := runnerCatalog(t, executable, []string{"-test.run=TestValidationHelperProcess", "--", "success"})
	runner, err := NewRunner(RunnerConfig{Catalog: catalog, Processes: recordOnlyProcessStore{}, MaxOutputBytes: 128})
	if err != nil {
		t.Fatalf("NewRunner() error = %v", err)
	}
	if _, err := runner.Recover(context.Background()); err == nil {
		t.Fatal("Recover(non-recovery store) error = nil")
	}
	store := &recordingProcessStore{activeErr: errors.New("store unavailable")}
	runner, err = NewRunner(RunnerConfig{Catalog: catalog, Processes: store, MaxOutputBytes: 128})
	if err != nil {
		t.Fatalf("NewRunner() error = %v", err)
	}
	if _, err := runner.Recover(context.Background()); err == nil {
		t.Fatal("Recover(store failure) error = nil")
	}
}

func TestRunner_CancellationStopsOnlyOwningValidationOperation(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable() error = %v", err)
	}
	catalog := runnerCatalog(t, executable, []string{
		"-test.run=TestValidationHelperProcess", "--", "wait",
	})
	store := &recordingProcessStore{running: make(chan struct{})}
	runningSignal := store.running
	runner, err := NewRunner(RunnerConfig{Catalog: catalog, Processes: store, MaxOutputBytes: 128})
	if err != nil {
		t.Fatalf("NewRunner() error = %v", err)
	}
	request := RunRequest{
		OperationID: "validate-alpha", TaskHandle: "task-alpha", ProfileID: "fixture-default", CheckID: "unit",
		Fields: TaskFields{TaskHandle: "task-alpha", WorktreePath: t.TempDir(), BaseRevision: strings.Repeat("a", 40), HeadRevision: strings.Repeat("b", 40)},
	}
	result := make(chan error, 1)
	go func() {
		_, runErr := runner.Run(context.Background(), request)
		result <- runErr
	}()
	select {
	case <-runningSignal:
	case <-time.After(5 * time.Second):
		t.Fatal("validation process did not reach running")
	}
	if err := runner.Stop(context.Background(), "validate-other", "task-alpha"); err == nil {
		t.Fatal("Stop(non-owner) error = nil")
	}
	if err := runner.Stop(context.Background(), request.OperationID, request.TaskHandle); err != nil {
		t.Fatalf("Stop(owner) error = %v", err)
	}
	select {
	case runErr := <-result:
		if runErr == nil {
			t.Fatal("cancelled Run() error = nil")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("cancelled validation process did not exit")
	}
	records := store.snapshot()
	if records[len(records)-1].State != ProcessExited {
		t.Fatalf("terminal process record = %#v", records[len(records)-1])
	}
}

func TestRunner_FailsClosedForInvalidConfigurationFailureAndOversizedOutput(t *testing.T) {
	if _, err := NewRunner(RunnerConfig{}); err == nil {
		t.Fatal("NewRunner(empty) error = nil")
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable() error = %v", err)
	}
	for _, scenario := range []struct {
		name       string
		helperMode string
		outputMax  int64
	}{
		{name: "program failure", helperMode: "failure", outputMax: 128},
		{name: "oversized output", helperMode: "oversized", outputMax: 8},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			catalog := runnerCatalog(t, executable, []string{"-test.run=TestValidationHelperProcess", "--", scenario.helperMode})
			runner, runErr := NewRunner(RunnerConfig{Catalog: catalog, Processes: &recordingProcessStore{}, MaxOutputBytes: scenario.outputMax})
			if runErr != nil {
				t.Fatalf("NewRunner() error = %v", runErr)
			}
			fields := TaskFields{TaskHandle: "task-alpha", WorktreePath: t.TempDir(), BaseRevision: strings.Repeat("a", 40), HeadRevision: strings.Repeat("b", 40)}
			receipt, runErr := runner.Run(context.Background(), RunRequest{
				OperationID: "validate-alpha", TaskHandle: "task-alpha", ProfileID: "fixture-default", CheckID: "unit", Fields: fields,
			})
			if runErr == nil || receipt.Passed || receipt.OutputBytes > scenario.outputMax {
				t.Fatalf("Run() = %#v, %v", receipt, runErr)
			}
			fields.TaskHandle = "task-other"
			if _, invalidErr := runner.Run(context.Background(), RunRequest{
				OperationID: "validate-other", TaskHandle: "task-alpha", ProfileID: "fixture-default", CheckID: "unit", Fields: fields,
			}); invalidErr == nil {
				t.Fatal("Run(mismatched task) error = nil")
			}
		})
	}
	if err := (*Runner)(nil).Stop(context.Background(), "validate-alpha", "task-alpha"); err == nil {
		t.Fatal("Stop(nil runner) error = nil")
	}
}

func TestValidationHelperProcess(t *testing.T) {
	separator := -1
	for index, argument := range os.Args {
		if argument == "--" {
			separator = index
			break
		}
	}
	if separator < 0 || separator+1 >= len(os.Args) {
		return
	}
	switch os.Args[separator+1] {
	case "success":
		_, _ = os.Stdout.WriteString("validated fixture output\n")
	case "wait":
		select {}
	case "oversized":
		_, _ = os.Stdout.WriteString("output larger than the configured validation bound\n")
	case "failure":
		os.Exit(17)
	default:
		os.Exit(17)
	}
}

func runnerCatalog(t *testing.T, executable string, arguments []string) *Catalog {
	t.Helper()
	templates := make([]ArgumentTemplate, 0, len(arguments))
	for _, argument := range arguments {
		templates = append(templates, ArgumentTemplate{Kind: ArgumentLiteral, Value: argument})
	}
	catalog, err := NewCatalog(CatalogConfig{
		Programs: []Program{{ID: "fixture-program", Executable: executable}},
		Profiles: []Profile{{ID: "fixture-default", EvidenceTTL: time.Minute, LocalChecks: []LocalCheck{{
			ID: "unit", ProgramID: "fixture-program", Arguments: templates, Timeout: 10 * time.Second, Required: true,
		}}}},
	})
	if err != nil {
		t.Fatalf("NewCatalog() error = %v", err)
	}
	return catalog
}

type recordingProcessStore struct {
	mu        sync.Mutex
	records   []ProcessRecord
	running   chan struct{}
	active    []ProcessRecord
	activeErr error
}

type recordOnlyProcessStore struct{}

func (recordOnlyProcessStore) Record(context.Context, ProcessRecord) error { return nil }

func (store *recordingProcessStore) ListActiveValidationProcesses(context.Context) ([]ProcessRecord, error) {
	return append([]ProcessRecord(nil), store.active...), store.activeErr
}

func (store *recordingProcessStore) Record(_ context.Context, record ProcessRecord) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	store.records = append(store.records, record)
	if record.State == ProcessRunning && store.running != nil {
		close(store.running)
		store.running = nil
	}
	return nil
}

func (store *recordingProcessStore) snapshot() []ProcessRecord {
	store.mu.Lock()
	defer store.mu.Unlock()
	return append([]ProcessRecord(nil), store.records...)
}
