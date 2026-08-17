package comiswire_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/comisai/comis-dev-crew/internal/comiswire"
	"github.com/comisai/comis-dev-crew/internal/domain"
)

type stubLivenessTasks struct {
	mu    sync.Mutex
	tasks []domain.Task
	err   error
	calls int
}

func (stub *stubLivenessTasks) ListTasks(context.Context) ([]domain.Task, error) {
	stub.mu.Lock()
	defer stub.mu.Unlock()
	stub.calls++
	if stub.err != nil {
		return nil, stub.err
	}
	return append([]domain.Task(nil), stub.tasks...), nil
}

type stubHeartbeatSender struct {
	mu   sync.Mutex
	sent []comiswire.HeartbeatRequestParams
	fail error
}

func (stub *stubHeartbeatSender) Heartbeat(
	_ context.Context,
	params comiswire.HeartbeatRequestParams,
) (comiswire.HeartbeatResponseResult, error) {
	stub.mu.Lock()
	defer stub.mu.Unlock()
	stub.sent = append(stub.sent, params)
	if stub.fail != nil {
		return comiswire.HeartbeatResponseResult{}, stub.fail
	}
	return comiswire.HeartbeatResponseResult{ManagedRunID: params.ManagedRunID}, nil
}

func (stub *stubHeartbeatSender) observed() []comiswire.HeartbeatRequestParams {
	stub.mu.Lock()
	defer stub.mu.Unlock()
	return append([]comiswire.HeartbeatRequestParams(nil), stub.sent...)
}

// Returned through a function so the nil is not a literal argument at the call
// site, matching how the package's own tests exercise this guard.
func nilLivenessContext() context.Context { return nil }

func livenessTask(handle string, state domain.TaskState, managedRunID string) domain.Task {
	return domain.Task{Handle: handle, State: state, ManagedRunID: managedRunID}
}

func runOneLivenessSweep(t *testing.T, tasks *stubLivenessTasks, sender *stubHeartbeatSender) {
	t.Helper()
	observed := time.Unix(1_800_000_000, 0).UTC()
	reporter, err := comiswire.NewLivenessReporter(comiswire.LivenessReporterConfig{
		Tasks:    tasks,
		Sender:   sender,
		Clock:    func() time.Time { return observed },
		Interval: time.Millisecond,
	})
	if err != nil {
		t.Fatalf("new liveness reporter: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- reporter.Run(ctx) }()
	deadline := time.After(2 * time.Second)
	for {
		if len(sender.observed()) > 0 {
			break
		}
		select {
		case err := <-done:
			cancel()
			t.Fatalf("reporter stopped before sending: %v", err)
		case <-deadline:
			cancel()
			t.Fatal("reporter sent no heartbeat within the bounded wait")
		default:
			time.Sleep(time.Millisecond)
		}
	}
	cancel()
	if err := <-done; err != nil && !errors.Is(err, context.Canceled) {
		t.Fatalf("run liveness reporter: %v", err)
	}
}

func TestLivenessReporter_WhenARunIsBoundAndUnsettled_ReportsThatItStillHoldsIt(t *testing.T) {
	tasks := &stubLivenessTasks{tasks: []domain.Task{
		livenessTask("task_a", domain.TaskWorking, "managed-run_a"),
	}}
	sender := &stubHeartbeatSender{}

	runOneLivenessSweep(t, tasks, sender)

	observed := sender.observed()
	if len(observed) == 0 || observed[0].ManagedRunID != "managed-run_a" {
		t.Fatalf("expected a beat for managed-run_a, got %+v", observed)
	}
	if observed[0].ObservedAtMs != time.Unix(1_800_000_000, 0).UTC().UnixMilli() {
		t.Errorf("beat must carry the service's own observation time, got %d", observed[0].ObservedAtMs)
	}
	if observed[0].OperationID == "" {
		t.Error("beat must carry an operation identity")
	}
}

func TestLivenessReporter_WhenARunIsSettledOrUnbound_SendsNoBeat(t *testing.T) {
	// A settled run needs no proof of life, and an unbound task has no managed
	// run to prove anything about. Beating for either would tell the host a run
	// is live that it has already closed, or name a run that does not exist.
	tasks := &stubLivenessTasks{tasks: []domain.Task{
		livenessTask("task_done", domain.TaskCleaned, "managed-run_done"),
		livenessTask("task_delivered", domain.TaskDelivered, "managed-run_delivered"),
		livenessTask("task_cancelled", domain.TaskCancelled, "managed-run_cancelled"),
		livenessTask("task_failed", domain.TaskFailed, "managed-run_failed"),
		livenessTask("task_unbound", domain.TaskPrepared, ""),
		livenessTask("task_live", domain.TaskWorking, "managed-run_live"),
	}}
	sender := &stubHeartbeatSender{}

	runOneLivenessSweep(t, tasks, sender)

	for _, params := range sender.observed() {
		if params.ManagedRunID != "managed-run_live" {
			t.Errorf("unexpected beat for %q", params.ManagedRunID)
		}
	}
}

func TestLivenessReporter_WhenTheHostRefusesABeat_KeepsSupervisingTheRest(t *testing.T) {
	// A refused beat is the host's answer about one run — terminal, not its own,
	// or an observation it already has. It is never a reason to stop proving that
	// the other runs are alive.
	tasks := &stubLivenessTasks{tasks: []domain.Task{
		livenessTask("task_a", domain.TaskWorking, "managed-run_a"),
		livenessTask("task_b", domain.TaskWorking, "managed-run_b"),
	}}
	sender := &stubHeartbeatSender{fail: errors.New("precondition_failed")}

	runOneLivenessSweep(t, tasks, sender)

	if len(sender.observed()) == 0 {
		t.Fatal("expected the reporter to keep sending after a refusal")
	}
}

func TestLivenessReporter_WhenDependenciesAreIncomplete_RefusesConstruction(t *testing.T) {
	for name, config := range map[string]comiswire.LivenessReporterConfig{
		"no tasks":    {Sender: &stubHeartbeatSender{}, Clock: time.Now, Interval: time.Second},
		"no sender":   {Tasks: &stubLivenessTasks{}, Clock: time.Now, Interval: time.Second},
		"no clock":    {Tasks: &stubLivenessTasks{}, Sender: &stubHeartbeatSender{}, Interval: time.Second},
		"no interval": {Tasks: &stubLivenessTasks{}, Sender: &stubHeartbeatSender{}, Clock: time.Now},
	} {
		if _, err := comiswire.NewLivenessReporter(config); err == nil {
			t.Errorf("%s: expected construction to be refused", name)
		}
	}
}

func TestLivenessReporter_WhenTaskStateCannotBeRead_StopsSupervising(t *testing.T) {
	// Losing the task store is different from a refused beat: the reporter no
	// longer knows which runs it is meant to be proving, so continuing would
	// mean silently beating for nothing while runs go dark.
	tasks := &stubLivenessTasks{err: errors.New("store unavailable")}
	reporter, err := comiswire.NewLivenessReporter(comiswire.LivenessReporterConfig{
		Tasks: tasks, Sender: &stubHeartbeatSender{},
		Clock: time.Now, Interval: time.Millisecond,
	})
	if err != nil {
		t.Fatalf("new liveness reporter: %v", err)
	}

	if err := reporter.Run(context.Background()); err == nil {
		t.Fatal("expected the reporter to stop when task state is unreadable")
	}
}

func TestLivenessReporter_WhenTheContextIsAbsentOrAlreadyDone_RefusesToRun(t *testing.T) {
	reporter, err := comiswire.NewLivenessReporter(comiswire.LivenessReporterConfig{
		Tasks: &stubLivenessTasks{}, Sender: &stubHeartbeatSender{},
		Clock: time.Now, Interval: time.Millisecond,
	})
	if err != nil {
		t.Fatalf("new liveness reporter: %v", err)
	}
	if err := reporter.Run(nilLivenessContext()); err == nil {
		t.Error("Run(nil) error = nil")
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if err := reporter.Run(cancelled); !errors.Is(err, context.Canceled) {
		t.Errorf("Run(cancelled) error = %v", err)
	}
}

func TestLivenessReporter_WhenShutdownRacesABeat_ReportsCancellationNotFailure(t *testing.T) {
	// Shutdown cancels the beat in flight. That is not the host refusing the run
	// and must not be reported as one, or a clean stop would look like a fault.
	tasks := &stubLivenessTasks{tasks: []domain.Task{
		livenessTask("task_a", domain.TaskWorking, "managed-run_a"),
	}}
	ctx, cancel := context.WithCancel(context.Background())
	sender := &cancellingHeartbeatSender{cancel: cancel}
	reporter, err := comiswire.NewLivenessReporter(comiswire.LivenessReporterConfig{
		Tasks: tasks, Sender: sender, Clock: time.Now, Interval: time.Millisecond,
	})
	if err != nil {
		t.Fatalf("new liveness reporter: %v", err)
	}

	if err := reporter.Run(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("Run during shutdown error = %v", err)
	}
}

type cancellingHeartbeatSender struct{ cancel context.CancelFunc }

func (sender *cancellingHeartbeatSender) Heartbeat(
	_ context.Context,
	_ comiswire.HeartbeatRequestParams,
) (comiswire.HeartbeatResponseResult, error) {
	sender.cancel()
	return comiswire.HeartbeatResponseResult{}, errors.New("connection closed")
}
