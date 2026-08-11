package git_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"sync"
	"testing"

	"github.com/comisai/comis-dev-crew/internal/application"
	devgit "github.com/comisai/comis-dev-crew/internal/git"
)

var preparedBranchPattern = regexp.MustCompile(`^devcrew/[a-z0-9][a-z0-9-]{2,47}-[a-f0-9]{24}$`)

func TestRegistry_PrepareWorktreeCreatesAndAdoptsOneOperationBoundWorkspace(t *testing.T) {
	fixture := newRepositoryFixture(t, "product-api")
	registry := newLifecycleRegistry(t, fixture)
	baseRevision := gitOutput(t, fixture.gitExecutable, "--no-optional-locks", "-C", fixture.primary, "rev-parse", "HEAD")
	request := devgit.PrepareWorktreeRequest{
		OperationID: "prepare-worktree-0001", TaskHandle: "task-worktree-0001",
		RepositoryID: fixture.repositoryID, BaseRevision: baseRevision,
	}
	created, err := registry.PrepareWorktree(context.Background(), request)
	if err != nil {
		t.Fatalf("PrepareWorktree(create) error = %v", err)
	}
	wantPath := filepath.Join(fixture.worktreeRoot, request.TaskHandle)
	if created.OperationID != request.OperationID || created.TaskHandle != request.TaskHandle ||
		created.RepositoryID != fixture.repositoryID || created.CanonicalPath != wantPath ||
		created.BaseRevision != baseRevision || created.HeadRevision != baseRevision ||
		created.GitCommonDirIdentity == "" || !preparedBranchPattern.MatchString(created.Branch) ||
		len(created.Branch) > 96 {
		t.Fatalf("PrepareWorktree(create) = %#v", created)
	}
	if branch := gitOutput(t, fixture.gitExecutable, "--no-optional-locks", "-C", wantPath, "branch", "--show-current"); branch != created.Branch {
		t.Fatalf("worktree branch = %q, want %q", branch, created.Branch)
	}
	adopted, err := registry.PrepareWorktree(context.Background(), request)
	if err != nil {
		t.Fatalf("PrepareWorktree(adopt) error = %v", err)
	}
	if !reflect.DeepEqual(adopted, created) {
		t.Fatalf("idempotent adoption differs: created=%#v adopted=%#v", created, adopted)
	}
	throughPort, err := registry.PrepareWorkspace(context.Background(), application.WorkspacePreparationRequest{
		OperationID: request.OperationID, TaskHandle: request.TaskHandle,
		RepositoryID: request.RepositoryID, BaseRevision: request.BaseRevision,
	})
	if err != nil || throughPort.CanonicalRoot != created.CanonicalPath {
		t.Fatalf("PrepareWorkspace(port) = %#v, %v", throughPort, err)
	}
}

func TestRegistry_PrepareWorktreeCreatesConcurrentDistinctRootsFromOnePinnedBase(t *testing.T) {
	fixture := newRepositoryFixture(t, "product-api")
	registry := newLifecycleRegistry(t, fixture)
	baseRevision := gitOutput(t, fixture.gitExecutable, "--no-optional-locks", "-C", fixture.primary, "rev-parse", "HEAD")
	requests := []devgit.PrepareWorktreeRequest{
		{OperationID: "prepare-concurrent-0001", TaskHandle: "task-concurrent-0001", RepositoryID: fixture.repositoryID, BaseRevision: baseRevision},
		{OperationID: "prepare-concurrent-0002", TaskHandle: "task-concurrent-0002", RepositoryID: fixture.repositoryID, BaseRevision: baseRevision},
	}
	results := make([]devgit.PreparedWorktree, len(requests))
	errorsByIndex := make([]error, len(requests))
	var workers sync.WaitGroup
	for index := range requests {
		workers.Add(1)
		go func(index int) {
			defer workers.Done()
			results[index], errorsByIndex[index] = registry.PrepareWorktree(context.Background(), requests[index])
		}(index)
	}
	workers.Wait()
	for index, err := range errorsByIndex {
		if err != nil {
			t.Fatalf("PrepareWorktree(%d) error = %v", index, err)
		}
	}
	if results[0].CanonicalPath == results[1].CanonicalPath || results[0].Branch == results[1].Branch ||
		results[0].HeadRevision != baseRevision || results[1].HeadRevision != baseRevision {
		t.Fatalf("concurrent worktrees = %#v / %#v", results[0], results[1])
	}
}

func TestRegistry_InspectCandidateReturnsExactHeadBranchAndCleanliness(t *testing.T) {
	fixture := newRepositoryFixture(t, "product-api")
	registry := newLifecycleRegistry(t, fixture)
	request := lifecycleRequest(t, fixture, "prepare-candidate", "task-candidate")
	prepared, err := registry.PrepareWorktree(context.Background(), request)
	if err != nil {
		t.Fatalf("PrepareWorktree() error = %v", err)
	}
	snapshot, err := registry.InspectCandidate(context.Background(), devgit.CandidateSnapshotRequest{
		TaskHandle: request.TaskHandle, RepositoryID: request.RepositoryID, WorktreePath: prepared.CanonicalPath,
	})
	if err != nil {
		t.Fatalf("InspectCandidate() error = %v", err)
	}
	if snapshot.RepositoryID != request.RepositoryID || snapshot.WorktreePath != prepared.CanonicalPath ||
		snapshot.Branch != prepared.Branch || snapshot.HeadRevision != prepared.HeadRevision ||
		snapshot.Cleanliness != devgit.CandidateClean {
		t.Fatalf("InspectCandidate() = %#v", snapshot)
	}
	if err := os.WriteFile(filepath.Join(prepared.CanonicalPath, "candidate.txt"), []byte("candidate\n"), 0o600); err != nil {
		t.Fatalf("write candidate: %v", err)
	}
	dirty, err := registry.InspectCandidate(context.Background(), devgit.CandidateSnapshotRequest{
		TaskHandle: request.TaskHandle, RepositoryID: request.RepositoryID, WorktreePath: prepared.CanonicalPath,
	})
	if err != nil || dirty.Cleanliness != devgit.CandidateDirty {
		t.Fatalf("InspectCandidate(dirty) = %#v, %v", dirty, err)
	}
	if _, err := registry.InspectCandidate(context.Background(), devgit.CandidateSnapshotRequest{
		TaskHandle: "task-other", RepositoryID: request.RepositoryID, WorktreePath: prepared.CanonicalPath,
	}); err == nil {
		t.Fatal("InspectCandidate(cross task) error = nil")
	}
}

func TestRegistry_PrepareWorktreeRefusesUnsafeBaseTargetsAndAdoption(t *testing.T) {
	t.Run("base exists but is unreachable from configured default", func(t *testing.T) {
		fixture := newRepositoryFixture(t, "product-api")
		registry := newLifecycleRegistry(t, fixture)
		tree := gitOutput(t, fixture.gitExecutable, "--no-optional-locks", "-C", fixture.primary, "rev-parse", "HEAD^{tree}")
		orphan := gitOutputWithEnvironment(t, fixture.gitExecutable, []string{
			"GIT_AUTHOR_NAME=DevCrew Fixture", "GIT_AUTHOR_EMAIL=fixture@example.invalid",
			"GIT_COMMITTER_NAME=DevCrew Fixture", "GIT_COMMITTER_EMAIL=fixture@example.invalid",
		}, "--no-optional-locks", "-C", fixture.primary, "commit-tree", tree, "-m", "unreachable")
		_, err := registry.PrepareWorktree(context.Background(), devgit.PrepareWorktreeRequest{
			OperationID: "prepare-unreachable", TaskHandle: "task-unreachable",
			RepositoryID: fixture.repositoryID, BaseRevision: orphan,
		})
		if err == nil {
			t.Fatal("PrepareWorktree(unreachable base) error = nil")
		}
	})

	t.Run("untracked preexisting target is preserved", func(t *testing.T) {
		fixture := newRepositoryFixture(t, "product-api")
		registry := newLifecycleRegistry(t, fixture)
		target := filepath.Join(fixture.worktreeRoot, "task-preexisting")
		if err := os.Mkdir(target, 0o700); err != nil {
			t.Fatal(err)
		}
		marker := filepath.Join(target, "preserve.txt")
		if err := os.WriteFile(marker, []byte("preserve\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		_, err := registry.PrepareWorktree(context.Background(), lifecycleRequest(t, fixture, "prepare-preexisting", "task-preexisting"))
		if err == nil {
			t.Fatal("PrepareWorktree(preexisting target) error = nil")
		}
		if contents, readErr := os.ReadFile(marker); readErr != nil || string(contents) != "preserve\n" {
			t.Fatalf("preexisting content changed: %q, %v", contents, readErr)
		}
	})

	t.Run("symlink target escape is preserved", func(t *testing.T) {
		fixture := newRepositoryFixture(t, "product-api")
		registry := newLifecycleRegistry(t, fixture)
		outside := canonicalTempDir(t)
		target := filepath.Join(fixture.worktreeRoot, "task-symlink")
		if err := os.Symlink(outside, target); err != nil {
			t.Fatal(err)
		}
		_, err := registry.PrepareWorktree(context.Background(), lifecycleRequest(t, fixture, "prepare-symlink", "task-symlink"))
		if err == nil {
			t.Fatal("PrepareWorktree(symlink target) error = nil")
		}
		if info, statErr := os.Lstat(target); statErr != nil || info.Mode()&os.ModeSymlink == 0 {
			t.Fatalf("symlink target was changed: %v, %v", info, statErr)
		}
	})

	t.Run("same task with altered operation conflicts", func(t *testing.T) {
		fixture := newRepositoryFixture(t, "product-api")
		registry := newLifecycleRegistry(t, fixture)
		first := lifecycleRequest(t, fixture, "prepare-original", "task-collision")
		if _, err := registry.PrepareWorktree(context.Background(), first); err != nil {
			t.Fatal(err)
		}
		altered := first
		altered.OperationID = "prepare-altered"
		if _, err := registry.PrepareWorktree(context.Background(), altered); err == nil {
			t.Fatal("PrepareWorktree(altered operation) error = nil")
		}
	})

	t.Run("deterministic branch without its target conflicts", func(t *testing.T) {
		fixture := newRepositoryFixture(t, "product-api")
		registry := newLifecycleRegistry(t, fixture)
		request := lifecycleRequest(t, fixture, "prepare-orphan-branch", "task-orphan-branch")
		prepared, err := registry.PrepareWorktree(context.Background(), request)
		if err != nil {
			t.Fatal(err)
		}
		runGit(t, fixture.gitExecutable, "--no-optional-locks", "-C", fixture.primary,
			"worktree", "remove", "--", prepared.CanonicalPath)
		if _, err := registry.PrepareWorktree(context.Background(), request); err == nil {
			t.Fatal("PrepareWorktree(orphan branch) error = nil")
		}
	})

	t.Run("live lease path conflicts before Git mutation", func(t *testing.T) {
		fixture := newRepositoryFixture(t, "product-api")
		registry := newLifecycleRegistry(t, fixture)
		request := lifecycleRequest(t, fixture, "prepare-live", "task-live")
		request.LiveLeasePaths = []string{filepath.Join(fixture.worktreeRoot, request.TaskHandle)}
		if _, err := registry.PrepareWorktree(context.Background(), request); err == nil {
			t.Fatal("PrepareWorktree(live lease collision) error = nil")
		}
		if _, err := os.Lstat(request.LiveLeasePaths[0]); !os.IsNotExist(err) {
			t.Fatalf("live collision created target: %v", err)
		}
	})

	t.Run("wrong repository worktree cannot be adopted", func(t *testing.T) {
		fixture := newRepositoryFixture(t, "product-api")
		registry := newLifecycleRegistry(t, fixture)
		other := newRepositoryFixtureUnder(t, fixture.approvedRoot, "other-api", fixture.gitExecutable)
		target := filepath.Join(fixture.worktreeRoot, "task-wrong-repository")
		runGit(t, fixture.gitExecutable, "--no-optional-locks", "-C", other.primary, "worktree", "add", "--detach", target, "HEAD")
		if _, err := registry.PrepareWorktree(context.Background(), lifecycleRequest(t, fixture, "prepare-wrong", "task-wrong-repository")); err == nil {
			t.Fatal("PrepareWorktree(wrong repository) error = nil")
		}
	})
}

func TestRegistry_CleanupWorktreeRefusesAmbiguityAndRemovesOnlyExactCleanBase(t *testing.T) {
	t.Run("dirty and untracked work is preserved", func(t *testing.T) {
		fixture := newRepositoryFixture(t, "product-api")
		registry := newLifecycleRegistry(t, fixture)
		request := lifecycleRequest(t, fixture, "prepare-dirty", "task-dirty")
		prepared, err := registry.PrepareWorktree(context.Background(), request)
		if err != nil {
			t.Fatal(err)
		}
		untracked := filepath.Join(prepared.CanonicalPath, "untracked.txt")
		if err := os.WriteFile(untracked, []byte("work\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := registry.CleanupWorktree(context.Background(), cleanupRequest(request)); err == nil {
			t.Fatal("CleanupWorktree(dirty) error = nil")
		}
		if _, err := os.Lstat(untracked); err != nil {
			t.Fatalf("dirty work was not preserved: %v", err)
		}
	})

	t.Run("unpushed commit is preserved", func(t *testing.T) {
		fixture := newRepositoryFixture(t, "product-api")
		registry := newLifecycleRegistry(t, fixture)
		request := lifecycleRequest(t, fixture, "prepare-unpushed", "task-unpushed")
		prepared, err := registry.PrepareWorktree(context.Background(), request)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(prepared.CanonicalPath, "candidate.txt"), []byte("candidate\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		runGit(t, fixture.gitExecutable, "--no-optional-locks", "-C", prepared.CanonicalPath, "add", "candidate.txt")
		runGit(t, fixture.gitExecutable, "--no-optional-locks", "-C", prepared.CanonicalPath,
			"-c", "user.name=DevCrew Fixture", "-c", "user.email=fixture@example.invalid", "commit", "-m", "candidate")
		if err := registry.CleanupWorktree(context.Background(), cleanupRequest(request)); err == nil {
			t.Fatal("CleanupWorktree(unpushed) error = nil")
		}
		if _, err := os.Lstat(prepared.CanonicalPath); err != nil {
			t.Fatalf("unpushed worktree was removed: %v", err)
		}
	})

	t.Run("divergent head is preserved", func(t *testing.T) {
		fixture := newRepositoryFixture(t, "product-api")
		registry := newLifecycleRegistry(t, fixture)
		request := lifecycleRequest(t, fixture, "prepare-divergent", "task-divergent")
		prepared, err := registry.PrepareWorktree(context.Background(), request)
		if err != nil {
			t.Fatal(err)
		}
		tree := gitOutput(t, fixture.gitExecutable, "--no-optional-locks", "-C", fixture.primary, "rev-parse", "HEAD^{tree}")
		divergent := gitOutputWithEnvironment(t, fixture.gitExecutable, []string{
			"GIT_AUTHOR_NAME=DevCrew Fixture", "GIT_AUTHOR_EMAIL=fixture@example.invalid",
			"GIT_COMMITTER_NAME=DevCrew Fixture", "GIT_COMMITTER_EMAIL=fixture@example.invalid",
		}, "--no-optional-locks", "-C", fixture.primary, "commit-tree", tree, "-m", "divergent")
		runGit(t, fixture.gitExecutable, "--no-optional-locks", "-C", prepared.CanonicalPath, "reset", "--hard", divergent)
		if err := registry.CleanupWorktree(context.Background(), cleanupRequest(request)); err == nil {
			t.Fatal("CleanupWorktree(divergent) error = nil")
		}
		if _, err := os.Lstat(prepared.CanonicalPath); err != nil {
			t.Fatalf("divergent worktree was removed: %v", err)
		}
	})

	t.Run("locked worktree is preserved", func(t *testing.T) {
		fixture := newRepositoryFixture(t, "product-api")
		registry := newLifecycleRegistry(t, fixture)
		request := lifecycleRequest(t, fixture, "prepare-locked", "task-locked")
		prepared, err := registry.PrepareWorktree(context.Background(), request)
		if err != nil {
			t.Fatal(err)
		}
		runGit(t, fixture.gitExecutable, "--no-optional-locks", "-C", fixture.primary, "worktree", "lock", "--reason", "fixture", prepared.CanonicalPath)
		if err := registry.CleanupWorktree(context.Background(), cleanupRequest(request)); err == nil {
			t.Fatal("CleanupWorktree(locked) error = nil")
		}
		if _, err := os.Lstat(prepared.CanonicalPath); err != nil {
			t.Fatalf("locked worktree was removed: %v", err)
		}
	})

	t.Run("detached worktree is preserved", func(t *testing.T) {
		fixture := newRepositoryFixture(t, "product-api")
		registry := newLifecycleRegistry(t, fixture)
		request := lifecycleRequest(t, fixture, "prepare-detached", "task-detached")
		prepared, err := registry.PrepareWorktree(context.Background(), request)
		if err != nil {
			t.Fatal(err)
		}
		runGit(t, fixture.gitExecutable, "--no-optional-locks", "-C", prepared.CanonicalPath,
			"checkout", "--detach", prepared.HeadRevision)
		if err := registry.CleanupWorktree(context.Background(), cleanupRequest(request)); err == nil {
			t.Fatal("CleanupWorktree(detached) error = nil")
		}
		if _, err := os.Lstat(prepared.CanonicalPath); err != nil {
			t.Fatalf("detached worktree was removed: %v", err)
		}
	})

	t.Run("exact clean prepared worktree and branch are removed", func(t *testing.T) {
		fixture := newRepositoryFixture(t, "product-api")
		registry := newLifecycleRegistry(t, fixture)
		request := lifecycleRequest(t, fixture, "prepare-cleanup", "task-cleanup")
		prepared, err := registry.PrepareWorktree(context.Background(), request)
		if err != nil {
			t.Fatal(err)
		}
		if err := registry.CleanupWorktree(context.Background(), cleanupRequest(request)); err != nil {
			t.Fatalf("CleanupWorktree(clean) error = %v", err)
		}
		if _, err := os.Lstat(prepared.CanonicalPath); !os.IsNotExist(err) {
			t.Fatalf("clean worktree remains: %v", err)
		}
		showRef := exec.Command(fixture.gitExecutable, "--no-optional-locks", "-C", fixture.primary, "show-ref", "--verify", "--quiet", "refs/heads/"+prepared.Branch)
		showRef.Env = gitTestEnvironment(nil)
		if err := showRef.Run(); err == nil {
			t.Fatalf("prepared branch %q remains", prepared.Branch)
		}
	})
}

func newLifecycleRegistry(t *testing.T, fixture repositoryFixture) *devgit.Registry {
	t.Helper()
	registry, err := devgit.NewRegistry(context.Background(), devgit.RegistryConfig{
		GitExecutable: fixture.gitExecutable,
		ApprovedRoots: []string{fixture.approvedRoot},
		Repositories: []devgit.RepositoryConfig{{
			ID: fixture.repositoryID, PrimaryCheckout: fixture.primary,
			WorktreeRoot: fixture.worktreeRoot, DefaultBranch: "main",
		}},
	})
	if err != nil {
		t.Fatalf("NewRegistry() error = %v", err)
	}
	return registry
}

func lifecycleRequest(t *testing.T, fixture repositoryFixture, operationID, taskHandle string) devgit.PrepareWorktreeRequest {
	t.Helper()
	return devgit.PrepareWorktreeRequest{
		OperationID: operationID, TaskHandle: taskHandle, RepositoryID: fixture.repositoryID,
		BaseRevision: gitOutput(t, fixture.gitExecutable, "--no-optional-locks", "-C", fixture.primary, "rev-parse", "HEAD"),
	}
}

func cleanupRequest(request devgit.PrepareWorktreeRequest) devgit.CleanupWorktreeRequest {
	return devgit.CleanupWorktreeRequest(request)
}

func gitOutput(t *testing.T, executable string, arguments ...string) string {
	t.Helper()
	return gitOutputWithEnvironment(t, executable, nil, arguments...)
}

func gitOutputWithEnvironment(t *testing.T, executable string, additional []string, arguments ...string) string {
	t.Helper()
	command := exec.Command(executable, arguments...)
	command.Env = gitTestEnvironment(additional)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("Git fixture output command failed: %v: %s", err, output)
	}
	return strings.TrimSpace(string(output))
}

func gitTestEnvironment(additional []string) []string {
	return append([]string{
		"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1", "GIT_OPTIONAL_LOCKS=0", "LC_ALL=C",
	}, additional...)
}
