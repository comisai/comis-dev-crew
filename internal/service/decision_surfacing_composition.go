package service

import (
	"context"
	"fmt"
	"time"

	"github.com/comisai/comis-dev-crew/internal/application"
	"github.com/comisai/comis-dev-crew/internal/comiswire"
)

// decisionSurfacingMaxPollInterval bounds how long the service may go without
// consulting the surfacing ledger.
//
// The sweep decides only how often the ledger is read; each decision's own
// interval decides when it is raised, so a faster sweep asks nobody anything
// more often. The cap keeps a long cadence on a live loop instead of one that
// wakes as rarely as the slowest question it might find.
const decisionSurfacingMaxPollInterval = time.Minute

// composeDecisionSurfacing binds the surfacing ledger, the reviewed cadence and
// the authenticated attention path into one supervised loop.
//
// A deployment that configures no cadence gets the reviewed default; one that
// configures an incoherent cadence is refused rather than rounded into the
// default, so a deployment never runs at a rate it did not ask for.
func composeDecisionSurfacing(
	store application.DecisionSurfacingStore,
	sender comiswire.ReportSender,
	policy application.DecisionSurfacingPolicy,
	clock application.Clock,
) (func(context.Context) error, error) {
	if policy == (application.DecisionSurfacingPolicy{}) {
		policy = application.DefaultDecisionSurfacingPolicy
	}
	surfacer, err := application.NewDecisionSurfacer(application.DecisionSurfacingConfig{
		Store: store, Policy: policy, Clock: clock,
	})
	if err != nil {
		return nil, fmt.Errorf("compose decision surfacing: %w", err)
	}
	raiser, err := comiswire.NewDecisionRaiser(comiswire.DecisionRaiserConfig{Sender: sender})
	if err != nil {
		return nil, fmt.Errorf("compose decision surfacing: %w", err)
	}
	pollInterval := policy.Initial
	if pollInterval > decisionSurfacingMaxPollInterval {
		pollInterval = decisionSurfacingMaxPollInterval
	}
	supervisor, err := newDecisionSurfacingSupervisor(decisionSurfacingSupervisorConfig{
		Surfacer: surfacer, Raiser: raiser, PollInterval: pollInterval,
	})
	if err != nil {
		return nil, fmt.Errorf("compose decision surfacing: %w", err)
	}
	return supervisor.run, nil
}
