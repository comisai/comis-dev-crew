package git

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

var gitRevisionPattern = regexp.MustCompile(`^(?:[0-9a-f]{40}|[0-9a-f]{64})$`)

type worktreeListEntry struct {
	path     string
	head     string
	branch   string
	locked   bool
	prunable bool
}

// PrepareWorktree creates or exactly adopts one clean task branch rooted at
// the pinned base. The stable operation determines the collision suffix, while
// the task handle determines the ratified directory layout.
func (registry *Registry) PrepareWorktree(ctx context.Context, request PrepareWorktreeRequest) (PreparedWorktree, error) {
	if registry == nil {
		return PreparedWorktree{}, errors.New("prepare task worktree: registry is unavailable")
	}
	if ctx == nil {
		return PreparedWorktree{}, errors.New("prepare task worktree: context is required")
	}
	if err := ctx.Err(); err != nil {
		return PreparedWorktree{}, err
	}
	if !repositoryIDPattern.MatchString(request.OperationID) || !repositoryIDPattern.MatchString(request.TaskHandle) ||
		!repositoryIDPattern.MatchString(request.RepositoryID) || !gitRevisionPattern.MatchString(request.BaseRevision) {
		return PreparedWorktree{}, errors.New("prepare task worktree: request identity is invalid")
	}

	registry.mu.Lock()
	defer registry.mu.Unlock()
	repository, err := registry.Resolve(request.RepositoryID)
	if err != nil {
		return PreparedWorktree{}, fmt.Errorf("prepare task worktree: %w", err)
	}
	if err := registry.validatePinnedBase(ctx, repository, request.BaseRevision); err != nil {
		return PreparedWorktree{}, err
	}
	target := filepath.Join(repository.WorktreeRoot, request.TaskHandle)
	if err := validatePreparedTarget(repository, target, request.LiveLeasePaths); err != nil {
		return PreparedWorktree{}, err
	}
	branch, operationSuffix := preparedBranch(request.RepositoryID, request.TaskHandle, request.OperationID)
	if err := registry.validateOperationBranch(ctx, repository, branch, operationSuffix); err != nil {
		return PreparedWorktree{}, err
	}

	info, statErr := os.Lstat(target)
	switch {
	case statErr == nil:
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return PreparedWorktree{}, errors.New("prepare task worktree: existing target is unsafe")
		}
		return registry.adoptPreparedWorktree(ctx, repository, request, target, branch)
	case !os.IsNotExist(statErr):
		return PreparedWorktree{}, errors.New("prepare task worktree: target cannot be inspected")
	}

	branchExists, err := registry.branchExists(ctx, repository, branch)
	if err != nil {
		return PreparedWorktree{}, err
	}
	if branchExists {
		return PreparedWorktree{}, errors.New("prepare task worktree: deterministic branch already exists without its target")
	}
	if _, err := runGitBytes(ctx, registry.gitExecutable,
		"-c", "core.hooksPath=/dev/null", "--no-optional-locks", "-C", repository.PrimaryCheckout,
		"worktree", "add", "-b", branch, "--", target, request.BaseRevision); err != nil {
		return PreparedWorktree{}, errors.New("prepare task worktree: Git creation failed; preserve any partial target")
	}
	return registry.adoptPreparedWorktree(ctx, repository, request, target, branch)
}

// CleanupWorktree removes only the exact clean operation-bound branch still at
// its pinned base. Dirty, changed, locked, escaped, or otherwise ambiguous
// targets are preserved without force.
func (registry *Registry) CleanupWorktree(ctx context.Context, request CleanupWorktreeRequest) error {
	if registry == nil {
		return errors.New("cleanup task worktree: registry is unavailable")
	}
	if ctx == nil {
		return errors.New("cleanup task worktree: context is required")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if !repositoryIDPattern.MatchString(request.OperationID) || !repositoryIDPattern.MatchString(request.TaskHandle) ||
		!repositoryIDPattern.MatchString(request.RepositoryID) || !gitRevisionPattern.MatchString(request.BaseRevision) {
		return errors.New("cleanup task worktree: request identity is invalid")
	}

	registry.mu.Lock()
	defer registry.mu.Unlock()
	repository, err := registry.Resolve(request.RepositoryID)
	if err != nil {
		return fmt.Errorf("cleanup task worktree: %w", err)
	}
	target := filepath.Join(repository.WorktreeRoot, request.TaskHandle)
	if err := validatePreparedTarget(repository, target, request.LiveLeasePaths); err != nil {
		return errors.New("cleanup task worktree: target conflicts with repository or live lease authority")
	}
	if err := registry.validatePinnedBase(ctx, repository, request.BaseRevision); err != nil {
		return errors.New("cleanup task worktree: pinned base is no longer verifiable")
	}
	branch, operationSuffix := preparedBranch(request.RepositoryID, request.TaskHandle, request.OperationID)
	if err := registry.validateOperationBranch(ctx, repository, branch, operationSuffix); err != nil {
		return errors.New("cleanup task worktree: operation branch is ambiguous")
	}
	prepared, err := registry.adoptPreparedWorktree(ctx, repository, PrepareWorktreeRequest{
		OperationID: request.OperationID, TaskHandle: request.TaskHandle,
		RepositoryID: request.RepositoryID, BaseRevision: request.BaseRevision,
	}, target, branch)
	if err != nil || prepared.HeadRevision != request.BaseRevision {
		return errors.New("cleanup task worktree: changed or ambiguous worktree is preserved")
	}
	if _, err := runGitBytes(ctx, registry.gitExecutable,
		"-c", "core.hooksPath=/dev/null", "--no-optional-locks", "-C", repository.PrimaryCheckout,
		"worktree", "remove", "--", target); err != nil {
		return errors.New("cleanup task worktree: Git removal refused")
	}
	if _, err := runGitBytes(ctx, registry.gitExecutable,
		"-c", "core.hooksPath=/dev/null", "--no-optional-locks", "-C", repository.PrimaryCheckout,
		"branch", "-d", "--", branch); err != nil {
		return errors.New("cleanup task worktree: branch removal refused after worktree removal")
	}
	return nil
}

// RemoveDeliveredWorktree removes only the exact clean task worktree and
// operation-bound branch whose delivered head was independently authorized.
// A replay converges after either or both Git resources have been removed.
func (registry *Registry) RemoveDeliveredWorktree(ctx context.Context, request DeliveredWorktreeCleanupRequest) error {
	if registry == nil {
		return errors.New("remove delivered worktree: registry is unavailable")
	}
	if ctx == nil {
		return errors.New("remove delivered worktree: context is required")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if !repositoryIDPattern.MatchString(request.PreparationOperationID) ||
		!repositoryIDPattern.MatchString(request.TaskHandle) ||
		!repositoryIDPattern.MatchString(request.RepositoryID) ||
		!gitRevisionPattern.MatchString(request.HeadRevision) {
		return errors.New("remove delivered worktree: request identity is invalid")
	}

	registry.mu.Lock()
	defer registry.mu.Unlock()
	repository, err := registry.Resolve(request.RepositoryID)
	if err != nil {
		return errors.New("remove delivered worktree: repository is unavailable")
	}
	target := filepath.Join(repository.WorktreeRoot, request.TaskHandle)
	if request.WorktreePath != target || validatePreparedTarget(repository, target, nil) != nil {
		return errors.New("remove delivered worktree: target does not match the task root")
	}
	branch, operationSuffix := preparedBranch(
		request.RepositoryID,
		request.TaskHandle,
		request.PreparationOperationID,
	)
	if request.Branch != branch {
		return errors.New("remove delivered worktree: branch does not match its preparation")
	}
	if err := registry.validateOperationBranch(ctx, repository, branch, operationSuffix); err != nil {
		return errors.New("remove delivered worktree: operation branch is ambiguous")
	}

	entries, err := registry.worktreeEntries(ctx, repository)
	if err != nil {
		return err
	}
	entry, listed := findWorktreeEntry(entries, target)
	info, statErr := os.Lstat(target)
	switch {
	case statErr == nil:
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return errors.New("remove delivered worktree: target is unsafe")
		}
		if _, err := registry.ValidateWorktree(ctx, request.RepositoryID, target); err != nil {
			return errors.New("remove delivered worktree: worktree identity is invalid")
		}
		if !listed || entry.locked || entry.prunable || entry.branch != branch || entry.head != request.HeadRevision {
			return errors.New("remove delivered worktree: worktree inventory differs")
		}
		currentBranch, err := runGit(ctx, registry.gitExecutable, "--no-optional-locks", "-C", target,
			"symbolic-ref", "--quiet", "--short", "HEAD")
		if err != nil || currentBranch != branch {
			return errors.New("remove delivered worktree: branch identity differs")
		}
		head, err := runGit(ctx, registry.gitExecutable, "--no-optional-locks", "-C", target,
			"rev-parse", "--verify", "HEAD^{commit}")
		if err != nil || head != request.HeadRevision {
			return errors.New("remove delivered worktree: head identity differs")
		}
		status, err := runGitBytes(ctx, registry.gitExecutable, "--no-optional-locks", "-C", target,
			"status", "--porcelain=v2", "-z", "--untracked-files=all")
		if err != nil || len(status) != 0 {
			return errors.New("remove delivered worktree: dirty or untracked work is preserved")
		}
		if _, err := runGitBytes(ctx, registry.gitExecutable,
			"-c", "core.hooksPath=/dev/null", "--no-optional-locks", "-C", repository.PrimaryCheckout,
			"worktree", "remove", "--", target); err != nil {
			return errors.New("remove delivered worktree: Git removal refused")
		}
	case os.IsNotExist(statErr):
		if listed {
			return errors.New("remove delivered worktree: absent target remains in worktree inventory")
		}
	default:
		return errors.New("remove delivered worktree: target cannot be inspected")
	}

	branchExists, err := registry.branchExists(ctx, repository, branch)
	if err != nil {
		return err
	}
	if !branchExists {
		return nil
	}
	branchHead, err := runGit(ctx, registry.gitExecutable, "--no-optional-locks", "-C", repository.PrimaryCheckout,
		"rev-parse", "--verify", "refs/heads/"+branch+"^{commit}")
	if err != nil || branchHead != request.HeadRevision {
		return errors.New("remove delivered worktree: branch head differs")
	}
	if _, err := runGitBytes(ctx, registry.gitExecutable,
		"-c", "core.hooksPath=/dev/null", "--no-optional-locks", "-C", repository.PrimaryCheckout,
		"update-ref", "-d", "refs/heads/"+branch, request.HeadRevision); err != nil {
		return errors.New("remove delivered worktree: exact branch removal refused")
	}
	return nil
}

func (registry *Registry) validatePinnedBase(ctx context.Context, repository Repository, baseRevision string) error {
	resolved, err := runGit(ctx, registry.gitExecutable, "--no-optional-locks", "-C", repository.PrimaryCheckout,
		"rev-parse", "--verify", baseRevision+"^{commit}")
	if err != nil || resolved != baseRevision {
		return errors.New("prepare task worktree: base commit is unavailable or differs")
	}
	reachable, err := gitPredicate(ctx, registry.gitExecutable, "--no-optional-locks", "-C", repository.PrimaryCheckout,
		"merge-base", "--is-ancestor", baseRevision, "refs/heads/"+repository.DefaultBranch)
	if err != nil || !reachable {
		return errors.New("prepare task worktree: base commit is outside the configured default-branch history")
	}
	return nil
}

func (registry *Registry) adoptPreparedWorktree(
	ctx context.Context,
	repository Repository,
	request PrepareWorktreeRequest,
	target string,
	branch string,
) (PreparedWorktree, error) {
	validated, err := registry.ValidateWorktree(ctx, request.RepositoryID, target)
	if err != nil {
		return PreparedWorktree{}, errors.New("prepare task worktree: existing target is not the configured repository worktree")
	}
	entries, err := registry.worktreeEntries(ctx, repository)
	if err != nil {
		return PreparedWorktree{}, err
	}
	entry, found := findWorktreeEntry(entries, target)
	if !found || entry.locked || entry.prunable || entry.branch != branch || entry.head != request.BaseRevision {
		return PreparedWorktree{}, errors.New("prepare task worktree: adoption evidence differs or is ambiguous")
	}
	currentBranch, err := runGit(ctx, registry.gitExecutable, "--no-optional-locks", "-C", target,
		"symbolic-ref", "--quiet", "--short", "HEAD")
	if err != nil || currentBranch != branch {
		return PreparedWorktree{}, errors.New("prepare task worktree: task branch identity differs")
	}
	head, err := runGit(ctx, registry.gitExecutable, "--no-optional-locks", "-C", target,
		"rev-parse", "--verify", "HEAD^{commit}")
	if err != nil || head != request.BaseRevision {
		return PreparedWorktree{}, errors.New("prepare task worktree: task head differs from pinned base")
	}
	status, err := runGitBytes(ctx, registry.gitExecutable, "--no-optional-locks", "-C", target,
		"status", "--porcelain=v2", "-z", "--untracked-files=all")
	if err != nil || len(status) != 0 {
		return PreparedWorktree{}, errors.New("prepare task worktree: dirty or untracked work is preserved")
	}
	return PreparedWorktree{
		OperationID: request.OperationID, TaskHandle: request.TaskHandle,
		RepositoryID: request.RepositoryID, CanonicalPath: target, Branch: branch,
		BaseRevision: request.BaseRevision, HeadRevision: head,
		GitCommonDir: validated.GitCommonDir, GitCommonDirIdentity: validated.GitCommonDirIdentity,
	}, nil
}

func validatePreparedTarget(repository Repository, target string, liveLeasePaths []string) error {
	if !pathWithin(repository.WorktreeRoot, target, true) || target == repository.PrimaryCheckout ||
		filepath.Dir(target) != repository.WorktreeRoot {
		return errors.New("task worktree target is outside its repository root")
	}
	if err := validateCanonicalPathText(target); err != nil {
		return errors.New("task worktree target text is invalid")
	}
	for _, leasePath := range liveLeasePaths {
		if err := validateCanonicalPathText(leasePath); err != nil {
			return errors.New("live lease path is invalid")
		}
		if pathsOverlap(target, leasePath) {
			return errors.New("task worktree target conflicts with a live lease")
		}
	}
	return nil
}

func preparedBranch(repositoryID, taskHandle, operationID string) (string, string) {
	slug := taskHandle
	if len(slug) > 48 {
		slug = strings.TrimRight(slug[:48], "-")
	}
	digest := fmt.Sprintf("%x", sha256.Sum256([]byte(repositoryID+"\x00"+operationID)))[:24]
	return "devcrew/" + slug + "-" + digest, digest
}

func (registry *Registry) validateOperationBranch(
	ctx context.Context,
	repository Repository,
	expectedBranch string,
	operationSuffix string,
) error {
	branches, err := runGitBytes(ctx, registry.gitExecutable, "--no-optional-locks", "-C", repository.PrimaryCheckout,
		"for-each-ref", "--format=%(refname:short)", "refs/heads/devcrew/")
	if err != nil {
		return errors.New("inspect operation-bound task branches: Git query failed")
	}
	for _, encoded := range bytes.Split(branches, []byte{'\n'}) {
		branch := strings.TrimSpace(string(encoded))
		if branch == "" {
			continue
		}
		if strings.HasSuffix(branch, "-"+operationSuffix) && branch != expectedBranch {
			return errors.New("prepare task worktree: operation identity already owns another task branch")
		}
	}
	return nil
}

func (registry *Registry) branchExists(ctx context.Context, repository Repository, branch string) (bool, error) {
	exists, err := gitPredicate(ctx, registry.gitExecutable, "--no-optional-locks", "-C", repository.PrimaryCheckout,
		"show-ref", "--verify", "--quiet", "refs/heads/"+branch)
	if err != nil {
		return false, errors.New("inspect deterministic task branch: Git query failed")
	}
	return exists, nil
}

func (registry *Registry) worktreeEntries(ctx context.Context, repository Repository) ([]worktreeListEntry, error) {
	encoded, err := runGitBytes(ctx, registry.gitExecutable, "--no-optional-locks", "-C", repository.PrimaryCheckout,
		"worktree", "list", "--porcelain", "-z")
	if err != nil {
		return nil, errors.New("inspect task worktree inventory: Git query failed")
	}
	entries, err := decodeWorktreeList(encoded)
	if err != nil {
		return nil, errors.New("inspect task worktree inventory: machine output is invalid")
	}
	return entries, nil
}

func decodeWorktreeList(encoded []byte) ([]worktreeListEntry, error) {
	var entries []worktreeListEntry
	var current worktreeListEntry
	for _, field := range bytes.Split(encoded, []byte{0}) {
		if len(field) == 0 {
			if current.path != "" {
				entries = append(entries, current)
				current = worktreeListEntry{}
			}
			continue
		}
		name, value, found := strings.Cut(string(field), " ")
		if !found {
			name = string(field)
		}
		switch name {
		case "worktree":
			if current.path != "" || value == "" {
				return nil, errors.New("duplicate worktree record")
			}
			current.path = value
		case "HEAD":
			current.head = value
		case "branch":
			current.branch = strings.TrimPrefix(value, "refs/heads/")
		case "locked":
			current.locked = true
		case "prunable":
			current.prunable = true
		case "detached", "bare":
		default:
			return nil, errors.New("unknown worktree inventory field")
		}
	}
	if current.path != "" {
		entries = append(entries, current)
	}
	for _, entry := range entries {
		if entry.path == "" || entry.head == "" {
			return nil, errors.New("incomplete worktree inventory record")
		}
	}
	return entries, nil
}

func findWorktreeEntry(entries []worktreeListEntry, target string) (worktreeListEntry, bool) {
	for _, entry := range entries {
		if entry.path == target {
			return entry, true
		}
	}
	return worktreeListEntry{}, false
}
