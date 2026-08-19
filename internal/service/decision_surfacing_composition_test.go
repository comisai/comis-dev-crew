package service

import (
	"context"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/comisai/comis-dev-crew/internal/application"
	"github.com/comisai/comis-dev-crew/internal/comiswire"
	"github.com/comisai/comis-dev-crew/internal/domain"
	"github.com/comisai/comis-dev-crew/internal/store/sqlite"
)

// surfacingControl accepts every report and separates the two lanes that carry
// an attention report: the durable outbox forwarding the worker's own decision,
// and the cadence raising it again.
type surfacingControl struct {
	mu          sync.Mutex
	resurfaced  chan comiswire.ReportRequestParams
	forwardOnce sync.Once
}

func (control *surfacingControl) Connected() bool { return true }

func (control *surfacingControl) Run(ctx context.Context) error {
	<-ctx.Done()
	return ctx.Err()
}

func (control *surfacingControl) Report(
	_ context.Context,
	request comiswire.ReportRequestParams,
) (comiswire.ReportResponseResult, error) {
	if strings.HasPrefix(string(request.ServiceReportID), "service-attention-") {
		control.mu.Lock()
		channel := control.resurfaced
		control.mu.Unlock()
		select {
		case channel <- request:
		default:
		}
	}
	return comiswire.ReportResponseResult{
		AcceptedSequence: 1, ManagedRunID: request.ManagedRunID, ServiceReportID: request.ServiceReportID,
		RetainedUntilMs: serviceForwarderClock().Add(24 * time.Hour).UnixMilli(),
	}, nil
}

func (control *surfacingControl) PutEvidence(
	_ context.Context,
	request comiswire.PutEvidenceRequestParams,
) (comiswire.PutEvidenceResponseResult, error) {
	retainedUntil := serviceForwarderClock().Add(24 * time.Hour).UnixMilli()
	return comiswire.PutEvidenceResponseResult{
		ManagedRunID: request.ManagedRunID, EvidenceRef: request.EvidenceRef,
		ContentHash: request.ContentHash, VerificationLevel: request.VerificationLevel,
		RetainedUntilMs: &retainedUntil,
	}, nil
}

func (control *surfacingControl) Heartbeat(
	_ context.Context,
	params comiswire.HeartbeatRequestParams,
) (comiswire.HeartbeatResponseResult, error) {
	return comiswire.HeartbeatResponseResult{ManagedRunID: params.ManagedRunID}, nil
}

func (control *surfacingControl) ReceiveAttentionResponse(
	_ context.Context,
	request comiswire.ReceiveAttentionResponseRequestParams,
) (comiswire.ReceiveAttentionResponseResponseResult, error) {
	return comiswire.ReceiveAttentionResponseResponseResult{
		ManagedRunID: request.ManagedRunID, ExternalKey: request.ExternalKey,
		State: comiswire.ManagedRunStatePending,
	}, nil
}

func (control *surfacingControl) ReleaseManagedRun(
	_ context.Context,
	request application.ManagedRunReleaseRequest,
) (application.ManagedRunReleaseReceipt, error) {
	return application.ManagedRunReleaseReceipt{
		ManagedRunID: request.ManagedRunID, WorkspaceLeaseID: request.WorkspaceLeaseID,
		Disposition: request.Disposition, ReleasedAt: request.ReleasedAt, State: application.ManagedRunReleased,
	}, nil
}

// advancingSurfacingClock moves forward on every read. The cadence is measured
// against wall-clock time, so a frozen clock would make no interval ever elapse
// and prove nothing about whether the loop is wired.
func advancingSurfacingClock() application.Clock {
	var mu sync.Mutex
	elapsed := time.Duration(0)
	return func() time.Time {
		mu.Lock()
		defer mu.Unlock()
		elapsed += 50 * time.Millisecond
		return serviceForwarderClock().Add(elapsed)
	}
}

func seedOpenDecision(t *testing.T, databasePath string) {
	t.Helper()
	store, err := sqlite.Open(context.Background(), databasePath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
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
	report := domain.AuthenticatedReport{TaskHandle: task.Handle, Report: domain.WorkerReport{
		SchemaVersion: 1, LocalReportID: "service-report-decision-0001",
		BriefRevision: task.BriefRevision, BriefRevisionHash: task.BriefRevisionHash,
		Kind: domain.ReportDecision, ExternalKey: "schema-choice",
		Summary: "which migration order applies", Details: "the orders differ in their backfill window",
	}}
	if _, err := sink.AcceptReport(context.Background(), report); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
}

// An unanswered question keeps coming back. Without a production caller the
// cadence is inert: the policy and the loop both exist and are tested, and no
// open decision is ever raised a second time, so a question nobody answered
// stops existing while the work it blocks waits.
func TestRun_KeepsAnUnansweredQuestionComingBack(t *testing.T) {
	root := shortTempDir(t)
	databasePath := filepath.Join(root, "state", "devcrew.db")
	seedOpenDecision(t, databasePath)
	control := &surfacingControl{resurfaced: make(chan comiswire.ReportRequestParams, 4)}
	ready := make(chan struct{})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- Run(ctx, Config{
			DatabasePath: databasePath, SocketPath: filepath.Join(root, "run", "devcrew.sock"),
			ComisControl: control, Clock: advancingSurfacingClock(), Ready: func() { close(ready) },
			DecisionSurfacing: application.DecisionSurfacingPolicy{
				Initial: 20 * time.Millisecond, Maximum: 40 * time.Millisecond,
			},
		})
	}()
	select {
	case <-ready:
	case err := <-done:
		t.Fatalf("Run() before ready error = %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("Run() did not advertise ready")
	}

	// Two repeats rather than one: the second can only be reached after the
	// first was durably recorded, so it proves the loop keeps going instead of
	// raising once and stopping.
	raised := make([]comiswire.ReportRequestParams, 2)
	for index := range raised {
		select {
		case raised[index] = <-control.resurfaced:
		case err := <-done:
			t.Fatalf("Run() before re-surfacing %d error = %v", index+1, err)
		case <-time.After(10 * time.Second):
			t.Fatalf("open decision was not raised again %d time(s)", index+1)
		}
	}
	if raised[0].Kind != comiswire.ReportKindAttention {
		t.Errorf("kind = %q, want %q", raised[0].Kind, comiswire.ReportKindAttention)
	}
	if raised[0].ExternalKey == nil || *raised[0].ExternalKey != "schema-choice" {
		t.Errorf("external key = %v, want the open decision key", raised[0].ExternalKey)
	}
	if raised[0].Summary != "which migration order applies" {
		t.Errorf("summary = %q, want the original question", raised[0].Summary)
	}
	if raised[0].OperationID == raised[1].OperationID {
		t.Errorf("the repeat reused a recorded identity: %q", raised[1].OperationID)
	}

	cancel()
	if err := <-done; err != nil {
		t.Fatalf("Run() cancellation error = %v", err)
	}

	reopened, err := sqlite.Open(context.Background(), databasePath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	open, err := reopened.OpenDecisionsAwaitingHuman(context.Background())
	if err != nil {
		t.Fatalf("OpenDecisionsAwaitingHuman() error = %v", err)
	}
	if len(open) != 1 || open[0].SurfaceCount < 2 {
		t.Fatalf("durable surfacing ledger = %+v, want the repeat recorded", open)
	}
}

// The cadence is a deployment posture, so a deployment that configures none gets
// the reviewed default rather than a service that silently never repeats.
func TestRun_FallsBackToTheReviewedSurfacingCadence(t *testing.T) {
	root := shortTempDir(t)
	control := &surfacingControl{resurfaced: make(chan comiswire.ReportRequestParams, 1)}
	ready := make(chan struct{})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- Run(ctx, Config{
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
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("Run() cancellation error = %v", err)
	}
}

// An incoherent cadence is a composition failure, not something to round into
// the default: a deployment that asked for a specific rate and got a different
// one would never learn its configuration was ignored.
func TestRun_RefusesAnIncoherentSurfacingCadence(t *testing.T) {
	root := shortTempDir(t)
	// Bounded so a cadence that is accepted instead of refused fails the
	// assertion rather than leaving the service running until the test binary
	// times out with no verdict.
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	err := Run(ctx, Config{
		DatabasePath: filepath.Join(root, "state", "devcrew.db"),
		SocketPath:   filepath.Join(root, "run", "devcrew.sock"),
		ComisControl: &surfacingControl{resurfaced: make(chan comiswire.ReportRequestParams, 1)},
		Clock:        serviceForwarderClock,
		DecisionSurfacing: application.DecisionSurfacingPolicy{
			Initial: time.Hour, Maximum: time.Minute,
		},
	})
	if err == nil {
		t.Fatal("Run(incoherent cadence) error = nil")
	}
	if !strings.Contains(err.Error(), "decision surfacing") {
		t.Fatalf("Run() error = %v, want it to name the surfacing cadence", err)
	}
}
