package git

import (
	"context"
	"errors"
)

func (registry *Registry) inspectIntegrationCandidate(
	ctx context.Context,
	request CandidateSnapshotRequest,
) (CandidateSnapshot, error) {
	entry, err := registry.inspectCandidateWorktreeIdentity(ctx, request)
	if err != nil || entry.branch == "" {
		return CandidateSnapshot{}, errors.New("apply integration candidate: worktree identity is unavailable")
	}
	branch, err := runGit(ctx, registry.gitExecutable, "--no-optional-locks", "-C", request.WorktreePath,
		"symbolic-ref", "--quiet", "--short", "HEAD")
	if err != nil || branch != entry.branch {
		return CandidateSnapshot{}, errors.New("apply integration candidate: worktree branch differs")
	}
	head, err := runGit(ctx, registry.gitExecutable, "--no-optional-locks", "-C", request.WorktreePath,
		"rev-parse", "--verify", "HEAD^{commit}")
	if err != nil || head != entry.head {
		return CandidateSnapshot{}, errors.New("apply integration candidate: worktree head differs")
	}
	tree, err := registry.integrationCommitTree(ctx, request.WorktreePath, head)
	if err != nil {
		return CandidateSnapshot{}, err
	}
	snapshot, err := registry.loadIntegrationTreeSnapshot(ctx, request.WorktreePath, tree)
	if err != nil {
		return CandidateSnapshot{}, err
	}
	matches, err := integrationWorktreeMatchesSnapshot(request.WorktreePath, snapshot)
	if err != nil {
		return CandidateSnapshot{}, err
	}
	cleanliness := CandidateDirty
	indexTree, indexErr := registry.integrationIndexTree(ctx, request.WorktreePath)
	if indexErr == nil && indexTree == tree && matches {
		cleanliness = CandidateClean
	}
	return CandidateSnapshot{
		RepositoryID: request.RepositoryID, WorktreePath: request.WorktreePath,
		Branch: branch, HeadRevision: head, Cleanliness: cleanliness,
	}, nil
}
