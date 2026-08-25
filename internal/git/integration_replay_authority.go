package git

import (
	"context"
	"errors"
	"os"
	"path/filepath"

	"github.com/comisai/comis-dev-crew/internal/application"
)

func (registry *Registry) integrationReplayStatePristine(
	ctx context.Context,
	repository Repository,
	request application.IntegrationAdapterRequest,
) (bool, error) {
	requests := []application.IntegrationAdapterRequest{request}
	if request.RecoveryOperationID != "" {
		requests = append(requests, originalIntegrationRequest(request))
	}
	for _, identity := range requests {
		for _, outcome := range []string{"target", "conflicted", "applied", "rebased"} {
			receipt, err := registry.inspectIntegrationReceipt(
				ctx, request.Target.WorktreePath, integrationReceiptRef(outcome, identity),
			)
			if err != nil {
				return false, err
			}
			if receipt.kind != integrationReceiptAbsent {
				return false, nil
			}
		}
		proof, err := registry.inspectIntegrationReceipt(
			ctx, request.Target.WorktreePath, integrationRebaseProofRef(identity),
		)
		if err != nil {
			return false, err
		}
		if proof.kind != integrationReceiptAbsent {
			return false, nil
		}
		paths, err := integrationReplayArtifactPaths(repository, identity)
		if err != nil {
			return false, err
		}
		for _, path := range paths {
			if _, err := os.Lstat(path); err == nil {
				return false, nil
			} else if !errors.Is(err, os.ErrNotExist) {
				return false, errors.New("apply integration candidate: replay artifact identity is unavailable")
			}
		}
	}
	target, err := registry.InspectCandidate(ctx, CandidateSnapshotRequest{
		TaskHandle: request.Target.TaskHandle, RepositoryID: request.Target.RepositoryID,
		WorktreePath: request.Target.WorktreePath,
	})
	if err != nil {
		return false, err
	}
	if target.HeadRevision != request.Target.ExpectedHead || target.Branch != expectedIntegrationTargetBranch(request) ||
		target.Cleanliness != CandidateClean {
		return false, nil
	}
	gitDirectory, err := runGit(ctx, registry.gitExecutable, "--no-optional-locks", "-C",
		request.Target.WorktreePath, "rev-parse", "--absolute-git-dir")
	if err != nil || !filepath.IsAbs(gitDirectory) {
		return false, errors.New("apply integration candidate: replay sequencer identity is unavailable")
	}
	for _, name := range []string{"rebase-merge", "rebase-apply", "sequencer"} {
		if _, err := os.Lstat(filepath.Join(gitDirectory, name)); err == nil {
			return false, nil
		} else if !errors.Is(err, os.ErrNotExist) {
			return false, errors.New("apply integration candidate: replay sequencer identity is unavailable")
		}
	}
	return true, nil
}

func integrationReplayArtifactPaths(
	repository Repository,
	request application.IntegrationAdapterRequest,
) ([]string, error) {
	_, planPath, err := serverIntegrationPlanPath(repository, request)
	if err != nil {
		return nil, err
	}
	_, proofPath, err := serverRebaseProofPath(repository, request)
	if err != nil {
		return nil, err
	}
	_, transitionPath, err := integrationMaterializationPath(repository, request)
	if err != nil {
		return nil, err
	}
	return []string{planPath, proofPath, transitionPath}, nil
}
