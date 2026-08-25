package git

import (
	"context"
	"errors"
	"os"
	"path/filepath"

	"github.com/comisai/comis-dev-crew/internal/application"
)

func (registry *Registry) ensureRebaseSequencerAbsent(ctx context.Context, worktreePath string) error {
	gitDir, err := runGit(ctx, registry.gitExecutable, "--no-optional-locks", "-C", worktreePath,
		"rev-parse", "--absolute-git-dir")
	if err != nil || !filepath.IsAbs(gitDir) {
		return errors.New("apply integration candidate: rebase sequencer is unavailable")
	}
	for _, name := range []string{"rebase-merge", "rebase-apply"} {
		_, statErr := os.Lstat(filepath.Join(gitDir, name))
		if statErr == nil {
			return errors.New("apply integration candidate: rebase sequencer is still active")
		}
		if !errors.Is(statErr, os.ErrNotExist) {
			return errors.New("apply integration candidate: rebase sequencer is unavailable")
		}
	}
	return nil
}

func (registry *Registry) finalizeRecoveredRebase(
	ctx context.Context,
	request application.IntegrationAdapterRequest,
	repository Repository,
	targetRef string,
	resultingHead string,
) (application.IntegrationAdapterResult, error) {
	if err := registry.authorizeRebaseFinalization(ctx, request, resultingHead); err != nil {
		return application.IntegrationAdapterResult{}, err
	}
	if found, err := registry.reconcileIntegrationMaterialization(
		ctx, request, targetRef, resultingHead,
	); err != nil {
		return application.IntegrationAdapterResult{}, err
	} else if found {
		if err := registry.authorizeRebaseFinalization(ctx, request, resultingHead); err != nil {
			return application.IntegrationAdapterResult{}, err
		}
	}
	currentHead, err := registry.inspectRecoveredRebaseHead(ctx, request)
	if err != nil || currentHead != resultingHead {
		return application.IntegrationAdapterResult{}, errors.New("apply integration candidate: rebased receipt differs from worktree")
	}
	if err := registry.authorizeRebaseFinalization(ctx, request, resultingHead); err != nil {
		return application.IntegrationAdapterResult{}, err
	}
	if err := registry.completeServerRebaseProof(ctx, repository, request, resultingHead); err != nil {
		return application.IntegrationAdapterResult{}, err
	}
	if err := registry.authorizeRebaseFinalization(ctx, request, resultingHead); err != nil {
		return application.IntegrationAdapterResult{}, err
	}
	if err := registry.promoteCompletedRebaseProof(ctx, request, resultingHead); err != nil {
		return application.IntegrationAdapterResult{}, err
	}
	branchHead, err := runGit(ctx, registry.gitExecutable, "--no-optional-locks", "-C", request.Target.WorktreePath,
		"rev-parse", "--verify", targetRef+"^{commit}")
	if err != nil {
		return application.IntegrationAdapterResult{}, errors.New("apply integration candidate: target branch is unavailable")
	}
	if branchHead == request.Target.ExpectedHead {
		if err := registry.authorizeRebaseFinalization(ctx, request, resultingHead); err != nil {
			return application.IntegrationAdapterResult{}, err
		}
		if _, err := runGitBytes(ctx, registry.gitExecutable, "--no-optional-locks", "-C", request.Target.WorktreePath,
			"update-ref", targetRef, resultingHead, request.Target.ExpectedHead); err != nil {
			return application.IntegrationAdapterResult{}, errors.New("apply integration candidate: target branch changed during recovery")
		}
	} else if branchHead != resultingHead {
		return application.IntegrationAdapterResult{}, errors.New("apply integration candidate: target branch differs from recovered head")
	}
	if err := registry.authorizeRebaseFinalization(ctx, request, resultingHead); err != nil {
		return application.IntegrationAdapterResult{}, err
	}
	if _, err := runGitBytes(ctx, registry.gitExecutable, "--no-optional-locks", "-C", request.Target.WorktreePath,
		"symbolic-ref", "HEAD", targetRef); err != nil {
		return application.IntegrationAdapterResult{}, errors.New("apply integration candidate: recovered target could not be reattached")
	}
	if err := registry.authorizeRebaseFinalization(ctx, request, resultingHead); err != nil {
		return application.IntegrationAdapterResult{}, err
	}
	if err := registry.retireIntegrationRebaseProof(ctx, request, resultingHead); err != nil {
		return application.IntegrationAdapterResult{}, err
	}
	final, err := registry.InspectCandidate(ctx, CandidateSnapshotRequest{
		TaskHandle: request.Target.TaskHandle, RepositoryID: request.Target.RepositoryID,
		WorktreePath: request.Target.WorktreePath,
	})
	if err != nil || final.Cleanliness != CandidateClean || final.HeadRevision != resultingHead ||
		final.Branch != expectedIntegrationTargetBranch(request) {
		return application.IntegrationAdapterResult{}, errors.New("apply integration candidate: recovered target is unverified")
	}
	if err := registry.authorizeRebaseFinalization(ctx, request, resultingHead); err != nil {
		return application.IntegrationAdapterResult{}, err
	}
	if err := registry.createIntegrationReceipt(
		ctx, repository, integrationReceiptRef("applied", request), resultingHead,
	); err != nil {
		return application.IntegrationAdapterResult{}, errors.New("apply integration candidate: recovery applied receipt could not be recorded")
	}
	return application.IntegrationAdapterResult{
		Outcome: application.IntegrationApplied, PreviousHead: request.Target.ExpectedHead,
		ResultingHead: resultingHead,
	}, nil
}
