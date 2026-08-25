package git

import (
	"context"
	"errors"
	"strings"

	"github.com/comisai/comis-dev-crew/internal/application"
)

func (registry *Registry) reconcileReceiptOnlyCompletedRebase(
	ctx context.Context,
	repository Repository,
	request application.IntegrationAdapterRequest,
) (application.IntegrationAdapterResult, bool, error) {
	if request.Strategy != application.IntegrationRebase {
		return application.IntegrationAdapterResult{}, false, nil
	}
	_, path, err := serverRebaseProofPath(repository, request)
	if err != nil {
		return application.IntegrationAdapterResult{}, true, err
	}
	proof, found, err := readServerRebaseProof(path)
	if err != nil {
		return application.IntegrationAdapterResult{}, true, err
	}
	if !found || proof.resultingHead == "" || len(proof.resultCommits) == 0 {
		return application.IntegrationAdapterResult{}, false, nil
	}
	if proof.operationID != originalIntegrationOperationID(request) {
		return application.IntegrationAdapterResult{}, true,
			errors.New("apply integration candidate: receipt-only proof identity differs")
	}
	if err := registry.requireServerRebaseProof(ctx, repository, request, proof.resultingHead); err != nil {
		return application.IntegrationAdapterResult{}, true, err
	}
	if err := registry.ensureRebaseSequencerAbsent(ctx, request.Target.WorktreePath); err != nil {
		return application.IntegrationAdapterResult{}, true, err
	}
	targetRef, rebasedFound, err := registry.validateReceiptOnlyRebaseReceipts(ctx, request, proof.resultingHead)
	if err != nil {
		return application.IntegrationAdapterResult{}, true, err
	}
	proofRef := integrationRebaseProofRef(request)
	proofHead, proofFound, err := registry.integrationReceiptHeadAtPath(
		ctx, request.Target.WorktreePath, proofRef,
	)
	if err != nil || proofFound && proofHead != proof.resultingHead {
		return application.IntegrationAdapterResult{}, true,
			errors.New("apply integration candidate: receipt-only proof receipt differs")
	}
	if !rebasedFound && !proofFound {
		return application.IntegrationAdapterResult{}, true,
			errors.New("apply integration candidate: receipt-only proof receipt is unavailable")
	}
	targetHead, err := registry.integrationBranchHead(ctx, request.Target.WorktreePath, targetRef)
	if err != nil || targetHead != proof.resultingHead {
		return application.IntegrationAdapterResult{}, true,
			errors.New("apply integration candidate: receipt-only target branch differs")
	}
	if !rebasedFound {
		completed, err := registry.completedIntegrationMaterializationTransition(
			ctx, request, targetRef, proof.resultingHead,
		)
		if err != nil || !completed {
			return application.IntegrationAdapterResult{}, true,
				errors.New("apply integration candidate: receipt-only completed materialization is unavailable")
		}
	}
	target, err := registry.InspectCandidate(ctx, CandidateSnapshotRequest{
		TaskHandle: request.Target.TaskHandle, RepositoryID: request.Target.RepositoryID,
		WorktreePath: request.Target.WorktreePath,
	})
	if err != nil || target.Cleanliness != CandidateClean || target.HeadRevision != proof.resultingHead {
		return application.IntegrationAdapterResult{}, true,
			errors.New("apply integration candidate: receipt-only completed rebase differs")
	}
	expectedBranch := expectedIntegrationTargetBranch(request)
	if target.Branch != expectedBranch {
		if !proofFound || target.Branch != strings.TrimPrefix(proofRef, "refs/heads/") {
			return application.IntegrationAdapterResult{}, true,
				errors.New("apply integration candidate: receipt-only completed rebase differs")
		}
		if err := registry.authorizeRebaseFinalization(ctx, request, proof.resultingHead); err != nil {
			return application.IntegrationAdapterResult{}, true, err
		}
		if _, err := runGitBytes(ctx, registry.gitExecutable, "--no-optional-locks", "-C", request.Target.WorktreePath,
			"symbolic-ref", "HEAD", targetRef); err != nil {
			return application.IntegrationAdapterResult{}, true,
				errors.New("apply integration candidate: receipt-only target could not be reattached")
		}
		target, err = registry.InspectCandidate(ctx, CandidateSnapshotRequest{
			TaskHandle: request.Target.TaskHandle, RepositoryID: request.Target.RepositoryID,
			WorktreePath: request.Target.WorktreePath,
		})
		if err != nil || target.Cleanliness != CandidateClean || target.HeadRevision != proof.resultingHead ||
			target.Branch != expectedBranch {
			return application.IntegrationAdapterResult{}, true,
				errors.New("apply integration candidate: receipt-only reattached target differs")
		}
	}
	return application.IntegrationAdapterResult{
		Outcome: application.IntegrationApplied, PreviousHead: request.Target.ExpectedHead,
		ResultingHead: proof.resultingHead,
	}, true, nil
}
