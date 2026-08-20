package application

import (
	"context"
	"errors"
	"sort"

	"github.com/comisai/comis-dev-crew/internal/domain"
)

// InitiativeQueryStore supplies transactionally consistent initiative views.
type InitiativeQueryStore interface {
	InitiativeSnapshot(context.Context) ([]domain.DevelopmentInitiative, int64, error)
	InitiativeObservation(context.Context, string) (domain.DevelopmentInitiative, []domain.Task, int64, error)
	BacklogSnapshot(context.Context) ([]domain.BacklogItem, int64, error)
}

// InitiativeQueryConfig binds the read-only initiative and backlog authority.
type InitiativeQueryConfig struct {
	Store InitiativeQueryStore
	Clock Clock
}

// InitiativeQueries owns canonical initiative and backlog reads.
type InitiativeQueries struct {
	store InitiativeQueryStore
	clock Clock
}

// NewInitiativeQueries validates and binds initiative reads.
func NewInitiativeQueries(config InitiativeQueryConfig) (*InitiativeQueries, error) {
	if config.Store == nil || config.Clock == nil {
		return nil, errors.New("create initiative queries: store and clock are required")
	}
	return &InitiativeQueries{store: config.Store, clock: config.Clock}, nil
}

// ListInitiatives returns a deterministic optionally state-scoped snapshot.
func (queries *InitiativeQueries) ListInitiatives(
	ctx context.Context,
	state domain.InitiativeState,
) (InitiativeList, error) {
	if state != "" && domain.ValidateInitiativeState(state) != nil {
		return InitiativeList{}, invalidReferenceFailure("initiative state", errors.New("state is not known"))
	}
	initiatives, stateVersion, err := queries.store.InitiativeSnapshot(ctx)
	if err != nil {
		return InitiativeList{}, translateReadError(err, "initiative list")
	}
	summaries := make([]InitiativeSummary, 0, len(initiatives))
	for _, initiative := range initiatives {
		if state != "" && initiative.State != state {
			continue
		}
		taskCount := 0
		for _, component := range initiative.Components {
			taskCount += len(component.TaskHandles)
		}
		summaries = append(summaries, InitiativeSummary{
			InitiativeHandle: initiative.Handle, TitleRef: initiative.TitleRef,
			State: initiative.State, StateVersion: initiative.StateVersion,
			ComponentCount: len(initiative.Components), TaskCount: taskCount,
			UpdatedAt: initiative.UpdatedAt,
		})
	}
	sort.Slice(summaries, func(left, right int) bool {
		return summaries[left].InitiativeHandle < summaries[right].InitiativeHandle
	})
	return InitiativeList{
		SchemaVersion: 1, CapturedAtMs: queries.clock().UTC().UnixMilli(),
		StateVersion: stateVersion, Initiatives: summaries,
	}, nil
}

// GetInitiative returns one durable initiative and the states of every member.
func (queries *InitiativeQueries) GetInitiative(
	ctx context.Context,
	handle string,
) (InitiativeDetail, error) {
	if domain.ValidateTaskHandle(handle) != nil {
		return InitiativeDetail{}, invalidReferenceFailure("initiative handle", errors.New("handle is invalid"))
	}
	initiative, tasks, stateVersion, err := queries.store.InitiativeObservation(ctx, handle)
	if err != nil {
		return InitiativeDetail{}, translateReadError(err, "initiative")
	}
	states := make(map[string]domain.TaskState, len(tasks))
	for _, task := range tasks {
		states[task.Handle] = task.State
	}
	observedAt := queries.clock().UTC()
	reason, explanation, actions := explainInitiativeState(initiative.State)
	return InitiativeDetail{
		SchemaVersion: 1, CapturedAtMs: observedAt.UnixMilli(), StateVersion: stateVersion,
		Initiative: initiative, Graph: ProjectInitiativeGraph(initiative, states, observedAt),
		ReasonCode: reason, Explanation: explanation, NextSafeActions: actions,
	}, nil
}

// ListBacklog returns scoped bounded requests without creating work authority.
func (queries *InitiativeQueries) ListBacklog(
	ctx context.Context,
	filter BacklogFilter,
) (BacklogList, error) {
	if filter.RepositoryID != "" && domain.ValidateRepositoryID(filter.RepositoryID) != nil {
		return BacklogList{}, invalidReferenceFailure("repository ID", errors.New("repository is invalid"))
	}
	if filter.Readiness != "" && domain.ValidateBacklogReadiness(filter.Readiness) != nil {
		return BacklogList{}, invalidReferenceFailure("backlog readiness", errors.New("readiness is not known"))
	}
	items, stateVersion, err := queries.store.BacklogSnapshot(ctx)
	if err != nil {
		return BacklogList{}, translateReadError(err, "backlog")
	}
	filtered := make([]domain.BacklogItem, 0, len(items))
	for _, item := range items {
		if filter.RepositoryID != "" && item.RepositoryID != filter.RepositoryID {
			continue
		}
		if filter.Readiness != "" && item.Readiness != filter.Readiness {
			continue
		}
		filtered = append(filtered, item)
	}
	sort.Slice(filtered, func(left, right int) bool { return filtered[left].Handle < filtered[right].Handle })
	return BacklogList{
		SchemaVersion: 1, CapturedAtMs: queries.clock().UTC().UnixMilli(),
		StateVersion: stateVersion, Items: filtered,
	}, nil
}

func explainInitiativeState(state domain.InitiativeState) (string, string, []InitiativeNextAction) {
	switch state {
	case domain.InitiativePreparing:
		return "initiative_preparing", "The initiative is prepared and awaits complete host binding.",
			[]InitiativeNextAction{InitiativeActionInspect, InitiativeActionCancel}
	case domain.InitiativeActive, domain.InitiativeIntegrating, domain.InitiativeValidating:
		return "initiative_" + string(state), "The initiative is progressing under durable group authority.",
			[]InitiativeNextAction{InitiativeActionPause, InitiativeActionCancel}
	case domain.InitiativeBlocked:
		return "initiative_blocked", "At least one initiative member cannot currently progress.",
			[]InitiativeNextAction{InitiativeActionInspect, InitiativeActionResume, InitiativeActionCancel}
	case domain.InitiativeUnknown:
		return "initiative_unknown", "Current group authority cannot be proven from durable state.",
			[]InitiativeNextAction{InitiativeActionInspect, InitiativeActionCancel}
	case domain.InitiativeCandidateComplete:
		return "initiative_candidate_complete", "Every required candidate is ready for integration review.",
			[]InitiativeNextAction{InitiativeActionInspect, InitiativeActionCancel}
	case domain.InitiativeDelivered, domain.InitiativeFailed, domain.InitiativeCancelled:
		return "initiative_" + string(state), "The initiative has reached a terminal durable posture.",
			[]InitiativeNextAction{InitiativeActionNone}
	default:
		return "initiative_unknown", "The initiative state cannot be interpreted safely.",
			[]InitiativeNextAction{InitiativeActionInspect}
	}
}
