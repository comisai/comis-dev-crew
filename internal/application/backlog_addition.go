package application

import (
	"context"
	"errors"
	"time"

	"github.com/comisai/comis-dev-crew/internal/domain"
)

const commandAddBacklog = "AddBacklog"

// BacklogIDSource mints one opaque durable request identity.
type BacklogIDSource func(operationID string) (string, error)

// BacklogAdditionCommand carries bounded intake without execution authority.
type BacklogAdditionCommand struct {
	OperationID           string
	RepositoryID          string
	Shape                 domain.TaskShape
	RequestedOutcome      string
	DependsOn             []string
	Priority              domain.BacklogPriority
	Readiness             domain.BacklogReadiness
	SourceConversationRef string
}

// BacklogAdditionMutation is the atomic addition record.
type BacklogAdditionMutation struct {
	OperationID   string
	SubjectDigest string
	Item          domain.BacklogItem
	At            time.Time
}

// BacklogAdditionResult is the exact durable addition projection.
type BacklogAdditionResult struct {
	Item      domain.BacklogItem     `json:"item"`
	Operation domain.OperationRecord `json:"-"`
}

// BacklogAdditionStore owns exact replay and the item-plus-operation commit.
type BacklogAdditionStore interface {
	ReplayBacklogAddition(context.Context, string, string) (BacklogAdditionResult, bool, error)
	CommitBacklogAddition(context.Context, BacklogAdditionMutation) (BacklogAdditionResult, error)
}

// BacklogAdditionConfig binds bounded intake to the sole durable writer.
type BacklogAdditionConfig struct {
	Store      BacklogAdditionStore
	BacklogIDs BacklogIDSource
	Clock      Clock
}

// BacklogAdditions owns durable backlog intake.
type BacklogAdditions struct {
	store      BacklogAdditionStore
	backlogIDs BacklogIDSource
	clock      Clock
}

// NewBacklogAdditions validates the intake composition.
func NewBacklogAdditions(config BacklogAdditionConfig) (*BacklogAdditions, error) {
	if config.Store == nil || config.BacklogIDs == nil || config.Clock == nil {
		return nil, errors.New("create backlog additions: store, backlog IDs, and clock are required")
	}
	return &BacklogAdditions{store: config.Store, backlogIDs: config.BacklogIDs, clock: config.Clock}, nil
}

// AddBacklog records one bounded request without creating run authority.
func (additions *BacklogAdditions) AddBacklog(
	ctx context.Context,
	command BacklogAdditionCommand,
) (BacklogAdditionResult, error) {
	if err := validMutationContext(ctx); err != nil {
		return BacklogAdditionResult{}, err
	}
	if domain.ValidateOperationID(command.OperationID) != nil {
		return BacklogAdditionResult{}, mutationValidationFailure("backlog addition operation is invalid")
	}
	if command.Readiness != domain.BacklogReady && command.Readiness != domain.BacklogNeedsRefinement {
		return BacklogAdditionResult{}, mutationValidationFailure("backlog addition readiness must be ready or needs_refinement")
	}
	subjectDigest, err := digestMutationSubject(command)
	if err != nil {
		return BacklogAdditionResult{}, mutationValidationFailure("backlog addition subject cannot be encoded")
	}
	if replay, found, err := additions.store.ReplayBacklogAddition(ctx, command.OperationID, subjectDigest); err != nil {
		return BacklogAdditionResult{}, mutationReplayFailure(err)
	} else if found {
		return replay, nil
	}
	handle, err := additions.backlogIDs(command.OperationID)
	if err != nil {
		return BacklogAdditionResult{}, &dependencyFailure{message: "backlog identity source failed", cause: err}
	}
	at := additions.clock().UTC()
	item := domain.BacklogItem{
		SchemaVersion: 1, Handle: handle, RepositoryID: command.RepositoryID,
		Shape: command.Shape, RequestedOutcome: command.RequestedOutcome,
		DependsOn: append([]string(nil), command.DependsOn...), Priority: command.Priority,
		Readiness: command.Readiness, SourceConversationRef: command.SourceConversationRef,
		CreatedAt: at, UpdatedAt: at,
	}
	if err := item.Validate(); err != nil {
		return BacklogAdditionResult{}, mutationValidationFailure("backlog addition is invalid")
	}
	result, err := additions.store.CommitBacklogAddition(ctx, BacklogAdditionMutation{
		OperationID: command.OperationID, SubjectDigest: subjectDigest, Item: item, At: at,
	})
	if err != nil {
		return BacklogAdditionResult{}, mutationCommitFailure(err)
	}
	if result.Item.Validate() != nil || result.Item.Handle != item.Handle ||
		result.Operation.ID != command.OperationID || result.Operation.Command != commandAddBacklog ||
		result.Operation.SubjectDigest != subjectDigest || result.Operation.Status != domain.OperationCompleted ||
		result.Operation.ResultRef != item.Handle {
		return BacklogAdditionResult{}, &dependencyFailure{message: "backlog addition result is invalid"}
	}
	return result, nil
}
