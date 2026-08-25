package git

import (
	"context"
	"errors"

	"github.com/comisai/comis-dev-crew/internal/application"
)

func (registry *Registry) serverRebaseProof(
	repository Repository,
	request application.IntegrationAdapterRequest,
) (serverRebaseProof, bool, error) {
	_, path, err := serverRebaseProofPath(repository, request)
	if err != nil {
		return serverRebaseProof{}, false, err
	}
	return readServerRebaseProof(path)
}

func (registry *Registry) applyIsolatedRebaseResult(
	ctx context.Context,
	request application.IntegrationAdapterRequest,
	targetRef string,
	resultingHead string,
) error {
	if err := registry.authorizeRebaseFinalization(ctx, request, resultingHead); err != nil {
		return err
	}
	proofRef := integrationRebaseProofRef(request)
	proof, err := registry.inspectIntegrationReceipt(ctx, request.Target.WorktreePath, proofRef)
	if err != nil {
		return errors.New("apply integration candidate: isolated result receipt is unavailable")
	}
	switch proof.kind {
	case integrationReceiptAbsent:
		if _, err := runGitBytes(ctx, registry.gitExecutable, "--no-optional-locks", "-C", request.Target.WorktreePath,
			"update-ref", proofRef, resultingHead, integrationZeroRevision); err != nil {
			return errors.New("apply integration candidate: isolated result receipt could not be recorded")
		}
	case integrationReceiptDirect:
		if proof.value == request.Candidate.HeadRevision {
			if _, err := runGitBytes(ctx, registry.gitExecutable, "--no-optional-locks", "-C", request.Target.WorktreePath,
				"update-ref", proofRef, resultingHead, request.Candidate.HeadRevision); err != nil {
				return errors.New("apply integration candidate: prepared result receipt could not be promoted")
			}
		} else if proof.value != resultingHead {
			return errors.New("apply integration candidate: isolated result receipt differs")
		}
	default:
		return errors.New("apply integration candidate: isolated result receipt is ambiguous")
	}
	if err := registry.materializeIntegrationResult(ctx, request, targetRef, resultingHead); err != nil {
		return err
	}
	if err := registry.authorizeRebaseFinalization(ctx, request, resultingHead); err != nil {
		return err
	}
	if err := registry.promoteCompletedRebaseProof(ctx, request, resultingHead); err != nil {
		return err
	}
	if err := registry.authorizeRebaseFinalization(ctx, request, resultingHead); err != nil {
		return err
	}
	return registry.retireIntegrationRebaseProof(ctx, request, resultingHead)
}
