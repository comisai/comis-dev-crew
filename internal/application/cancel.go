package application

import (
	"context"

	"github.com/comisai/comis-dev-crew/internal/domain"
)

// CancelManagedRun stops one activated run on the host's authority.
//
// The host has already recorded its own decision by the time this arrives, so a
// run this service has already settled is not an error: it answers with the
// settled task rather than refusing, which is what makes a repeated cancel safe.
func (mutations *Mutations) CancelManagedRun(ctx context.Context, command CancelManagedRunCommand) (MutationResult, error) {
	if err := validMutationContext(ctx); err != nil {
		return MutationResult{}, err
	}
	if err := domain.ValidateOperationID(command.OperationID); err != nil {
		return MutationResult{}, mutationValidationFailure("operation ID is invalid")
	}
	if err := domain.ValidateAuthorityReference("serviceInstanceId", command.ServiceInstanceID); err != nil {
		return MutationResult{}, mutationValidationFailure("service instance identity is invalid")
	}
	if err := domain.ValidateAuthorityReference("managedRunId", command.ManagedRunID); err != nil {
		return MutationResult{}, mutationValidationFailure("managed run identity is invalid")
	}
	if !command.Reason.valid() {
		return MutationResult{}, mutationValidationFailure("cancellation reason is invalid")
	}
	subjectDigest, err := digestMutationSubject(command)
	if err != nil {
		return MutationResult{}, mutationValidationFailure("cancellation subject cannot be encoded")
	}
	if replay, found, err := mutations.store.ReplayMutation(ctx, command.OperationID, commandCancelManagedRun, subjectDigest); err != nil {
		return MutationResult{}, mutationReplayFailure(err)
	} else if found {
		return replay, nil
	}
	result, err := mutations.store.CommitManagedRunCancel(ctx, ManagedRunCancelMutation{
		ServiceInstanceID: command.ServiceInstanceID, ManagedRunID: command.ManagedRunID,
		Reason: command.Reason, OperationID: command.OperationID,
		SubjectDigest: subjectDigest, At: mutations.clock(),
	})
	if err != nil {
		return MutationResult{}, err
	}
	return result, nil
}
