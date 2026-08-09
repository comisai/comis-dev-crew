// Package workers contains reviewed worker harness adapters. The deterministic
// fixture proves lifecycle and recovery behavior without launching a coding CLI.
package workers

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/comisai/comis-dev-crew/internal/domain"
)

var (
	// ErrInjectedFault identifies a deliberate deterministic fixture stop.
	ErrInjectedFault = errors.New("fixture worker injected fault")
	// ErrDecisionUnavailable means the fixture received no usable decision answer.
	ErrDecisionUnavailable = errors.New("fixture decision response is unavailable")
)

// FaultPoint is the closed set of deterministic fixture stop boundaries.
type FaultPoint string

const (
	FaultNone            FaultPoint = "none"
	FaultBeforeProgress  FaultPoint = "before_progress"
	FaultAfterProgress   FaultPoint = "after_progress"
	FaultAfterDecision   FaultPoint = "after_decision"
	FaultAfterResolution FaultPoint = "after_resolution"
)

func (point FaultPoint) valid() bool {
	switch point {
	case FaultNone, FaultBeforeProgress, FaultAfterProgress, FaultAfterDecision, FaultAfterResolution:
		return true
	default:
		return false
	}
}

// TaskReporter is the fixture's append-only task-scoped report capability.
type TaskReporter interface {
	Report(context.Context, domain.WorkerReport) (domain.ReportReceipt, error)
}

// DecisionSource supplies exactly one answer after the fixture emits its keyed
// decision report.
type DecisionSource interface {
	AwaitDecision(context.Context, string) (string, error)
}

// Clock supplies deterministic worker observation time.
type Clock func() time.Time

// FixtureConfig contains every dependency and deterministic identity used by a
// fixture run.
type FixtureConfig struct {
	Brief          domain.WorkerBrief
	Reporter       TaskReporter
	Decisions      DecisionSource
	Clock          Clock
	ReportIDPrefix string
	DecisionKey    string
	Fault          FaultPoint
}

// Fixture is a synchronous worker with no process, goroutine, sleep, or hidden
// state outside one Run call.
type Fixture struct {
	brief          domain.WorkerBrief
	reporter       TaskReporter
	decisions      DecisionSource
	clock          Clock
	reportIDPrefix string
	decisionKey    string
	fault          FaultPoint
}

// NewFixture validates the exact brief, closed fault point, dependencies, and
// all report identities before a run can emit anything.
func NewFixture(config FixtureConfig) (*Fixture, error) {
	if err := config.Brief.Validate(); err != nil {
		return nil, fmt.Errorf("create fixture worker: %w", err)
	}
	if config.Reporter == nil || config.Decisions == nil || config.Clock == nil {
		return nil, errors.New("create fixture worker: reporter, decisions, and clock are required")
	}
	for _, suffix := range []string{"progress", "decision", "resolution"} {
		if err := domain.ValidateLocalReportID(config.ReportIDPrefix + "-" + suffix); err != nil {
			return nil, errors.New("create fixture worker: report ID prefix is invalid")
		}
	}
	if err := domain.ValidateDecisionKey(config.DecisionKey); err != nil {
		return nil, errors.New("create fixture worker: decision key is invalid")
	}
	if !config.Fault.valid() {
		return nil, errors.New("create fixture worker: fault point is unknown")
	}
	return &Fixture{
		brief: config.Brief, reporter: config.Reporter, decisions: config.Decisions,
		clock: config.Clock, reportIDPrefix: config.ReportIDPrefix,
		decisionKey: config.DecisionKey, fault: config.Fault,
	}, nil
}

// Run emits the canonical progress/decision/resolution sequence and exits at
// the selected deterministic boundary.
func (fixture *Fixture) Run(ctx context.Context) error {
	if ctx == nil {
		return errors.New("run fixture worker: context is required")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := fixture.stopAt(FaultBeforeProgress); err != nil {
		return err
	}
	if err := fixture.emit(ctx, domain.ReportProgress, "progress", "fixture accepted the pinned worker brief", ""); err != nil {
		return err
	}
	if err := fixture.stopAt(FaultAfterProgress); err != nil {
		return err
	}
	if err := fixture.emit(ctx, domain.ReportDecision, "decision", "fixture requests one deterministic decision", fixture.decisionKey); err != nil {
		return err
	}
	if err := fixture.stopAt(FaultAfterDecision); err != nil {
		return err
	}
	answer, err := fixture.decisions.AwaitDecision(ctx, fixture.decisionKey)
	if err != nil {
		return &decisionFailure{cause: err}
	}
	if strings.TrimSpace(answer) == "" {
		return ErrDecisionUnavailable
	}
	if err := fixture.emit(ctx, domain.ReportResolution, "resolution", "fixture received one decision response", fixture.decisionKey); err != nil {
		return err
	}
	return fixture.stopAt(FaultAfterResolution)
}

func (fixture *Fixture) emit(ctx context.Context, kind domain.WorkerReportKind, suffix, summary, externalKey string) error {
	observedAt := fixture.clock()
	report := domain.WorkerReport{
		SchemaVersion: 1, LocalReportID: fixture.reportIDPrefix + "-" + suffix,
		BriefRevision: fixture.brief.Revision, BriefRevisionHash: fixture.brief.RevisionHash,
		Kind: kind, ExternalKey: externalKey, Summary: summary, WorkerObservedAt: &observedAt,
	}
	if _, err := fixture.reporter.Report(ctx, report); err != nil {
		return fmt.Errorf("fixture report rejected: %w", err)
	}
	return nil
}

func (fixture *Fixture) stopAt(point FaultPoint) error {
	if fixture.fault != point {
		return nil
	}
	return &faultError{point: point}
}

type faultError struct{ point FaultPoint }

func (failure *faultError) Error() string {
	return fmt.Sprintf("%s: %s", ErrInjectedFault, failure.point)
}
func (failure *faultError) Unwrap() error { return ErrInjectedFault }

type decisionFailure struct{ cause error }

func (failure *decisionFailure) Error() string { return "fixture decision source failed" }
func (failure *decisionFailure) Unwrap() error { return failure.cause }
