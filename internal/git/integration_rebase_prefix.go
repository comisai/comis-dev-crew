package git

import (
	"context"
	"errors"

	"github.com/comisai/comis-dev-crew/internal/application"
)

func (registry *Registry) currentServerRebasePrefix(
	ctx context.Context,
	repository Repository,
	request application.IntegrationAdapterRequest,
	proof serverRebaseProof,
	rebaseHead string,
) ([]string, error) {
	currentHead, err := runGit(ctx, registry.gitExecutable, "--no-optional-locks", "-C", request.Target.WorktreePath,
		"rev-parse", "--verify", "HEAD^{commit}")
	if err != nil {
		return nil, errors.New("apply integration candidate: continued rebase head is unavailable")
	}
	var continued []string
	if currentHead != request.Target.ExpectedHead {
		continued, err = registry.rebaseCommitRange(ctx, repository, request.Target.ExpectedHead, currentHead)
		if err != nil {
			return nil, errors.New("apply integration candidate: continued rebase range is unavailable")
		}
	}
	if len(continued) >= len(proof.candidateCommits) || proof.candidateCommits[len(continued)] != rebaseHead ||
		len(proof.continuedCommits) > len(continued) ||
		!sameRebaseCommits(proof.continuedCommits, continued[:len(proof.continuedCommits)]) {
		return nil, errors.New("apply integration candidate: continued rebase chain differs")
	}
	resolved := make(map[string]struct{}, len(proof.resolvedCommits))
	for _, commit := range proof.resolvedCommits {
		resolved[commit] = struct{}{}
	}
	for index, result := range continued {
		if index < len(proof.continuedCommits) {
			continue
		}
		if _, wasResolved := resolved[proof.candidateCommits[index]]; wasResolved {
			continue
		}
		patch, patchErr := registry.rebasePatchIdentity(ctx, repository, result)
		if patchErr != nil || patch != proof.candidatePatches[index] {
			return nil, errors.New("apply integration candidate: continued rebase content differs")
		}
	}
	return continued, nil
}

func (registry *Registry) requireServerRebasePrefix(
	ctx context.Context,
	repository Repository,
	request application.IntegrationAdapterRequest,
	proof serverRebaseProof,
	rebaseHead string,
) error {
	continued, err := registry.currentServerRebasePrefix(ctx, repository, request, proof, rebaseHead)
	if err != nil || !sameRebaseCommits(continued, proof.continuedCommits) {
		return errors.New("apply integration candidate: continued rebase chain differs")
	}
	return nil
}
