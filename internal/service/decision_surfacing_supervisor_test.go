package service

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/comisai/comis-dev-crew/internal/application"
)

// The supervisor runs on its own goroutine, so the spy is guarded: the test
// reads what it observed while the loop is still writing to it.
type surfacingSpy struct {
	mu        sync.Mutex
	due       [][]application.OpenDecision
	dueErr    error
	raised    []string
	raiseErr  error
	recorded  []string
	recordErr error
}

func (spy *surfacingSpy) DueDecisions(context.Context) ([]application.OpenDecision, error) {
	spy.mu.Lock()
	defer spy.mu.Unlock()
	if spy.dueErr != nil {
		return nil, spy.dueErr
	}
	if len(spy.due) == 0 {
		return nil, nil
	}
	batch := spy.due[0]
	spy.due = spy.due[1:]
	return batch, nil
}

func (spy *surfacingSpy) RecordSurfaced(_ context.Context, decision application.OpenDecision) error {
	spy.mu.Lock()
	defer spy.mu.Unlock()
	spy.recorded = append(spy.recorded, decision.TaskHandle+":"+decision.ExternalKey)
	return spy.recordErr
}

func (spy *surfacingSpy) RaiseOpenDecision(_ context.Context, decision application.OpenDecision) error {
	spy.mu.Lock()
	defer spy.mu.Unlock()
	spy.raised = append(spy.raised, decision.TaskHandle+":"+decision.ExternalKey)
	return spy.raiseErr
}

func newSurfacingSupervisor(t *testing.T, spy *surfacingSpy) *decisionSurfacingSupervisor {
	t.Helper()
	supervisor, err := newDecisionSurfacingSupervisor(decisionSurfacingSupervisorConfig{
		Surfacer: spy, Raiser: spy, PollInterval: time.Millisecond,
	})
	if err != nil {
		t.Fatalf("newDecisionSurfacingSupervisor() error = %v", err)
	}
	return supervisor
}

// Every due decision is raised and then recorded, in that order. Recording a
// decision that was never actually raised would leave it silent for a whole
// interval while the work it blocks waits for an answer nobody was asked for.
func TestDecisionSurfacingSupervisor_RaisesBeforeItRecords(t *testing.T) {
	spy := &surfacingSpy{due: [][]application.OpenDecision{{
		{TaskHandle: "task-0001", ExternalKey: "schema-choice"},
		{TaskHandle: "task-0002", ExternalKey: "rollout-window"},
	}}}
	supervisor := newSurfacingSupervisor(t, spy)

	if err := supervisor.raiseDueDecisions(context.Background()); err != nil {
		t.Fatalf("raiseDueDecisions() error = %v", err)
	}
	if len(spy.raised) != 2 || spy.raised[0] != "task-0001:schema-choice" {
		t.Fatalf("raised = %v", spy.raised)
	}
	if len(spy.recorded) != 2 || spy.recorded[1] != "task-0002:rollout-window" {
		t.Fatalf("recorded = %v", spy.recorded)
	}
}

// A raising that failed is not recorded. Recording it would consume the
// decision's whole interval on an attempt that reached nobody.
func TestDecisionSurfacingSupervisor_DoesNotRecordAFailedRaising(t *testing.T) {
	spy := &surfacingSpy{
		due:      [][]application.OpenDecision{{{TaskHandle: "task-0001", ExternalKey: "schema-choice"}}},
		raiseErr: errors.New("attention path unavailable"),
	}
	supervisor := newSurfacingSupervisor(t, spy)

	if err := supervisor.raiseDueDecisions(context.Background()); err == nil {
		t.Fatal("raiseDueDecisions(failing raiser) error = nil")
	}
	if len(spy.recorded) != 0 {
		t.Fatalf("a failed raising was recorded: %v", spy.recorded)
	}
}

func TestDecisionSurfacingSupervisor_SurfacesLedgerFailures(t *testing.T) {
	reading := newSurfacingSupervisor(t, &surfacingSpy{dueErr: errors.New("store unavailable")})
	if err := reading.raiseDueDecisions(context.Background()); err == nil {
		t.Error("raiseDueDecisions(failing read) error = nil")
	}
	recording := newSurfacingSupervisor(t, &surfacingSpy{
		due:       [][]application.OpenDecision{{{TaskHandle: "task-0001", ExternalKey: "schema-choice"}}},
		recordErr: errors.New("store unavailable"),
	})
	if err := recording.raiseDueDecisions(context.Background()); err == nil {
		t.Error("raiseDueDecisions(failing record) error = nil")
	}
}

// The loop is bounded by its context and joins on cancellation rather than
// leaving a goroutine raising decisions after the service has stopped.
func TestDecisionSurfacingSupervisor_StopsWhenItsContextIsCanceled(t *testing.T) {
	spy := &surfacingSpy{}
	supervisor := newSurfacingSupervisor(t, spy)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- supervisor.run(ctx) }()

	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("run() error = %v, want context.Canceled", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("run() did not stop when its context was canceled")
	}
}

func TestNewDecisionSurfacingSupervisor_RequiresEverySeamAndABoundedInterval(t *testing.T) {
	spy := &surfacingSpy{}
	for label, config := range map[string]decisionSurfacingSupervisorConfig{
		"no surfacer":        {Raiser: spy, PollInterval: time.Second},
		"no raiser":          {Surfacer: spy, PollInterval: time.Second},
		"no interval":        {Surfacer: spy, Raiser: spy},
		"negative interval":  {Surfacer: spy, Raiser: spy, PollInterval: -time.Second},
		"unbounded interval": {Surfacer: spy, Raiser: spy, PollInterval: 2 * time.Hour},
	} {
		if _, err := newDecisionSurfacingSupervisor(config); err == nil {
			t.Errorf("newDecisionSurfacingSupervisor(%s) error = nil", label)
		}
	}
}

func TestDecisionSurfacingSupervisor_RefusesMissingAuthority(t *testing.T) {
	supervisor := newSurfacingSupervisor(t, &surfacingSpy{})
	if err := supervisor.run(missingSupervisorContext()); err == nil {
		t.Error("run(no context) error = nil")
	}
	var absent *decisionSurfacingSupervisor
	if err := absent.run(context.Background()); err == nil {
		t.Error("run(absent supervisor) error = nil")
	}
}

func missingSupervisorContext() context.Context { return nil }

// The loop keeps running across ticks rather than raising once and exiting: an
// open decision that becomes due later must still be picked up.
func TestDecisionSurfacingSupervisor_KeepsRaisingAcrossTicks(t *testing.T) {
	spy := &surfacingSpy{due: [][]application.OpenDecision{
		{{TaskHandle: "task-0001", ExternalKey: "schema-choice"}},
		{{TaskHandle: "task-0002", ExternalKey: "rollout-window"}},
	}}
	supervisor := newSurfacingSupervisor(t, spy)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- supervisor.run(ctx) }()

	deadline := time.After(2 * time.Second)
	for {
		if spy.remainingBatches() == 0 {
			break
		}
		select {
		case <-deadline:
			t.Fatal("supervisor did not consume a second batch across ticks")
		case <-time.After(2 * time.Millisecond):
		}
	}
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("run() error = %v", err)
	}
}

func (spy *surfacingSpy) remainingBatches() int {
	spy.mu.Lock()
	defer spy.mu.Unlock()
	return len(spy.due)
}
