package service

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/comisai/comis-dev-crew/internal/application"
	"github.com/comisai/comis-dev-crew/internal/comiswire"
	"github.com/comisai/comis-dev-crew/internal/domain"
	"github.com/comisai/comis-dev-crew/internal/localapi"
	"github.com/comisai/comis-dev-crew/internal/store/sqlite"
)

func TestRun_ComposesCanonicalMutationOnDedicatedMCPEndpoint(t *testing.T) {
	root := shortTempDir(t)
	databasePath := filepath.Join(root, "state", "devcrew.db")
	operatorSocket := filepath.Join(root, "run", "operator.sock")
	mcpSocket := filepath.Join(root, "run", "mcp.sock")
	ready := make(chan struct{})
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	done := make(chan error, 1)
	go func() {
		done <- Run(ctx, Config{
			DatabasePath: databasePath, SocketPath: operatorSocket, MCPSocketPath: mcpSocket,
			ServiceInstanceID: "service-instance_a", Repositories: serviceRepositoryCatalog{},
			TaskIDs:            func() (string, error) { return "task-service-prepare", nil },
			RegistrationNonces: func() (string, error) { return "registration-nonce_service", nil },
			PreparationTTL:     time.Hour, Ready: func() { close(ready) },
		})
	}()
	select {
	case <-ready:
	case err := <-done:
		t.Fatalf("Run() before ready error = %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("Run() did not advertise ready")
	}
	client, err := localapi.NewClient(mcpSocket, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	result, err := client.PrepareTask(context.Background(), "operation-service-prepare", localapi.PrepareTaskInput{
		Shape: domain.ShapeScout, RepositoryID: "product-api",
		BaseRevision: strings.Repeat("a", 40), AcceptanceCriteria: []string{"Return one report."},
		Constraints: []string{"Do not deliver."}, ValidationProfile: "go-default",
		DeliveryMode: domain.DeliveryReport, WorkerProfileID: "fixture-worker",
	})
	if err != nil {
		t.Fatalf("PrepareTask() error = %v", err)
	}
	if result.TaskHandle != "task-service-prepare" || result.ManagedRun.RegistrationNonce != "registration-nonce_service" {
		t.Fatalf("PrepareTask() = %#v", result)
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if _, err := os.Lstat(operatorSocket); !os.IsNotExist(err) {
		t.Fatalf("operator socket remains: %v", err)
	}
	if _, err := os.Lstat(mcpSocket); !os.IsNotExist(err) {
		t.Fatalf("MCP socket remains: %v", err)
	}
}

type serviceRepositoryCatalog struct{}

func (serviceRepositoryCatalog) ValidateRepository(context.Context, string) error { return nil }

var _ application.RepositoryCatalog = serviceRepositoryCatalog{}

func TestRun_ServesPersistedQueriesAndRestartsCleanly(t *testing.T) {
	root := shortTempDir(t)
	databasePath := filepath.Join(root, "state", "devcrew.db")
	socketPath := filepath.Join(root, "run", "devcrew.sock")
	seed, err := sqlite.Open(context.Background(), databasePath)
	if err != nil {
		t.Fatalf("open seed store: %v", err)
	}
	task := serviceTask()
	if err := seed.CreateTask(context.Background(), task); err != nil {
		t.Fatalf("seed task: %v", err)
	}
	if err := seed.Close(); err != nil {
		t.Fatalf("close seed store: %v", err)
	}

	for iteration := 1; iteration <= 2; iteration++ {
		ready := make(chan struct{})
		ctx, cancel := context.WithCancel(context.Background())
		t.Cleanup(cancel)
		done := make(chan error, 1)
		go func() {
			done <- Run(ctx, Config{
				DatabasePath: databasePath,
				SocketPath:   socketPath,
				Ready:        func() { close(ready) },
			})
		}()
		select {
		case <-ready:
		case err := <-done:
			t.Fatalf("iteration %d Run() before ready error = %v", iteration, err)
		case <-time.After(5 * time.Second):
			t.Fatalf("iteration %d Run() did not advertise ready", iteration)
		}

		client, err := localapi.NewClient(socketPath, time.Second)
		if err != nil {
			t.Fatalf("iteration %d NewClient() error = %v", iteration, err)
		}
		detail, err := client.ShowTask(context.Background(), "read-0001", task.Handle)
		if err != nil {
			t.Fatalf("iteration %d ShowTask() error = %v", iteration, err)
		}
		if detail.Summary.TaskHandle != task.Handle || detail.StateVersion != task.StateVersion {
			t.Fatalf("iteration %d detail = %#v, want persisted task", iteration, detail)
		}

		cancel()
		if err := <-done; err != nil {
			t.Fatalf("iteration %d Run() error = %v", iteration, err)
		}
		if _, err := os.Lstat(socketPath); !os.IsNotExist(err) {
			t.Fatalf("iteration %d socket still exists: %v", iteration, err)
		}
	}
}

func TestRun_RejectsMissingContextAndConfiguration(t *testing.T) {
	valid := Config{
		DatabasePath: filepath.Join(shortTempDir(t), "state", "devcrew.db"),
		SocketPath:   filepath.Join(shortTempDir(t), "run", "devcrew.sock"),
	}
	//lint:ignore SA1012 The boundary test proves service composition rejects nil context.
	if err := Run(nil, valid); err == nil {
		t.Fatal("Run(nil context) error = nil")
	}
	if err := Run(context.Background(), Config{}); err == nil {
		t.Fatal("Run(empty config) error = nil")
	}
	if err := Run(context.Background(), Config{DatabasePath: "relative.db", SocketPath: valid.SocketPath}); err == nil {
		t.Fatal("Run(relative database) error = nil")
	}
	if err := Run(context.Background(), Config{DatabasePath: valid.DatabasePath, SocketPath: "relative.sock"}); err == nil {
		t.Fatal("Run(relative socket) error = nil")
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if err := Run(cancelled, valid); !errors.Is(err, context.Canceled) {
		t.Fatalf("Run(cancelled) error = %v, want context.Canceled", err)
	}
}

func TestRun_ReconcilesAmbiguousStateBeforeAdvertisingReady(t *testing.T) {
	root := shortTempDir(t)
	databasePath := filepath.Join(root, "state", "devcrew.db")
	socketPath := filepath.Join(root, "run", "devcrew.sock")
	seed, err := sqlite.Open(context.Background(), databasePath)
	if err != nil {
		t.Fatalf("open seed store: %v", err)
	}
	task := serviceTask()
	task.ManagedRunID = "managed-run-0001"
	task.WorkspaceLeaseID = "workspace-lease-0001"
	task.State = domain.TaskWorking
	if err := seed.CreateTask(context.Background(), task); err != nil {
		t.Fatalf("seed working task: %v", err)
	}
	accepted := domain.OperationRecord{
		SchemaVersion: 1, ID: "op-accepted-0001", Command: "StartTask",
		SubjectDigest: strings.Repeat("b", 64), Status: domain.OperationAccepted,
		StateVersion: 2, CreatedAt: task.CreatedAt, UpdatedAt: task.UpdatedAt,
	}
	if err := seed.RecordOperation(context.Background(), accepted); err != nil {
		t.Fatalf("seed accepted operation: %v", err)
	}
	if err := seed.Close(); err != nil {
		t.Fatalf("close seed store: %v", err)
	}

	ready := make(chan struct{})
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	done := make(chan error, 1)
	go func() {
		done <- Run(ctx, Config{
			DatabasePath: databasePath, SocketPath: socketPath,
			Clock: time.Now, Ready: func() { close(ready) },
		})
	}()
	select {
	case <-ready:
	case err := <-done:
		t.Fatalf("Run() before ready error = %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("Run() did not advertise ready")
	}
	client, err := localapi.NewClient(socketPath, time.Second)
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	detail, err := client.ShowTask(context.Background(), "read-reconciled-task", task.Handle)
	if err != nil || detail.Summary.State != domain.TaskUnknown {
		t.Fatalf("ShowTask() = %#v, %v, want unknown before ready", detail, err)
	}
	operation, err := client.Operation(context.Background(), "read-reconciled-operation", accepted.ID)
	if err != nil || operation.Status != domain.OperationUnknown {
		t.Fatalf("Operation() = %#v, %v, want unknown before ready", operation, err)
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("Run() error = %v", err)
	}
}

type serviceComisControl struct {
	mu          sync.Mutex
	runCalls    int
	reportCalls int
	reports     chan comiswire.ReportRequestParams
	failRun     chan error
}

func (control *serviceComisControl) Run(ctx context.Context) error {
	control.mu.Lock()
	control.runCalls++
	control.mu.Unlock()
	if control.failRun != nil {
		select {
		case err := <-control.failRun:
			return err
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	<-ctx.Done()
	return ctx.Err()
}

func (control *serviceComisControl) Report(ctx context.Context, request comiswire.ReportRequestParams) (comiswire.ReportResponseResult, error) {
	control.mu.Lock()
	control.reportCalls++
	call := control.reportCalls
	control.mu.Unlock()
	control.reports <- request
	if call == 1 {
		return comiswire.ReportResponseResult{}, errors.New("uncertain first report")
	}
	if call == 3 {
		<-ctx.Done()
		return comiswire.ReportResponseResult{}, ctx.Err()
	}
	return comiswire.ReportResponseResult{
		AcceptedSequence: int64(call), ManagedRunID: request.ManagedRunID,
		ServiceReportID: request.ServiceReportID,
		RetainedUntilMs: time.Date(2026, time.August, 11, 0, 0, 0, 0, time.UTC).UnixMilli(),
	}, nil
}

func TestRun_OwnsOneControlConnectionAndDurableReportForwarder(t *testing.T) {
	root := shortTempDir(t)
	databasePath := filepath.Join(root, "state", "devcrew.db")
	socketPath := filepath.Join(root, "run", "devcrew.sock")
	seedServiceReports(t, databasePath)
	control := &serviceComisControl{reports: make(chan comiswire.ReportRequestParams, 3)}
	ready := make(chan struct{})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- Run(ctx, Config{
			DatabasePath: databasePath, SocketPath: socketPath, ComisControl: control,
			Clock: serviceForwarderClock, Ready: func() { close(ready) },
		})
	}()
	select {
	case <-ready:
	case err := <-done:
		t.Fatalf("Run() before ready error = %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("Run() did not advertise ready")
	}
	requests := make([]comiswire.ReportRequestParams, 3)
	for index := range requests {
		select {
		case requests[index] = <-control.reports:
		case err := <-done:
			t.Fatalf("Run() before report %d error = %v", index+1, err)
		case <-time.After(5 * time.Second):
			t.Fatalf("report %d was not forwarded", index+1)
		}
	}
	if !reflect.DeepEqual(requests[0], requests[1]) {
		t.Fatalf("uncertain retry = %#v / %#v, want exact request", requests[0], requests[1])
	}
	if requests[2].ServiceReportID == requests[0].ServiceReportID || requests[2].OperationID == requests[0].OperationID {
		t.Fatalf("next durable report reused prior identity: %#v / %#v", requests[0], requests[2])
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("Run() cancellation error = %v", err)
	}
	control.mu.Lock()
	runCalls := control.runCalls
	control.mu.Unlock()
	if runCalls != 1 {
		t.Fatalf("control Run() calls = %d, want one", runCalls)
	}

	reopened, err := sqlite.Open(context.Background(), databasePath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	pending, found, err := reopened.NextComisReport(context.Background())
	if err != nil || !found || pending.LocalReportID != "service-report-progress-0002" {
		t.Fatalf("pending after joined cancellation = %#v, %t, %v", pending, found, err)
	}
}

func TestRun_ControlFailureCancelsAndJoinsLocalEndpoint(t *testing.T) {
	root := shortTempDir(t)
	socketPath := filepath.Join(root, "run", "devcrew.sock")
	controlFailure := errors.New("control supervisor failed")
	control := &serviceComisControl{
		reports: make(chan comiswire.ReportRequestParams, 1), failRun: make(chan error, 1),
	}
	ready := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- Run(context.Background(), Config{
			DatabasePath: filepath.Join(root, "state", "devcrew.db"), SocketPath: socketPath,
			ComisControl: control, Clock: serviceForwarderClock, Ready: func() { close(ready) },
		})
	}()
	select {
	case <-ready:
	case err := <-done:
		t.Fatalf("Run() before ready error = %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("Run() did not advertise ready")
	}
	control.failRun <- controlFailure
	if err := <-done; !errors.Is(err, controlFailure) {
		t.Fatalf("Run(control failure) error = %v", err)
	}
	if _, err := os.Lstat(socketPath); !os.IsNotExist(err) {
		t.Fatalf("operator socket remains after control failure: %v", err)
	}
}

func TestRun_UnexpectedCleanControlStopFailsClosed(t *testing.T) {
	root := shortTempDir(t)
	control := &serviceComisControl{
		reports: make(chan comiswire.ReportRequestParams, 1), failRun: make(chan error, 1),
	}
	ready := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- Run(context.Background(), Config{
			DatabasePath: filepath.Join(root, "state", "devcrew.db"),
			SocketPath:   filepath.Join(root, "run", "devcrew.sock"),
			ComisControl: control, Clock: serviceForwarderClock, Ready: func() { close(ready) },
		})
	}()
	select {
	case <-ready:
	case err := <-done:
		t.Fatalf("Run() before ready error = %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("Run() did not advertise ready")
	}
	control.failRun <- nil
	if err := <-done; err == nil {
		t.Fatal("Run(clean control stop) error = nil")
	}
}

func seedServiceReports(t *testing.T, databasePath string) {
	t.Helper()
	store, err := sqlite.Open(context.Background(), databasePath)
	if err != nil {
		t.Fatal(err)
	}
	task := serviceTask()
	task.State = domain.TaskWorking
	task.ManagedRunID = "managed-run-service-0001"
	task.WorkspaceLeaseID = "workspace-lease-service-0001"
	if err := store.CreateTask(context.Background(), task); err != nil {
		t.Fatal(err)
	}
	sink, err := application.NewReportSink(application.ReportSinkConfig{Store: store, Clock: serviceForwarderClock})
	if err != nil {
		t.Fatal(err)
	}
	for index := 1; index <= 2; index++ {
		report := domain.AuthenticatedReport{TaskHandle: task.Handle, Report: domain.WorkerReport{
			SchemaVersion: 1, LocalReportID: "service-report-progress-000" + string(rune('0'+index)),
			BriefRevision: task.BriefRevision, BriefRevisionHash: task.BriefRevisionHash,
			Kind: domain.ReportProgress, Summary: "Deterministic service progress.",
		}}
		if _, err := sink.AcceptReport(context.Background(), report); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
}

func serviceForwarderClock() time.Time {
	return time.Date(2026, time.August, 10, 0, 0, 0, 0, time.UTC)
}

func serviceTask() domain.Task {
	created := time.Date(2026, time.August, 8, 20, 0, 0, 0, time.UTC)
	task := domain.Task{
		SchemaVersion:      1,
		Handle:             "task-0001",
		ServiceInstanceID:  "service-instance-0001",
		State:              domain.TaskPrepared,
		Shape:              domain.ShapeShip,
		RepositoryID:       "product-api",
		BaseRevision:       strings.Repeat("a", 40),
		BriefRevision:      1,
		AcceptanceCriteria: []string{"The requested change is proven."},
		Constraints:        []string{"Preserve unrelated changes."},
		ValidationProfile:  "go-default",
		DeliveryMode:       domain.DeliveryPullRequest,
		WorkerProfileID:    "codex-standard",
		StateVersion:       1,
		CreatedAt:          created,
		UpdatedAt:          created,
	}
	pinned, err := task.PinBriefRevision()
	if err != nil {
		panic(err)
	}
	return pinned
}

func shortTempDir(t *testing.T) string {
	t.Helper()
	directory, err := os.MkdirTemp("/tmp", "dcs-")
	if err != nil {
		t.Fatalf("create short temporary directory: %v", err)
	}
	t.Cleanup(func() {
		if err := os.RemoveAll(directory); err != nil {
			t.Errorf("remove short temporary directory: %v", err)
		}
	})
	resolved, err := filepath.EvalSymlinks(directory)
	if err != nil {
		t.Fatalf("resolve short temporary directory: %v", err)
	}
	return resolved
}
