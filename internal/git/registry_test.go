package git_test

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	devgit "github.com/comisai/comis-dev-crew/internal/git"
)

func TestRegistry_ResolvesConfiguredPrimaryAndValidatesRealWorktreeIdentity(t *testing.T) {
	fixture := newRepositoryFixture(t, "product-api")
	registry, err := devgit.NewRegistry(context.Background(), devgit.RegistryConfig{
		GitExecutable: fixture.gitExecutable,
		ApprovedRoots: []string{fixture.approvedRoot},
		Repositories: []devgit.RepositoryConfig{{
			ID: fixture.repositoryID, PrimaryCheckout: fixture.primary, WorktreeRoot: fixture.worktreeRoot,
		}},
	})
	if err != nil {
		t.Fatalf("NewRegistry() error = %v", err)
	}

	repository, err := registry.Resolve(fixture.repositoryID)
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if repository.ID != fixture.repositoryID || repository.PrimaryCheckout != fixture.primary || repository.WorktreeRoot != fixture.worktreeRoot {
		t.Fatalf("Resolve() = %#v, want configured canonical repository", repository)
	}
	if len(repository.GitCommonDirIdentity) != 64 || repository.GitCommonDir == "" {
		t.Fatalf("repository common-directory identity is not pinned: %#v", repository)
	}

	worktreePath := filepath.Join(fixture.worktreeRoot, "task-0001")
	runGit(t, fixture.gitExecutable, "--no-optional-locks", "-C", fixture.primary, "worktree", "add", "--detach", worktreePath, "HEAD")
	worktree, err := registry.ValidateWorktree(context.Background(), fixture.repositoryID, worktreePath)
	if err != nil {
		t.Fatalf("ValidateWorktree() error = %v", err)
	}
	if worktree.CanonicalPath != worktreePath || worktree.GitCommonDirIdentity != repository.GitCommonDirIdentity {
		t.Fatalf("ValidateWorktree() = %#v, want matching canonical worktree identity", worktree)
	}
	if worktree.CanonicalPath == repository.PrimaryCheckout {
		t.Fatal("task worktree must remain distinct from the primary checkout")
	}
}

func TestRegistry_RejectsUnsafeConfiguredRootsAndPrimaryCheckouts(t *testing.T) {
	fixture := newRepositoryFixture(t, "product-api")
	symlinkPrimary := filepath.Join(fixture.approvedRoot, "linked-primary")
	if err := os.Symlink(fixture.primary, symlinkPrimary); err != nil {
		t.Fatalf("create primary symlink: %v", err)
	}
	symlinkWorktreeRoot := filepath.Join(fixture.approvedRoot, "linked-worktrees")
	if err := os.Symlink(fixture.worktreeRoot, symlinkWorktreeRoot); err != nil {
		t.Fatalf("create worktree-root symlink: %v", err)
	}
	subdirectory := filepath.Join(fixture.primary, "nested")
	if err := os.Mkdir(subdirectory, 0o700); err != nil {
		t.Fatalf("create repository subdirectory: %v", err)
	}
	outsideRoot := canonicalTempDir(t)

	tests := []struct {
		name   string
		config devgit.RegistryConfig
	}{
		{name: "relative git executable", config: fixture.config(func(config *devgit.RegistryConfig) { config.GitExecutable = "git" })},
		{name: "relative primary", config: fixture.config(func(config *devgit.RegistryConfig) { config.Repositories[0].PrimaryCheckout = "relative/repo" })},
		{name: "noncanonical primary", config: fixture.config(func(config *devgit.RegistryConfig) {
			separator := string(filepath.Separator)
			config.Repositories[0].PrimaryCheckout = fixture.approvedRoot + separator + "nested" + separator + ".." + separator + filepath.Base(fixture.primary)
		})},
		{name: "symlink primary", config: fixture.config(func(config *devgit.RegistryConfig) { config.Repositories[0].PrimaryCheckout = symlinkPrimary })},
		{name: "primary subdirectory", config: fixture.config(func(config *devgit.RegistryConfig) { config.Repositories[0].PrimaryCheckout = subdirectory })},
		{name: "worktree root outside approved root", config: fixture.config(func(config *devgit.RegistryConfig) { config.Repositories[0].WorktreeRoot = outsideRoot })},
		{name: "symlink worktree root", config: fixture.config(func(config *devgit.RegistryConfig) { config.Repositories[0].WorktreeRoot = symlinkWorktreeRoot })},
		{name: "primary used as worktree root", config: fixture.config(func(config *devgit.RegistryConfig) { config.Repositories[0].WorktreeRoot = fixture.primary })},
		{name: "duplicate repository id", config: fixture.config(func(config *devgit.RegistryConfig) {
			config.Repositories = append(config.Repositories, config.Repositories[0])
		})},
		{name: "duplicate repository identity", config: fixture.config(func(config *devgit.RegistryConfig) {
			duplicate := config.Repositories[0]
			duplicate.ID = "product-copy"
			config.Repositories = append(config.Repositories, duplicate)
		})},
		{name: "invalid repository id", config: fixture.config(func(config *devgit.RegistryConfig) { config.Repositories[0].ID = "owner/repository" })},
		{name: "no repositories", config: fixture.config(func(config *devgit.RegistryConfig) { config.Repositories = nil })},
		{name: "duplicate approved root", config: fixture.config(func(config *devgit.RegistryConfig) {
			config.ApprovedRoots = append(config.ApprovedRoots, config.ApprovedRoots[0])
		})},
		{name: "no approved roots", config: fixture.config(func(config *devgit.RegistryConfig) { config.ApprovedRoots = nil })},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := devgit.NewRegistry(context.Background(), test.config)
			if err == nil {
				t.Fatal("NewRegistry() error = nil, want unsafe configuration rejection")
			}
			if strings.Contains(err.Error(), fixture.primary) || strings.Contains(err.Error(), outsideRoot) {
				t.Fatalf("safe registry error leaked configured path: %q", err)
			}
		})
	}
}

func TestRegistry_RejectsWrongRepositoryAndWorktreeEscapes(t *testing.T) {
	fixture := newRepositoryFixture(t, "product-api")
	registry, err := devgit.NewRegistry(context.Background(), fixture.config(nil))
	if err != nil {
		t.Fatalf("NewRegistry() error = %v", err)
	}

	validWorktree := filepath.Join(fixture.worktreeRoot, "task-valid")
	runGit(t, fixture.gitExecutable, "--no-optional-locks", "-C", fixture.primary, "worktree", "add", "--detach", validWorktree, "HEAD")
	linkedWorktree := filepath.Join(fixture.worktreeRoot, "task-linked")
	if err := os.Symlink(validWorktree, linkedWorktree); err != nil {
		t.Fatalf("create worktree symlink: %v", err)
	}
	siblingEscape := filepath.Join(filepath.Dir(fixture.worktreeRoot), filepath.Base(fixture.worktreeRoot)+"-escape")
	if err := os.Mkdir(siblingEscape, 0o700); err != nil {
		t.Fatalf("create sibling escape: %v", err)
	}

	other := newRepositoryFixtureUnder(t, fixture.approvedRoot, "other-api", fixture.gitExecutable)
	wrongRepositoryWorktree := filepath.Join(fixture.worktreeRoot, "task-wrong-repository")
	runGit(t, fixture.gitExecutable, "--no-optional-locks", "-C", other.primary, "worktree", "add", "--detach", wrongRepositoryWorktree, "HEAD")

	tests := []struct {
		name string
		id   string
		path string
	}{
		{name: "unknown repository id", id: "missing-repository", path: validWorktree},
		{name: "primary is not a task worktree", id: fixture.repositoryID, path: fixture.primary},
		{name: "worktree root is not a task worktree", id: fixture.repositoryID, path: fixture.worktreeRoot},
		{name: "symlink worktree", id: fixture.repositoryID, path: linkedWorktree},
		{name: "sibling prefix escape", id: fixture.repositoryID, path: siblingEscape},
		{name: "wrong git common directory", id: fixture.repositoryID, path: wrongRepositoryWorktree},
		{name: "missing target", id: fixture.repositoryID, path: filepath.Join(fixture.worktreeRoot, "missing")},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := registry.ValidateWorktree(context.Background(), test.id, test.path)
			if err == nil {
				t.Fatal("ValidateWorktree() error = nil, want fail-closed rejection")
			}
			if strings.Contains(err.Error(), test.path) {
				t.Fatalf("safe worktree error leaked target path: %q", err)
			}
		})
	}
}

func TestRegistry_HonorsCancellationAndReturnsTypedMissingRepository(t *testing.T) {
	fixture := newRepositoryFixture(t, "product-api")
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := devgit.NewRegistry(cancelled, fixture.config(nil)); !errors.Is(err, context.Canceled) {
		t.Fatalf("NewRegistry(cancelled) error = %v, want context.Canceled", err)
	}

	registry, err := devgit.NewRegistry(context.Background(), fixture.config(nil))
	if err != nil {
		t.Fatalf("NewRegistry() error = %v", err)
	}
	if _, err := registry.Resolve("missing-repository"); !errors.Is(err, devgit.ErrRepositoryNotFound) {
		t.Fatalf("Resolve(missing) error = %v, want ErrRepositoryNotFound", err)
	}
	if _, err := registry.ValidateWorktree(cancelled, fixture.repositoryID, fixture.worktreeRoot); !errors.Is(err, context.Canceled) {
		t.Fatalf("ValidateWorktree(cancelled) error = %v, want context.Canceled", err)
	}
	//lint:ignore SA1012 The boundary test proves nil contexts fail before path or Git inspection.
	if _, err := registry.ValidateWorktree(nil, fixture.repositoryID, fixture.worktreeRoot); err == nil {
		t.Fatal("ValidateWorktree(nil context) error = nil")
	}
}

type repositoryFixture struct {
	gitExecutable string
	approvedRoot  string
	repositoryID  string
	primary       string
	worktreeRoot  string
}

func newRepositoryFixture(t *testing.T, repositoryID string) repositoryFixture {
	t.Helper()
	gitExecutable, err := exec.LookPath("git")
	if err != nil {
		t.Fatalf("find Git executable: %v", err)
	}
	gitExecutable, err = filepath.EvalSymlinks(gitExecutable)
	if err != nil {
		t.Fatalf("canonicalize Git executable: %v", err)
	}
	return newRepositoryFixtureUnder(t, canonicalTempDir(t), repositoryID, gitExecutable)
}

func newRepositoryFixtureUnder(t *testing.T, approvedRoot, repositoryID, gitExecutable string) repositoryFixture {
	t.Helper()
	primary := filepath.Join(approvedRoot, repositoryID+"-primary")
	worktreeRoot := filepath.Join(approvedRoot, repositoryID+"-worktrees")
	if err := os.Mkdir(worktreeRoot, 0o700); err != nil {
		t.Fatalf("create worktree root: %v", err)
	}
	runGit(t, gitExecutable, "init", "--initial-branch=main", primary)
	if err := os.WriteFile(filepath.Join(primary, "fixture.txt"), []byte("fixture\n"), 0o600); err != nil {
		t.Fatalf("write repository fixture: %v", err)
	}
	runGit(t, gitExecutable, "--no-optional-locks", "-C", primary, "add", "fixture.txt")
	runGit(t, gitExecutable, "--no-optional-locks", "-C", primary,
		"-c", "user.name=DevCrew Fixture", "-c", "user.email=fixture@example.invalid",
		"commit", "-m", "fixture")
	return repositoryFixture{
		gitExecutable: gitExecutable,
		approvedRoot:  approvedRoot,
		repositoryID:  repositoryID,
		primary:       primary,
		worktreeRoot:  worktreeRoot,
	}
}

func (fixture repositoryFixture) config(mutate func(*devgit.RegistryConfig)) devgit.RegistryConfig {
	config := devgit.RegistryConfig{
		GitExecutable: fixture.gitExecutable,
		ApprovedRoots: []string{fixture.approvedRoot},
		Repositories: []devgit.RepositoryConfig{{
			ID: fixture.repositoryID, PrimaryCheckout: fixture.primary, WorktreeRoot: fixture.worktreeRoot,
		}},
	}
	if mutate != nil {
		mutate(&config)
	}
	return config
}

func canonicalTempDir(t *testing.T) string {
	t.Helper()
	canonical, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("canonicalize temporary directory: %v", err)
	}
	return canonical
}

func runGit(t *testing.T, executable string, arguments ...string) {
	t.Helper()
	command := exec.Command(executable, arguments...)
	command.Env = []string{"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1", "GIT_OPTIONAL_LOCKS=0", "LC_ALL=C"}
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("Git fixture command failed: %v: %s", err, output)
	}
}
