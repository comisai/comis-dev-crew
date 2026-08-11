package validation

import (
	"context"
	"os"
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
	if running.PID < 1 || running.ProcessGroupIdentity == "" || running.StartIdentity == "" || running.ExecutableLabel != "fixture-program" {
		t.Fatalf("running record = %#v", running)
	}
	if strings.Contains(running.StartIdentity, "success") {
		t.Fatalf("process record leaked arguments: %#v", running)
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
	case <-store.running:
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
	mu      sync.Mutex
	records []ProcessRecord
	running chan struct{}
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
