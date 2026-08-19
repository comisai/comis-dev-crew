package service

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/comisai/comis-dev-crew/internal/application"
)

// decisionSurfacingRaiser puts one open decision back in front of the liaison.
// It is the same generic attention path a decision took the first time; the
// supervisor decides only when, never how.
type decisionSurfacingRaiser interface {
	RaiseOpenDecision(context.Context, application.OpenDecision) error
}

type decisionSurfacingReader interface {
	DueDecisions(context.Context) ([]application.OpenDecision, error)
	RecordSurfaced(context.Context, application.OpenDecision) error
}

type decisionSurfacingSupervisorConfig struct {
	Surfacer     decisionSurfacingReader
	Raiser       decisionSurfacingRaiser
	PollInterval time.Duration
}

// decisionSurfacingSupervisor re-raises open decisions on their bounded cadence.
type decisionSurfacingSupervisor struct {
	config decisionSurfacingSupervisorConfig
}

func newDecisionSurfacingSupervisor(
	config decisionSurfacingSupervisorConfig,
) (*decisionSurfacingSupervisor, error) {
	if config.Surfacer == nil || config.Raiser == nil {
		return nil, errors.New("create decision surfacing supervisor: surfacer and raiser are required")
	}
	if config.PollInterval <= 0 || config.PollInterval > time.Hour {
		return nil, errors.New("create decision surfacing supervisor: poll interval is invalid")
	}
	return &decisionSurfacingSupervisor{config: config}, nil
}

// run raises every due decision, then waits for the next tick.
//
// The tick only decides how often the ledger is consulted; the cadence itself
// belongs to each decision, so a shorter interval here inspects more often
// without asking anyone anything more often.
func (supervisor *decisionSurfacingSupervisor) run(ctx context.Context) error {
	if supervisor == nil || ctx == nil {
		return errors.New("run decision surfacing supervisor: supervisor and context are required")
	}
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := supervisor.raiseDueDecisions(ctx); err != nil {
			return err
		}
		timer := time.NewTimer(supervisor.config.PollInterval)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			return ctx.Err()
		case <-timer.C:
		}
	}
}

// raiseDueDecisions raises each due decision and records it only once the
// raising succeeded.
//
// Recording after the raising is what makes a crash repeat a question rather
// than lose one: a decision recorded first and then never actually raised would
// sit silent for a full interval while the work it blocks waits.
func (supervisor *decisionSurfacingSupervisor) raiseDueDecisions(ctx context.Context) error {
	due, err := supervisor.config.Surfacer.DueDecisions(ctx)
	if err != nil {
		return fmt.Errorf("run decision surfacing supervisor: read due decisions: %w", err)
	}
	for _, decision := range due {
		if err := supervisor.config.Raiser.RaiseOpenDecision(ctx, decision); err != nil {
			return fmt.Errorf("run decision surfacing supervisor: raise decision: %w", err)
		}
		if err := supervisor.config.Surfacer.RecordSurfaced(ctx, decision); err != nil {
			return fmt.Errorf("run decision surfacing supervisor: record surfaced decision: %w", err)
		}
	}
	return nil
}
