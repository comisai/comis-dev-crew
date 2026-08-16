package git

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"sync"
)

var repositoryIDPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{2,63}$`)
var errCandidateWorktreeStructural = errors.New("task worktree structure is unverified")

// Registry is an immutable map from operator-owned IDs to verified repository
// identities. It performs no Git mutation and launches no worker.
type Registry struct {
	gitExecutable string
	repositories  map[string]Repository
	mu            sync.Mutex
}

// NewRegistry validates every configured path and primary Git identity before
// returning any entry.
func NewRegistry(ctx context.Context, config RegistryConfig) (*Registry, error) {
	if ctx == nil {
		return nil, errors.New("create repository registry: context is required")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := validateGitExecutable(config.GitExecutable); err != nil {
		return nil, safePathError("create repository registry", err)
	}
	roots, err := validateApprovedRoots(config.ApprovedRoots)
	if err != nil {
		return nil, err
	}
	if len(config.Repositories) == 0 {
		return nil, errors.New("create repository registry: at least one repository is required")
	}

	registry := &Registry{gitExecutable: config.GitExecutable, repositories: make(map[string]Repository, len(config.Repositories))}
	identities := make(map[string]struct{}, len(config.Repositories))
	for _, configured := range config.Repositories {
		if !repositoryIDPattern.MatchString(configured.ID) {
			return nil, errors.New("create repository registry: repository ID is invalid")
		}
		if _, duplicate := registry.repositories[configured.ID]; duplicate {
			return nil, errors.New("create repository registry: repository ID is duplicated")
		}
		repository, err := inspectConfiguredRepository(ctx, config.GitExecutable, roots, configured)
		if err != nil {
			return nil, err
		}
		if _, duplicate := identities[repository.GitCommonDirIdentity]; duplicate {
			return nil, errors.New("create repository registry: git repository identity is duplicated")
		}
		identities[repository.GitCommonDirIdentity] = struct{}{}
		registry.repositories[repository.ID] = repository
	}
	return registry, nil
}

// Resolve returns one immutable verified repository by opaque ID.
func (registry *Registry) Resolve(repositoryID string) (Repository, error) {
	if registry == nil {
		return Repository{}, errors.New("resolve repository: registry is unavailable")
	}
	repository, ok := registry.repositories[repositoryID]
	if !ok {
		return Repository{}, ErrRepositoryNotFound
	}
	return repository, nil
}

// ValidateRepository implements the application catalog seam without exposing
// configured paths or Git identities outside the adapter.
func (registry *Registry) ValidateRepository(ctx context.Context, repositoryID string) error {
	if ctx == nil {
		return errors.New("validate repository: context is required")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	_, err := registry.Resolve(repositoryID)
	return err
}

// ValidateWorktree proves that a real linked worktree is inside its dedicated
// root and still shares the configured primary's exact common-directory identity.
func (registry *Registry) ValidateWorktree(ctx context.Context, repositoryID, path string) (Worktree, error) {
	if ctx == nil {
		return Worktree{}, errors.New("validate task worktree: context is required")
	}
	if err := ctx.Err(); err != nil {
		return Worktree{}, err
	}
	repository, err := registry.Resolve(repositoryID)
	if err != nil {
		return Worktree{}, fmt.Errorf("validate task worktree: %w", err)
	}
	if err := validateCanonicalDirectory(path); err != nil {
		if errors.Is(err, errFilesystemInfrastructure) {
			return Worktree{}, err
		}
		return Worktree{}, candidateWorktreeStructuralError(safePathError("validate task worktree", err))
	}
	if !pathWithin(repository.WorktreeRoot, path, true) || path == repository.PrimaryCheckout {
		return Worktree{}, candidateWorktreeStructuralError(
			errors.New("validate task worktree: path is outside its dedicated root"),
		)
	}
	if err := validateWorktreeMarker(path); err != nil {
		if errors.Is(err, errFilesystemInfrastructure) {
			return Worktree{}, err
		}
		return Worktree{}, candidateWorktreeStructuralError(safePathError("validate task worktree", err))
	}
	commonDirectory, identity, err := inspectGitWorktree(ctx, registry.gitExecutable, path)
	if err != nil {
		if errors.Is(err, errGitInfrastructure) || errors.Is(err, errFilesystemInfrastructure) ||
			errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return Worktree{}, err
		}
		return Worktree{}, candidateWorktreeStructuralError(err)
	}
	if commonDirectory != repository.GitCommonDir || identity != repository.GitCommonDirIdentity {
		return Worktree{}, candidateWorktreeStructuralError(
			errors.New("validate task worktree: git common-directory identity differs"),
		)
	}
	return Worktree{
		RepositoryID: repository.ID, CanonicalPath: path,
		GitCommonDir: commonDirectory, GitCommonDirIdentity: identity,
	}, nil
}

func candidateWorktreeStructuralError(cause error) error {
	return fmt.Errorf("%w: %w", errCandidateWorktreeStructural, cause)
}

func validateApprovedRoots(configured []string) ([]string, error) {
	if len(configured) == 0 {
		return nil, errors.New("create repository registry: approved roots are required")
	}
	roots := make([]string, 0, len(configured))
	seen := make(map[string]struct{}, len(configured))
	for _, root := range configured {
		if err := validateCanonicalDirectory(root); err != nil {
			return nil, safePathError("create repository registry", err)
		}
		if _, duplicate := seen[root]; duplicate {
			return nil, errors.New("create repository registry: approved root is duplicated")
		}
		seen[root] = struct{}{}
		roots = append(roots, root)
	}
	return roots, nil
}

func inspectConfiguredRepository(ctx context.Context, executable string, roots []string, configured RepositoryConfig) (Repository, error) {
	if err := validateCanonicalDirectory(configured.PrimaryCheckout); err != nil {
		return Repository{}, safePathError("inspect configured repository", err)
	}
	if err := validateCanonicalDirectory(configured.WorktreeRoot); err != nil {
		return Repository{}, safePathError("inspect configured repository", err)
	}
	if !containedByAny(configured.PrimaryCheckout, roots) || !containedByAny(configured.WorktreeRoot, roots) {
		return Repository{}, errors.New("inspect configured repository: path is outside approved roots")
	}
	if pathsOverlap(configured.PrimaryCheckout, configured.WorktreeRoot) {
		return Repository{}, errors.New("inspect configured repository: primary and worktree roots overlap")
	}
	if err := validatePrimaryMarker(configured.PrimaryCheckout); err != nil {
		return Repository{}, safePathError("inspect configured repository", err)
	}
	if !repositoryIDPattern.MatchString(configured.DefaultBranch) {
		return Repository{}, errors.New("inspect configured repository: default branch is invalid")
	}
	branchExists, err := gitPredicate(ctx, executable, "--no-optional-locks", "-C", configured.PrimaryCheckout,
		"show-ref", "--verify", "--quiet", "refs/heads/"+configured.DefaultBranch)
	if err != nil || !branchExists {
		return Repository{}, errors.New("inspect configured repository: default branch is unavailable")
	}
	commonDirectory, identity, err := inspectGitWorktree(ctx, executable, configured.PrimaryCheckout)
	if err != nil {
		return Repository{}, err
	}
	if commonDirectory != filepath.Join(configured.PrimaryCheckout, ".git") {
		return Repository{}, errors.New("inspect configured repository: primary common directory differs")
	}
	return Repository{
		ID: configured.ID, PrimaryCheckout: configured.PrimaryCheckout, WorktreeRoot: configured.WorktreeRoot,
		DefaultBranch: configured.DefaultBranch,
		GitCommonDir:  commonDirectory, GitCommonDirIdentity: identity,
	}, nil
}

func inspectGitWorktree(ctx context.Context, executable, path string) (string, string, error) {
	topLevel, err := runGit(ctx, executable, "--no-optional-locks", "-C", path, "rev-parse", "--show-toplevel")
	if err != nil {
		return "", "", fmt.Errorf("inspect git worktree root: %w", err)
	}
	if topLevel != path {
		return "", "", errors.New("inspect git worktree root: configured path is not the worktree root")
	}
	bare, err := runGit(ctx, executable, "--no-optional-locks", "-C", path, "rev-parse", "--is-bare-repository")
	if err != nil || bare != "false" {
		return "", "", errors.New("inspect git worktree root: bare or unreadable repository")
	}
	commonDirectory, err := runGit(ctx, executable, "--no-optional-locks", "-C", path,
		"rev-parse", "--path-format=absolute", "--git-common-dir")
	if err != nil {
		return "", "", fmt.Errorf("inspect git common directory: %w", err)
	}
	if err := validateCanonicalDirectory(commonDirectory); err != nil {
		return "", "", safePathError("inspect git common directory", err)
	}
	identity, err := commonDirectoryIdentity(commonDirectory)
	if err != nil {
		return "", "", errors.New("inspect git common directory: identity unavailable")
	}
	return commonDirectory, identity, nil
}
