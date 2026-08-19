package application

import (
	"context"
	"errors"
	"time"

	"github.com/comisai/comis-dev-crew/internal/domain"
)

// DefaultDecisionSurfacingPolicy is the cadence a deployment gets when it
// configures none.
var DefaultDecisionSurfacingPolicy = DecisionSurfacingPolicy{
	Initial: 30 * time.Minute,
	Maximum: 4 * time.Hour,
}

// DecisionSurfacingPolicy bounds how often one open decision is put back in
// front of the liaison.
//
// Bounded describes the rate, not the end. An open decision keeps coming back
// until it is resolved or cancelled, because the alternative — asking once and
// then falling silent — is how a question nobody answered stops existing as far
// as the system is concerned, while the work it blocks waits indefinitely.
//
// The interval grows so an unanswered question stops competing with fresh work,
// and it stops growing so it never becomes effectively silent.
type DecisionSurfacingPolicy struct {
	Initial time.Duration
	Maximum time.Duration
}

// Validate rejects a cadence that could never re-surface sensibly.
func (policy DecisionSurfacingPolicy) Validate() error {
	if policy.Initial <= 0 || policy.Maximum <= 0 {
		return errors.New("decision surfacing cadence requires a positive initial and maximum interval")
	}
	if policy.Maximum < policy.Initial {
		return errors.New("decision surfacing maximum interval is shorter than its initial interval")
	}
	return nil
}

// Interval is the wait owed after the given number of surfacings. It doubles
// each time and stops at the maximum.
func (policy DecisionSurfacingPolicy) Interval(surfaceCount int) time.Duration {
	if surfaceCount <= 0 {
		return 0
	}
	interval := policy.Initial
	for attempt := 1; attempt < surfaceCount; attempt++ {
		if interval >= policy.Maximum/2 {
			return policy.Maximum
		}
		interval *= 2
	}
	if interval > policy.Maximum {
		return policy.Maximum
	}
	return interval
}

// Due reports whether this decision may be surfaced again now.
func (policy DecisionSurfacingPolicy) Due(decision OpenDecision, now time.Time) bool {
	if decision.SurfaceCount <= 0 {
		return true
	}
	return !now.Before(decision.LastSurfacedAt.Add(policy.Interval(decision.SurfaceCount)))
}

// OpenDecision is one keyed decision still awaiting a human, together with the
// question itself, the run it belongs to, and what is known about how often it
// has already been raised.
//
// The question travels with the ledger entry because re-surfacing means asking
// it again, and reading the two halves separately would let them disagree: a
// decision selected as due could be raised against a run identity read a moment
// later, after cleanup released it.
type OpenDecision struct {
	TaskHandle     string
	ManagedRunID   string
	ExternalKey    string
	Summary        string
	Details        string
	SurfaceCount   int
	LastSurfacedAt time.Time
}

// DecisionSurfacedMutation durably records one surfacing.
type DecisionSurfacedMutation struct {
	TaskHandle  string
	ExternalKey string
	At          time.Time
}

// DecisionSurfacingStore reads the open decisions and records each surfacing.
type DecisionSurfacingStore interface {
	OpenDecisionsAwaitingHuman(context.Context) ([]OpenDecision, error)
	RecordDecisionSurfaced(context.Context, DecisionSurfacedMutation) error
}

// DecisionSurfacingConfig injects the ledger, the cadence and the clock.
type DecisionSurfacingConfig struct {
	Store  DecisionSurfacingStore
	Policy DecisionSurfacingPolicy
	Clock  Clock
}

// DecisionSurfacer decides which open decisions are owed another airing.
type DecisionSurfacer struct {
	store  DecisionSurfacingStore
	policy DecisionSurfacingPolicy
	clock  Clock
}

// NewDecisionSurfacer binds the ledger and validates the cadence.
func NewDecisionSurfacer(config DecisionSurfacingConfig) (*DecisionSurfacer, error) {
	if config.Store == nil || config.Clock == nil {
		return nil, errors.New("create decision surfacer: store and clock are required")
	}
	if err := config.Policy.Validate(); err != nil {
		return nil, err
	}
	return &DecisionSurfacer{store: config.Store, policy: config.Policy, clock: config.Clock}, nil
}

// DueDecisions returns the open decisions whose interval has elapsed.
//
// Filtering here rather than in the caller is what makes the cadence a property
// of the decision instead of a property of how often something happens to ask:
// running the loop twice as often does not repeat anything twice as often.
func (surfacer *DecisionSurfacer) DueDecisions(ctx context.Context) ([]OpenDecision, error) {
	if ctx == nil {
		return nil, errors.New("read due decisions: context is required")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	open, err := surfacer.store.OpenDecisionsAwaitingHuman(ctx)
	if err != nil {
		return nil, err
	}
	now := surfacer.clock()
	due := make([]OpenDecision, 0, len(open))
	for _, decision := range open {
		if surfacer.policy.Due(decision, now) {
			due = append(due, decision)
		}
	}
	return due, nil
}

// RecordSurfaced durably notes that one decision was raised again.
//
// It is recorded rather than counted in memory so a restart replays at worst
// one repeat. An in-memory count would reset every open decision to due-now on
// every boot, and a service that restarts often would wake the liaison with the
// whole backlog each time.
func (surfacer *DecisionSurfacer) RecordSurfaced(ctx context.Context, decision OpenDecision) error {
	if ctx == nil {
		return errors.New("record surfaced decision: context is required")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if domain.ValidateTaskHandle(decision.TaskHandle) != nil ||
		domain.ValidateDecisionKey(decision.ExternalKey) != nil {
		return mutationValidationFailure("surfaced decision identity is invalid")
	}
	return surfacer.store.RecordDecisionSurfaced(ctx, DecisionSurfacedMutation{
		TaskHandle: decision.TaskHandle, ExternalKey: decision.ExternalKey, At: surfacer.clock(),
	})
}
