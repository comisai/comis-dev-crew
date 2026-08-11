package git

import (
	"context"
	"errors"
	"path/filepath"
)

// InspectCandidate revalidates one task-owned worktree and returns its exact
// branch, head, and tracked plus untracked cleanliness.
func (registry *Registry) InspectCandidate(ctx context.Context, request CandidateSnapshotRequest) (CandidateSnapshot, error) {
	if registry == nil || ctx == nil {
		return CandidateSnapshot{}, errors.New("inspect task candidate: registry and context are required")
	}
	if err := ctx.Err(); err != nil {
		return CandidateSnapshot{}, err
	}
	if !repositoryIDPattern.MatchString(request.TaskHandle) || !repositoryIDPattern.MatchString(request.RepositoryID) {
		return CandidateSnapshot{}, errors.New("inspect task candidate: request identity is invalid")
	}
	repository, err := registry.Resolve(request.RepositoryID)
	if err != nil {
		return CandidateSnapshot{}, errors.New("inspect task candidate: repository is unavailable")
	}
	expectedPath := filepath.Join(repository.WorktreeRoot, request.TaskHandle)
	if request.WorktreePath != expectedPath {
		return CandidateSnapshot{}, errors.New("inspect task candidate: worktree does not match task root")
	}
	if _, err := registry.ValidateWorktree(ctx, request.RepositoryID, request.WorktreePath); err != nil {
		return CandidateSnapshot{}, errors.New("inspect task candidate: worktree identity is invalid")
	}
	entries, err := registry.worktreeEntries(ctx, repository)
	if err != nil {
		return CandidateSnapshot{}, err
	}
	entry, found := findWorktreeEntry(entries, request.WorktreePath)
	if !found || entry.locked || entry.prunable || entry.branch == "" || !gitRevisionPattern.MatchString(entry.head) {
		return CandidateSnapshot{}, errors.New("inspect task candidate: worktree inventory is ambiguous")
	}
	branch, err := runGit(ctx, registry.gitExecutable, "--no-optional-locks", "-C", request.WorktreePath,
		"symbolic-ref", "--quiet", "--short", "HEAD")
	if err != nil || branch != entry.branch {
		return CandidateSnapshot{}, errors.New("inspect task candidate: branch identity differs")
	}
	head, err := runGit(ctx, registry.gitExecutable, "--no-optional-locks", "-C", request.WorktreePath,
		"rev-parse", "--verify", "HEAD^{commit}")
	if err != nil || head != entry.head {
		return CandidateSnapshot{}, errors.New("inspect task candidate: head identity differs")
	}
	status, err := runGitBytes(ctx, registry.gitExecutable, "--no-optional-locks", "-C", request.WorktreePath,
		"status", "--porcelain=v2", "-z", "--untracked-files=all")
	if err != nil {
		return CandidateSnapshot{}, errors.New("inspect task candidate: worktree status is unavailable")
	}
	cleanliness := CandidateClean
	if len(status) != 0 {
		cleanliness = CandidateDirty
	}
	return CandidateSnapshot{
		RepositoryID: request.RepositoryID, WorktreePath: request.WorktreePath,
		Branch: branch, HeadRevision: head, Cleanliness: cleanliness,
	}, nil
}
