package git

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"regexp"
)

var fixtureArtifactPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,127}$`)

// PrepareFixtureCandidate creates or adopts the one exact deterministic
// candidate commit used by the installed fixture worker.
func (registry *Registry) PrepareFixtureCandidate(
	ctx context.Context,
	request FixtureCandidateRequest,
) (CandidateSnapshot, error) {
	if registry == nil || ctx == nil {
		return CandidateSnapshot{}, errors.New("prepare fixture candidate: registry and context are required")
	}
	if err := ctx.Err(); err != nil {
		return CandidateSnapshot{}, err
	}
	if !repositoryIDPattern.MatchString(request.TaskHandle) ||
		!repositoryIDPattern.MatchString(request.RepositoryID) ||
		!gitRevisionPattern.MatchString(request.BaseRevision) ||
		!fixtureArtifactPattern.MatchString(request.ArtifactRelativePath) {
		return CandidateSnapshot{}, errors.New("prepare fixture candidate: request identity is invalid")
	}

	registry.mu.Lock()
	defer registry.mu.Unlock()
	repository, err := registry.Resolve(request.RepositoryID)
	if err != nil {
		return CandidateSnapshot{}, errors.New("prepare fixture candidate: repository is unavailable")
	}
	expectedPath := filepath.Join(repository.WorktreeRoot, request.TaskHandle)
	if request.WorktreePath != expectedPath {
		return CandidateSnapshot{}, errors.New("prepare fixture candidate: worktree does not match task root")
	}
	if err := registry.validatePinnedBase(ctx, repository, request.BaseRevision); err != nil {
		return CandidateSnapshot{}, errors.New("prepare fixture candidate: pinned base is unavailable")
	}
	snapshot, err := registry.InspectCandidate(ctx, CandidateSnapshotRequest{
		TaskHandle: request.TaskHandle, RepositoryID: request.RepositoryID, WorktreePath: request.WorktreePath,
	})
	if err != nil || snapshot.Cleanliness != CandidateClean {
		return CandidateSnapshot{}, errors.New("prepare fixture candidate: worktree is not an exact clean candidate")
	}
	contents := []byte("deterministic fixture candidate for " + request.TaskHandle + "\n")
	if snapshot.HeadRevision != request.BaseRevision {
		if err := registry.validateFixtureCandidateReplay(ctx, request, snapshot, contents); err != nil {
			return CandidateSnapshot{}, err
		}
		return snapshot, nil
	}

	target := filepath.Join(request.WorktreePath, request.ArtifactRelativePath)
	artifact, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return CandidateSnapshot{}, errors.New("prepare fixture candidate: artifact creation was refused")
	}
	if _, err := artifact.Write(contents); err != nil {
		_ = artifact.Close()
		return CandidateSnapshot{}, errors.New("prepare fixture candidate: artifact write failed")
	}
	if err := artifact.Close(); err != nil {
		return CandidateSnapshot{}, errors.New("prepare fixture candidate: artifact close failed")
	}
	if _, err := runGitBytes(ctx, registry.gitExecutable,
		"-c", "core.hooksPath=/dev/null", "--no-optional-locks", "-C", request.WorktreePath,
		"add", "--", request.ArtifactRelativePath); err != nil {
		return CandidateSnapshot{}, errors.New("prepare fixture candidate: Git staging failed")
	}
	if _, err := runGitBytes(ctx, registry.gitExecutable,
		"-c", "core.hooksPath=/dev/null", "-c", "user.name=DevCrew Fixture",
		"-c", "user.email=fixture@example.invalid", "--no-optional-locks", "-C", request.WorktreePath,
		"commit", "-m", "deterministic fixture candidate"); err != nil {
		return CandidateSnapshot{}, errors.New("prepare fixture candidate: Git commit failed")
	}
	created, err := registry.InspectCandidate(ctx, CandidateSnapshotRequest{
		TaskHandle: request.TaskHandle, RepositoryID: request.RepositoryID, WorktreePath: request.WorktreePath,
	})
	if err != nil || created.Cleanliness != CandidateClean || created.HeadRevision == request.BaseRevision {
		return CandidateSnapshot{}, errors.New("prepare fixture candidate: committed candidate is unverifiable")
	}
	return created, nil
}

func (registry *Registry) validateFixtureCandidateReplay(
	ctx context.Context,
	request FixtureCandidateRequest,
	snapshot CandidateSnapshot,
	contents []byte,
) error {
	parent, err := runGit(ctx, registry.gitExecutable, "--no-optional-locks", "-C", request.WorktreePath,
		"rev-parse", "--verify", snapshot.HeadRevision+"^")
	if err != nil || parent != request.BaseRevision {
		return errors.New("prepare fixture candidate: existing candidate ancestry differs")
	}
	changed, err := runGitBytes(ctx, registry.gitExecutable, "--no-optional-locks", "-C", request.WorktreePath,
		"diff-tree", "--no-commit-id", "--name-only", "-r", "-z", request.BaseRevision, snapshot.HeadRevision)
	if err != nil || string(changed) != request.ArtifactRelativePath+"\x00" {
		return errors.New("prepare fixture candidate: existing candidate tree differs")
	}
	target := filepath.Join(request.WorktreePath, request.ArtifactRelativePath)
	info, err := os.Lstat(target)
	if err != nil || !info.Mode().IsRegular() {
		return errors.New("prepare fixture candidate: existing artifact is unsafe")
	}
	actual, err := os.ReadFile(target)
	if err != nil || string(actual) != string(contents) {
		return errors.New("prepare fixture candidate: existing artifact differs")
	}
	return nil
}
