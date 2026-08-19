package application

import (
	"context"

	"github.com/comisai/comis-dev-crew/internal/domain"
)

// ListDecisions inventories the open decisions, fleet-wide or for one task.
//
// The return schedule is derived here rather than in each adapter so the CLI can
// never publish a cadence the supervisor does not run.
func (queries *Queries) ListDecisions(ctx context.Context, taskHandle string) (DecisionList, error) {
	if taskHandle != "" {
		if err := domain.ValidateTaskHandle(taskHandle); err != nil {
			return DecisionList{}, invalidReferenceFailure("task reference", err)
		}
	}
	decisions, stateVersion, err := queries.readDecisions(ctx, taskHandle)
	if err != nil {
		return DecisionList{}, err
	}
	if decisions == nil {
		decisions = []TaskDecision{}
	}
	return DecisionList{
		SchemaVersion: 1, CapturedAt: queries.now(),
		StateVersion: stateVersion, Decisions: decisions,
	}, nil
}

// ShowDecision reads one keyed decision.
//
// The scope goes to the store rather than filtering a fleet-wide read in memory,
// so naming one task cannot surface another task's private questions.
func (queries *Queries) ShowDecision(
	ctx context.Context,
	taskHandle string,
	externalKey string,
) (TaskDecision, error) {
	if err := domain.ValidateTaskHandle(taskHandle); err != nil {
		return TaskDecision{}, invalidReferenceFailure("task reference", err)
	}
	if err := domain.ValidateDecisionKey(externalKey); err != nil {
		return TaskDecision{}, invalidReferenceFailure("decision reference", err)
	}
	decisions, _, err := queries.readDecisions(ctx, taskHandle)
	if err != nil {
		return TaskDecision{}, err
	}
	for _, decision := range decisions {
		if decision.ExternalKey == externalKey {
			return decision, nil
		}
	}
	return TaskDecision{}, translateReadError(ErrNotFound, "decision")
}

// readDecisions reads the inventory and attaches each decision's next airing.
//
// An unconfigured inventory is reported as unavailable rather than as an empty
// set: "nothing is waiting on you" is the one answer this read must never give
// when it simply could not look.
func (queries *Queries) readDecisions(
	ctx context.Context,
	taskHandle string,
) ([]TaskDecision, int64, error) {
	if queries.decisions == nil {
		return nil, 0, translateReadError(nil, "decision inventory")
	}
	stateVersion, err := queries.repository.CurrentStateVersion(ctx)
	if err != nil {
		return nil, 0, translateReadError(err, "decision inventory")
	}
	decisions, err := queries.decisions.ListTaskDecisions(ctx, taskHandle)
	if err != nil {
		return nil, 0, translateReadError(err, "decision inventory")
	}
	policy := queries.decisionSurfacing
	if policy == (DecisionSurfacingPolicy{}) {
		policy = DefaultDecisionSurfacingPolicy
	}
	for index := range decisions {
		decisions[index].StateVersion = stateVersion
		// Only an already-asked question has a cadence; the first airing is owed
		// by the durable outbox, so promising a repeat before it would schedule
		// a repeat of something nobody has asked.
		if decisions[index].Status != DecisionAwaitingHuman || decisions[index].LastAiringAt == nil {
			continue
		}
		next := decisions[index].LastAiringAt.Add(policy.Interval(decisions[index].Airings))
		decisions[index].NextAiringAt = &next
	}
	return decisions, stateVersion, nil
}
