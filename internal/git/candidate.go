package git

import (
	"context"
	"errors"
	"fmt"
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
		if ctx.Err() != nil {
			return CandidateSnapshot{}, ctx.Err()
		}
		if errors.Is(err, errCandidateWorktreeStructural) {
			return CandidateSnapshot{}, fmt.Errorf("inspect task candidate: worktree identity is invalid: %w", ErrCandidateWorktreeUnverified)
		}
		return CandidateSnapshot{}, fmt.Errorf("inspect task candidate: worktree inspection failed: %w", err)
	}
	entries, err := registry.worktreeEntries(ctx, repository)
	if err != nil {
		return CandidateSnapshot{}, err
	}
	entry, found := findWorktreeEntry(entries, request.WorktreePath)
	if !found || entry.locked || entry.prunable || entry.branch == "" || !gitRevisionPattern.MatchString(entry.head) {
		return CandidateSnapshot{}, fmt.Errorf("inspect task candidate: worktree inventory is ambiguous: %w", ErrCandidateWorktreeUnverified)
	}
	branch, err := runGit(ctx, registry.gitExecutable, "--no-optional-locks", "-C", request.WorktreePath,
		"symbolic-ref", "--quiet", "--short", "HEAD")
	if err != nil || branch != entry.branch {
		if ctx.Err() != nil {
			return CandidateSnapshot{}, ctx.Err()
		}
		if errors.Is(err, errGitInfrastructure) || errors.Is(err, errFilesystemInfrastructure) {
			return CandidateSnapshot{}, fmt.Errorf("inspect task candidate: branch inspection failed: %w", err)
		}
		return CandidateSnapshot{}, fmt.Errorf("inspect task candidate: branch identity differs: %w", ErrCandidateWorktreeUnverified)
	}
	head, err := runGit(ctx, registry.gitExecutable, "--no-optional-locks", "-C", request.WorktreePath,
		"rev-parse", "--verify", "HEAD^{commit}")
	if err != nil || head != entry.head {
		if ctx.Err() != nil {
			return CandidateSnapshot{}, ctx.Err()
		}
		if errors.Is(err, errGitInfrastructure) || errors.Is(err, errFilesystemInfrastructure) {
			return CandidateSnapshot{}, fmt.Errorf("inspect task candidate: head inspection failed: %w", err)
		}
		return CandidateSnapshot{}, fmt.Errorf("inspect task candidate: head identity differs: %w", ErrCandidateWorktreeUnverified)
	}
	status, err := runGitBytes(ctx, registry.gitExecutable, "--no-optional-locks", "-C", request.WorktreePath,
		"status", "--porcelain=v2", "-z", "--untracked-files=all")
	if err != nil {
		if ctx.Err() != nil {
			return CandidateSnapshot{}, ctx.Err()
		}
		if errors.Is(err, errGitInfrastructure) || errors.Is(err, errFilesystemInfrastructure) {
			return CandidateSnapshot{}, fmt.Errorf("inspect task candidate: status inspection failed: %w", err)
		}
		return CandidateSnapshot{}, fmt.Errorf("inspect task candidate: worktree status is unavailable: %w", ErrCandidateWorktreeUnverified)
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
