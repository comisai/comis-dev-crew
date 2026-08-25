package git

import (
	"context"
	"errors"
	"path/filepath"

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
	if err := registry.promoteCompletedRebaseProof(ctx, request, resultingHead); err != nil {
		return err
	}
	return registry.retireIntegrationRebaseProof(ctx, request, resultingHead)
}

func (registry *Registry) materializeIntegrationResult(
	ctx context.Context,
	request application.IntegrationAdapterRequest,
	targetRef string,
	resultingHead string,
) error {
	gitDirectory, err := runGit(ctx, registry.gitExecutable, "--no-optional-locks", "-C", request.Target.WorktreePath,
		"rev-parse", "--absolute-git-dir")
	if err != nil || !filepath.IsAbs(gitDirectory) {
		return errors.New("apply integration candidate: target index identity is unavailable")
	}
	workspace := gitWorkspaceEnvironment{
		gitDir: gitDirectory, gitWorkTree: request.Target.WorktreePath, gitIndex: filepath.Join(gitDirectory, "index"),
	}
	if request.Strategy != application.IntegrationRebase {
		if err := registry.validateIntegrationExecutionPolicy(ctx, request); err != nil {
			return errors.Join(err, application.ErrIntegrationMutationNotStarted)
		}
		if err := registry.validateIntegrationMutationDeadline(request); err != nil {
			return err
		}
	}
	if _, err := runGitBytesInWorkspace(ctx, registry.gitExecutable, workspace,
		"-c", "core.hooksPath=/dev/null", "-c", "core.fsmonitor=false",
		"read-tree", "--reset", "-u", resultingHead); err != nil {
		return errors.New("apply integration candidate: proved result could not be materialized")
	}
	if _, err := runGitBytes(ctx, registry.gitExecutable, "--no-optional-locks", "-C", request.Target.WorktreePath,
		"update-ref", targetRef, resultingHead, request.Target.ExpectedHead); err != nil {
		_, _ = runGitBytesInWorkspace(ctx, registry.gitExecutable, workspace,
			"-c", "core.hooksPath=/dev/null", "-c", "core.fsmonitor=false",
			"read-tree", "--reset", "-u", request.Target.ExpectedHead)
		return errors.New("apply integration candidate: target branch changed before proved result")
	}
	return nil
}
