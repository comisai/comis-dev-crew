package application

import (
	"context"

	"github.com/comisai/comis-dev-crew/internal/domain"
)

// PromoteScout creates a ship revision from one scout's investigation.
//
// The scout is not modified. Its handle, shape, evidence and history stay
// exactly as they were, and the promotion is recorded as a link between two
// tasks. This is the rule the operation exists to enforce: a worker that could
// turn its own scout into a ship task would grant itself push and pull-request
// authority the scout shape deliberately withholds, and the record of what was
// investigated would be overwritten by the work the investigation produced.
//
// The ship task inherits the scout's repository and base revision so it starts
// from the ground the investigation actually covered. It inherits nothing else:
// what the ship task must achieve is a new decision, stated by the caller.
func (mutations *Mutations) PromoteScout(
	ctx context.Context,
	command PromoteScoutCommand,
) (MutationResult, error) {
	if err := validMutationContext(ctx); err != nil {
		return MutationResult{}, err
	}
	if mutations.promotions == nil {
		return MutationResult{}, &dependencyFailure{message: "scout promotion authority is unavailable"}
	}
	if domain.ValidateOperationID(command.OperationID) != nil ||
		domain.ValidateTaskHandle(command.ScoutTaskHandle) != nil {
		return MutationResult{}, mutationValidationFailure("promotion identity is invalid")
	}
	source, err := mutations.promotions.ReadScoutPromotionSource(ctx, command.ScoutTaskHandle)
	if err != nil {
		return MutationResult{}, mutationCommitFailure(err)
	}
	// The prepared task is minted through the ordinary preparation path, so the
	// promotion re-evaluates workspace and credential needs exactly as any other
	// ship task does rather than inheriting a scout's lighter posture.
	prepared, err := mutations.PrepareTask(ctx, PrepareTaskCommand{
		OperationID:        command.OperationID,
		ServiceInstanceID:  command.ServiceInstanceID,
		Shape:              domain.ShapeShip,
		RepositoryID:       source.RepositoryID,
		BaseRevision:       source.BaseRevision,
		AcceptanceCriteria: append([]string(nil), command.AcceptanceCriteria...),
		Constraints:        append([]string(nil), command.Constraints...),
		ValidationProfile:  command.ValidationProfile,
		DeliveryMode:       command.DeliveryMode,
		WorkerProfileID:    command.WorkerProfileID,
	})
	if err != nil {
		return MutationResult{}, err
	}
	// The link is written after the task exists and is keyed by the same
	// operation, so a retry of an interrupted promotion replays the same prepared
	// task and re-attempts the link rather than minting a second ship task.
	if err := mutations.promotions.CommitScoutPromotionLink(ctx, ScoutPromotionLink{
		OperationID:     command.OperationID,
		ScoutTaskHandle: source.ScoutTaskHandle,
		ShipTaskHandle:  prepared.Task.Handle,
		EvidenceDigest:  source.EvidenceDigest,
		PromotedAt:      mutations.clock(),
	}); err != nil {
		return MutationResult{}, mutationCommitFailure(err)
	}
	return prepared, nil
}
