//go:build integration

package integration_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/comisai/comis-dev-crew/internal/application"
	"github.com/comisai/comis-dev-crew/internal/domain"
	"github.com/comisai/comis-dev-crew/internal/reporter"
	"github.com/comisai/comis-dev-crew/internal/service"
	"github.com/comisai/comis-dev-crew/internal/store/sqlite"
	"github.com/comisai/comis-dev-crew/internal/workers"
)

func TestDurableWorkflow_RestartMatrixConvergesWithoutDuplicateOrFalseSuccess(t *testing.T) {
	tests := []struct {
		boundary  matrixBoundary
		disruptor matrixDisruptor
		when      matrixTiming
	}{
		{boundaryPrepare, disruptService, beforeBoundary},
		{boundaryPrepare, disruptService, afterBoundary},
		{boundaryPrepare, disruptRequester, beforeBoundary},
		{boundaryPrepare, disruptRequester, afterBoundary},
		{boundaryBind, disruptService, beforeBoundary},
		{boundaryBind, disruptService, afterBoundary},
		{boundaryBind, disruptRequester, beforeBoundary},
		{boundaryBind, disruptRequester, afterBoundary},
		{boundaryReport, disruptService, beforeBoundary},
		{boundaryReport, disruptService, afterBoundary},
		{boundaryReport, disruptRequester, beforeBoundary},
		{boundaryReport, disruptRequester, afterBoundary},
	}
	for _, test := range tests {
		name := fmt.Sprintf("%s_%s_%s", test.boundary, test.disruptor, test.when)
		t.Run(name, func(t *testing.T) {
			harness := newRestartHarness(t)
			harness.seedPrerequisites(t, test.boundary)

			if test.when == beforeBoundary {
				switch test.disruptor {
				case disruptService:
					harness.restartService(t)
					if test.boundary == boundaryReport {
						harness.assertReportRefusedAfterAmbiguousLaunch(t)
						return
					}
				case disruptRequester:
					cancelled, cancel := context.WithCancel(context.Background())
					cancel()
					if _, err := harness.perform(t, cancelled, test.boundary); !errors.Is(err, context.Canceled) {
						t.Fatalf("cancelled %s error = %v, want context.Canceled", test.boundary, err)
					}
					harness.assertBoundaryAbsent(t, test.boundary)
				}
			}

			requestContext := context.Background()
			var cancelRequester context.CancelFunc
			if test.disruptor == disruptRequester && test.when == afterBoundary {
				requestContext, cancelRequester = context.WithCancel(context.Background())
			}
			original, err := harness.perform(t, requestContext, test.boundary)
			if err != nil {
				t.Fatalf("initial %s error = %v", test.boundary, err)
			}
			if cancelRequester != nil {
				cancelRequester() // The durable result exists, but the requesting client discards it.
			}
			if test.disruptor == disruptService && test.when == afterBoundary {
				harness.restartService(t)
			}

			replay, err := harness.perform(t, context.Background(), test.boundary)
			if err != nil || !reflect.DeepEqual(replay, original) {
				t.Fatalf("%s replay = %#v, %v, want original %#v", test.boundary, replay, err, original)
			}
			harness.assertAlteredReplayRejected(t, test.boundary)
			harness.assertConverged(t, test.boundary, test.disruptor == disruptService && test.when == afterBoundary)
		})
	}
}

func TestDurableWorkflow_DeterministicFixtureRunsOnceAndRestartFailsClosed(t *testing.T) {
	t.Run("full report sequence", func(t *testing.T) {
		harness := newRestartHarness(t)
		harness.seedPrerequisites(t, boundaryReport)
		fixture, decisions := harness.fixture(t, workers.FaultNone)
		if err := fixture.Run(context.Background()); err != nil {
			t.Fatalf("fixture Run() error = %v", err)
		}
		harness.assertFixtureEvidence(t, 4, domain.TaskValidating, 7)
		if decisions.calls != 1 || decisions.key != matrixDecisionKey {
			t.Fatalf("decision calls/key = %d/%q, want one %q", decisions.calls, decisions.key, matrixDecisionKey)
		}
	})

	t.Run("ambiguous runtime is not relaunched", func(t *testing.T) {
		harness := newRestartHarness(t)
		harness.seedPrerequisites(t, boundaryReport)
		fixture, _ := harness.fixture(t, workers.FaultAfterProgress)
		runs := 0
		runs++
		if err := fixture.Run(context.Background()); !errors.Is(err, workers.ErrInjectedFault) {
			t.Fatalf("fixture Run(fault) error = %v, want ErrInjectedFault", err)
		}
		harness.restartService(t)
		replay, err := harness.mutations.StartTask(context.Background(), matrixStartCommand())
		if err != nil || replay.Task.State != domain.TaskUnknown {
			t.Fatalf("StartTask(reconciled replay) = %#v, %v, want unknown without launch", replay, err)
		}
		if runs != 1 {
			t.Fatalf("fixture runs = %d, want exactly one", runs)
		}
		harness.assertFixtureEvidence(t, 1, domain.TaskUnknown, 5)
	})
}

type matrixBoundary string

const (
	boundaryPrepare matrixBoundary = "prepare"
	boundaryBind    matrixBoundary = "bind"
	boundaryReport  matrixBoundary = "report"
)

type matrixDisruptor string

const (
	disruptService   matrixDisruptor = "service"
	disruptRequester matrixDisruptor = "requester"
)

type matrixTiming string

const (
	beforeBoundary matrixTiming = "before"
	afterBoundary  matrixTiming = "after"
)

const (
	matrixTaskHandle  = "task-restart-0001"
	matrixCredential  = "restart-fixture-credential-00000001"
	matrixDecisionKey = "decision-restart-0001"
)

type restartHarness struct {
	t          *testing.T
	root       string
	database   string
	socket     string
	store      *sqlite.Store
	mutations  *application.Mutations
	now        time.Time
	taskIDs    *matrixTaskIDs
	taskHandle string
}

func newRestartHarness(t *testing.T) *restartHarness {
	t.Helper()
	root := integrationShortTempDir(t)
	harness := &restartHarness{
		t: t, root: root, database: filepath.Join(root, "state", "devcrew.db"),
		socket: filepath.Join(root, "run", "devcrew.sock"),
		now:    time.Date(2026, time.August, 9, 18, 0, 0, 0, time.UTC),
		taskIDs: &matrixTaskIDs{
			remaining: []string{matrixTaskHandle},
		},
	}
	harness.open(t)
	t.Cleanup(func() {
		if harness.store != nil {
			_ = harness.store.Close()
		}
	})
	return harness
}

func (harness *restartHarness) open(t *testing.T) {
	t.Helper()
	store, err := sqlite.Open(context.Background(), harness.database)
	if err != nil {
		t.Fatalf("open restart store: %v", err)
	}
	mutations, err := application.NewMutations(application.MutationConfig{
		Store: store, Repositories: matrixRepositoryCatalog{},
		WorkerProfiles: func(string, domain.TaskShape) error { return nil }, ValidationProfiles: func(string) error { return nil },
		Workspaces:         parityWorkspacePreparer{root: filepath.Join(harness.root, "fixture-workspace")},
		RuntimeAttachments: integrationRuntimeAttachments{},
		TaskIDs:            harness.taskIDs.next,
		RegistrationNonces: func() (string, error) { return "registration-nonce_matrix", nil },
		PreparationTTL:     time.Hour, Clock: func() time.Time { return harness.now },
	})
	if err != nil {
		_ = store.Close()
		t.Fatalf("compose restart mutations: %v", err)
	}
	harness.store, harness.mutations = store, mutations
}

func (harness *restartHarness) close(t *testing.T) {
	t.Helper()
	if harness.store == nil {
		return
	}
	if err := harness.store.Close(); err != nil {
		t.Fatalf("close restart store: %v", err)
	}
	harness.store, harness.mutations = nil, nil
}

func (harness *restartHarness) restartService(t *testing.T) {
	t.Helper()
	harness.close(t)
	harness.advance()
	ctx, cancel := context.WithCancel(context.Background())
	ready := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- service.Run(ctx, service.Config{
			DatabasePath: harness.database, SocketPath: harness.socket,
			Clock: func() time.Time { return harness.now }, Ready: func() { close(ready) },
		})
	}()
	select {
	case <-ready:
	case err := <-done:
		cancel()
		t.Fatalf("devcrew-service stopped before readiness: %v", err)
	case <-time.After(5 * time.Second):
		cancel()
		<-done
		t.Fatal("devcrew-service readiness timed out")
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("devcrew-service cancellation error = %v", err)
	}
	if _, err := os.Lstat(harness.socket); !os.IsNotExist(err) {
		t.Fatalf("devcrew-service socket remained after cancellation: %v", err)
	}
	harness.open(t)
}

func (harness *restartHarness) advance() { harness.now = harness.now.Add(time.Minute) }

func (harness *restartHarness) seedPrerequisites(t *testing.T, boundary matrixBoundary) {
	t.Helper()
	if boundary == boundaryPrepare {
		return
	}
	prepared, err := harness.mutations.PrepareTask(context.Background(), matrixPrepareCommand())
	if err != nil {
		t.Fatalf("seed PrepareTask() error = %v", err)
	}
	harness.taskHandle = prepared.Task.Handle
	if boundary == boundaryBind {
		return
	}
	harness.advance()
	if _, err := harness.mutations.ActivateManagedRun(context.Background(), matrixBindCommand()); err != nil {
		t.Fatalf("seed ActivateManagedRun() error = %v", err)
	}
	harness.advance()
	if _, err := harness.mutations.StartTask(context.Background(), matrixStartCommand()); err != nil {
		t.Fatalf("seed StartTask() error = %v", err)
	}
}

type matrixOutcome struct {
	Mutation *application.MutationResult
	Receipt  *domain.ReportReceipt
}

func (harness *restartHarness) perform(t *testing.T, ctx context.Context, boundary matrixBoundary) (matrixOutcome, error) {
	t.Helper()
	switch boundary {
	case boundaryPrepare:
		result, err := harness.mutations.PrepareTask(ctx, matrixPrepareCommand())
		if err == nil {
			harness.taskHandle = result.Task.Handle
		}
		return matrixOutcome{Mutation: &result}, err
	case boundaryBind:
		result, err := harness.mutations.ActivateManagedRun(ctx, matrixBindCommand())
		return matrixOutcome{Mutation: &result}, err
	case boundaryReport:
		receipt, err := harness.reportClient(t).Report(ctx, matrixProgressReport(harness.task(t)))
		return matrixOutcome{Receipt: &receipt}, err
	default:
		t.Fatalf("unknown matrix boundary %q", boundary)
		return matrixOutcome{}, nil
	}
}

func (harness *restartHarness) reportClient(t *testing.T) *reporter.Client {
	t.Helper()
	task := harness.task(t)
	sink, err := application.NewReportSink(application.ReportSinkConfig{
		Store: harness.store, Clock: func() time.Time { return harness.now },
	})
	if err != nil {
		t.Fatalf("compose report sink: %v", err)
	}
	endpoint, err := reporter.NewEndpoint(reporter.EndpointConfig{
		TaskHandle: task.Handle, BriefRevision: task.BriefRevision, BriefRevisionHash: task.BriefRevisionHash,
		Credential: matrixCredential, Sink: sink,
	})
	if err != nil {
		t.Fatalf("compose reporter endpoint: %v", err)
	}
	client, err := reporter.NewClient(endpoint, matrixCredential)
	if err != nil {
		t.Fatalf("compose reporter client: %v", err)
	}
	return client
}

func (harness *restartHarness) fixture(t *testing.T, fault workers.FaultPoint) (*workers.Fixture, *matrixDecisions) {
	t.Helper()
	task := harness.task(t)
	brief, err := task.RenderWorkerBrief()
	if err != nil {
		t.Fatalf("RenderWorkerBrief() error = %v", err)
	}
	decisions := &matrixDecisions{answer: "use the bounded fixture choice"}
	fixture, err := workers.NewFixture(workers.FixtureConfig{
		Brief: brief, Reporter: harness.reportClient(t), Decisions: decisions,
		Clock: func() time.Time { return harness.now }, ReportIDPrefix: "restart-fixture",
		DecisionKey: matrixDecisionKey, Fault: fault,
	})
	if err != nil {
		t.Fatalf("NewFixture() error = %v", err)
	}
	return fixture, decisions
}

func (harness *restartHarness) task(t *testing.T) domain.Task {
	t.Helper()
	task, err := harness.store.GetTask(context.Background(), matrixTaskHandle)
	if err != nil {
		t.Fatalf("GetTask() error = %v", err)
	}
	return task
}

func (harness *restartHarness) assertBoundaryAbsent(t *testing.T, boundary matrixBoundary) {
	t.Helper()
	tasks, err := harness.store.ListTasks(context.Background())
	if err != nil {
		t.Fatalf("ListTasks() error = %v", err)
	}
	if boundary == boundaryPrepare {
		if len(tasks) != 0 {
			t.Fatalf("tasks after cancelled prepare = %d, want zero", len(tasks))
		}
		return
	}
	if len(tasks) != 1 {
		t.Fatalf("tasks after cancelled %s = %d, want one prerequisite", boundary, len(tasks))
	}
	wantState := domain.TaskPrepared
	if boundary == boundaryReport {
		wantState = domain.TaskLaunching
		reports, err := harness.store.ListAcceptedReports(context.Background(), matrixTaskHandle)
		if err != nil || len(reports) != 0 {
			t.Fatalf("reports after cancelled report = %d, %v, want zero", len(reports), err)
		}
	}
	if tasks[0].State != wantState {
		t.Fatalf("task after cancelled %s = %q, want %q", boundary, tasks[0].State, wantState)
	}
}

func (harness *restartHarness) assertReportRefusedAfterAmbiguousLaunch(t *testing.T) {
	t.Helper()
	if _, err := harness.perform(t, context.Background(), boundaryReport); !errors.Is(err, domain.ErrInvalidTransition) {
		t.Fatalf("report after ambiguous launch error = %v, want ErrInvalidTransition", err)
	}
	reports, err := harness.store.ListAcceptedReports(context.Background(), matrixTaskHandle)
	if err != nil || len(reports) != 0 {
		t.Fatalf("reports after ambiguous launch = %d, %v, want zero", len(reports), err)
	}
	if task := harness.task(t); task.State != domain.TaskUnknown || task.ReportCursor != 0 {
		t.Fatalf("ambiguous task = %#v, want unknown with no accepted report", task)
	}
	if version, err := harness.store.CurrentStateVersion(context.Background()); err != nil || version != 4 {
		t.Fatalf("ambiguous state version = %d, %v, want 4", version, err)
	}
}

func (harness *restartHarness) assertAlteredReplayRejected(t *testing.T, boundary matrixBoundary) {
	t.Helper()
	var err error
	switch boundary {
	case boundaryPrepare:
		altered := matrixPrepareCommand()
		altered.AcceptanceCriteria = []string{"Altered acceptance must not replace the task."}
		_, err = harness.mutations.PrepareTask(context.Background(), altered)
	case boundaryBind:
		altered := matrixBindCommand()
		altered.WorkspaceLeaseID = "workspace-lease-altered"
		_, err = harness.mutations.ActivateManagedRun(context.Background(), altered)
	case boundaryReport:
		altered := matrixProgressReport(harness.task(t))
		altered.Summary = "altered report must not replace durable evidence"
		_, err = harness.reportClient(t).Report(context.Background(), altered)
	}
	if !errors.Is(err, application.ErrConflict) {
		t.Fatalf("altered %s replay error = %v, want ErrConflict", boundary, err)
	}
}

func (harness *restartHarness) assertConverged(t *testing.T, boundary matrixBoundary, restartedAfter bool) {
	t.Helper()
	tasks, err := harness.store.ListTasks(context.Background())
	if err != nil || len(tasks) != 1 || tasks[0].Handle != matrixTaskHandle {
		t.Fatalf("converged tasks = %#v, %v, want one exact task", tasks, err)
	}
	wantState, wantVersion, wantReports := domain.TaskPrepared, int64(1), 0
	switch boundary {
	case boundaryBind:
		wantState, wantVersion = domain.TaskReady, 2
	case boundaryReport:
		wantState, wantVersion, wantReports = domain.TaskWorking, 4, 1
		if restartedAfter {
			wantState, wantVersion = domain.TaskUnknown, 5
		}
	}
	if tasks[0].State != wantState || tasks[0].State == domain.TaskDelivered {
		t.Fatalf("converged task state = %q, want %q without false success", tasks[0].State, wantState)
	}
	reports, err := harness.store.ListAcceptedReports(context.Background(), matrixTaskHandle)
	if err != nil || len(reports) != wantReports {
		t.Fatalf("converged reports = %d, %v, want %d", len(reports), err, wantReports)
	}
	if version, err := harness.store.CurrentStateVersion(context.Background()); err != nil || version != wantVersion {
		t.Fatalf("converged state version = %d, %v, want %d", version, err, wantVersion)
	}
}

func (harness *restartHarness) assertFixtureEvidence(t *testing.T, wantReports int, wantState domain.TaskState, wantVersion int64) {
	t.Helper()
	task := harness.task(t)
	if task.State != wantState || task.ReportCursor != int64(wantReports) || task.State == domain.TaskDelivered {
		t.Fatalf("fixture task = %#v, want %q with %d reports and no success", task, wantState, wantReports)
	}
	reports, err := harness.store.ListAcceptedReports(context.Background(), matrixTaskHandle)
	if err != nil || len(reports) != wantReports {
		t.Fatalf("fixture reports = %d, %v, want %d", len(reports), err, wantReports)
	}
	if version, err := harness.store.CurrentStateVersion(context.Background()); err != nil || version != wantVersion {
		t.Fatalf("fixture state version = %d, %v, want %d", version, err, wantVersion)
	}
	for _, operationID := range []string{"op-prepare-restart", "op-bind-restart", "op-start-restart"} {
		operation, err := harness.store.GetOperation(context.Background(), operationID)
		if err != nil || operation.Status != domain.OperationCompleted {
			t.Fatalf("operation %q = %#v, %v, want completed", operationID, operation, err)
		}
	}
}

type matrixRepositoryCatalog struct{}

func (matrixRepositoryCatalog) ValidateRepository(context.Context, string) error { return nil }

type matrixTaskIDs struct {
	mu        sync.Mutex
	remaining []string
}

func (source *matrixTaskIDs) next(string) (string, error) {
	source.mu.Lock()
	defer source.mu.Unlock()
	if len(source.remaining) == 0 {
		return "", errors.New("restart matrix task identities exhausted")
	}
	next := source.remaining[0]
	source.remaining = source.remaining[1:]
	return next, nil
}

type matrixDecisions struct {
	answer string
	key    string
	calls  int
}

func (decisions *matrixDecisions) AwaitDecision(_ context.Context, key string) (string, error) {
	decisions.calls++
	decisions.key = key
	return decisions.answer, nil
}

func matrixPrepareCommand() application.PrepareTaskCommand {
	return application.PrepareTaskCommand{
		OperationID: "op-prepare-restart", ServiceInstanceID: "service-instance-restart",
		Shape: domain.ShapeShip, RepositoryID: "product-api", BaseRevision: strings.Repeat("a", 40),
		AcceptanceCriteria: []string{"The restart matrix converges without duplicate work."},
		Constraints:        []string{"Never infer successful completion from a lost process."},
		ValidationProfile:  "go-default", DeliveryMode: domain.DeliveryPullRequest,
		WorkerProfileID: "fixture-worker",
	}
}

func matrixBindCommand() application.ActivateManagedRunCommand {
	return application.ActivateManagedRunCommand{
		OperationID: "op-bind-restart", ServiceInstanceID: "service-instance-restart",
		ExternalRunRef: matrixTaskHandle, RegistrationNonce: "registration-nonce_matrix",
		ManagedRunID: "managed-run-restart", WorkspaceLeaseID: "workspace-lease-restart",
		ExecutionAttachmentID: "execution-attachment-restart",
		AttachmentTargetName:  "attachment-cccccccccccccccccccccccccccccccc.sock",
	}
}

func matrixStartCommand() application.StartTaskCommand {
	return application.StartTaskCommand{OperationID: "op-start-restart", TaskHandle: matrixTaskHandle}
}

func matrixProgressReport(task domain.Task) domain.WorkerReport {
	observed := time.Date(2026, time.August, 9, 18, 0, 0, 0, time.UTC)
	return domain.WorkerReport{
		SchemaVersion: 1, LocalReportID: "restart-progress", BriefRevision: task.BriefRevision,
		BriefRevisionHash: task.BriefRevisionHash, Kind: domain.ReportProgress,
		Summary: "restart fixture accepted the pinned brief", WorkerObservedAt: &observed,
	}
}
