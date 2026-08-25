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
) ([]string, []string, []serverRebaseConflict, error) {
	currentHead, err := runGit(ctx, registry.gitExecutable, "--no-optional-locks", "-C", request.Target.WorktreePath,
		"rev-parse", "--verify", "HEAD^{commit}")
	if err != nil {
		return nil, nil, nil, errors.New("apply integration candidate: continued rebase head is unavailable")
	}
	var continued []string
	if currentHead != request.Target.ExpectedHead {
		continued, err = registry.rebaseCommitRange(ctx, repository, request.Target.ExpectedHead, currentHead)
		if err != nil {
			return nil, nil, nil, errors.New("apply integration candidate: continued rebase range is unavailable")
		}
	}
	if len(continued) >= len(proof.candidateCommits) || proof.candidateCommits[len(continued)] != rebaseHead ||
		len(proof.continuedCommits) > len(continued) ||
		!sameRebaseCommits(proof.continuedCommits, continued[:len(proof.continuedCommits)]) {
		return nil, nil, nil, errors.New("apply integration candidate: continued rebase chain differs")
	}
	resolved, conflicts, err := registry.advanceServerRebaseResults(ctx, repository, request, proof, continued)
	if err != nil {
		return nil, nil, nil, err
	}
	return continued, resolved, conflicts, nil
}

func (registry *Registry) advanceServerRebaseResults(
	ctx context.Context,
	repository Repository,
	request application.IntegrationAdapterRequest,
	proof serverRebaseProof,
	continued []string,
) ([]string, []serverRebaseConflict, error) {
	if len(proof.continuedCommits) > len(continued) ||
		!sameRebaseCommits(proof.continuedCommits, continued[:len(proof.continuedCommits)]) ||
		len(continued) > len(proof.candidateCommits) {
		return nil, nil, errors.New("apply integration candidate: continued rebase chain differs")
	}
	resolved := make(map[string]struct{}, len(proof.resolvedCommits))
	for _, commit := range proof.resolvedCommits {
		resolved[commit] = struct{}{}
	}
	resolvedCommits := append([]string(nil), proof.resolvedCommits...)
	conflicts := append([]serverRebaseConflict(nil), proof.conflicts...)
	for index, result := range continued {
		if index < len(proof.continuedCommits) {
			continue
		}
		candidate := proof.candidateCommits[index]
		if _, wasResolved := resolved[candidate]; wasResolved {
			continue
		}
		if conflictIndex := serverRebaseConflictIndex(conflicts, candidate); conflictIndex >= 0 {
			conflict := conflicts[conflictIndex]
			if conflict.resolvedTree == "" {
				return nil, nil, errors.New("apply integration candidate: continued conflict result differs")
			}
			if conflict.expectedResult == "" {
				parent := request.Target.ExpectedHead
				if index > 0 {
					parent = continued[index-1]
				}
				if err := registry.validateRebaseConflictResult(
					ctx, repository, candidate, parent, conflict.resolvedTree, result,
				); err != nil {
					return nil, nil, err
				}
				conflicts[conflictIndex].expectedResult = result
			} else if result != conflict.expectedResult {
				return nil, nil, errors.New("apply integration candidate: continued conflict result differs")
			}
			resolved[candidate] = struct{}{}
			resolvedCommits = appendResolvedRebaseCommit(proof.candidateCommits, resolvedCommits, candidate)
			continue
		}
		patch, patchErr := registry.rebasePatchIdentity(ctx, repository, result)
		if patchErr != nil || patch != proof.candidatePatches[index] {
			return nil, nil, errors.New("apply integration candidate: continued rebase content differs")
		}
	}
	return resolvedCommits, conflicts, nil
}

func (registry *Registry) requireServerRebasePrefix(
	ctx context.Context,
	repository Repository,
	request application.IntegrationAdapterRequest,
	proof serverRebaseProof,
	rebaseHead string,
) error {
	continued, resolved, conflicts, err := registry.currentServerRebasePrefix(ctx, repository, request, proof, rebaseHead)
	if err != nil || !sameRebaseCommits(continued, proof.continuedCommits) ||
		!sameRebaseCommits(resolved, proof.resolvedCommits) || !sameServerRebaseConflicts(conflicts, proof.conflicts) {
		return errors.New("apply integration candidate: continued rebase chain differs")
	}
	return nil
}

func serverRebaseConflictIndex(conflicts []serverRebaseConflict, commit string) int {
	for index := range conflicts {
		if conflicts[index].commit == commit {
			return index
		}
	}
	return -1
}

func serverRebaseConflictForCommit(conflicts []serverRebaseConflict, commit string) (serverRebaseConflict, bool) {
	index := serverRebaseConflictIndex(conflicts, commit)
	if index >= 0 {
		return conflicts[index], true
	}
	return serverRebaseConflict{}, false
}
