package git_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	devgit "github.com/comisai/comis-dev-crew/internal/git"
)

func TestRegistry_InspectCandidatePreservesStatusCleanlinessSemantics(t *testing.T) {
	t.Run("large tracked tree", func(t *testing.T) {
		fixture, registry, request, worktree := preparedCandidateFixture(t, "large-status")
		contents := make([]byte, (17<<20)+1)
		for index := 0; index < 4; index++ {
			if err := os.WriteFile(filepath.Join(worktree, "large-"+string(rune('a'+index))), contents, 0o600); err != nil {
				t.Fatal(err)
			}
		}
		commitCandidatePaths(t, fixture, worktree, "large tracked tree", ".")
		assertCleanCandidate(t, registry, request)
	})

	t.Run("gitlink", func(t *testing.T) {
		fixture, registry, request, worktree := preparedCandidateFixture(t, "gitlink-status")
		nested := filepath.Join(worktree, "nested")
		runGit(t, fixture.gitExecutable, "init", "--initial-branch=main", nested)
		if err := os.WriteFile(filepath.Join(nested, "nested.txt"), []byte("nested\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		runGit(t, fixture.gitExecutable, "--no-optional-locks", "-C", nested, "add", "nested.txt")
		runGit(t, fixture.gitExecutable, "--no-optional-locks", "-C", nested,
			"-c", "user.name=DevCrew Fixture", "-c", "user.email=fixture@example.invalid",
			"commit", "-m", "nested fixture")
		commitCandidatePaths(t, fixture, worktree, "gitlink", "nested")
		assertCleanCandidate(t, registry, request)
	})

	t.Run("ignored artifact", func(t *testing.T) {
		fixture, registry, request, worktree := preparedCandidateFixture(t, "ignored-status")
		if err := os.WriteFile(filepath.Join(worktree, ".gitignore"), []byte("build/\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		commitCandidatePaths(t, fixture, worktree, "ignore build output", ".gitignore")
		if err := os.Mkdir(filepath.Join(worktree, "build"), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(worktree, "build", "output.bin"), []byte("ignored\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		assertCleanCandidate(t, registry, request)
	})
}

func preparedCandidateFixture(
	t *testing.T,
	identity string,
) (repositoryFixture, *devgit.Registry, devgit.CandidateSnapshotRequest, string) {
	t.Helper()
	fixture := newRepositoryFixture(t, "product-api")
	registry := newLifecycleRegistry(t, fixture)
	request := lifecycleRequest(t, fixture, "prepare-"+identity, "task-"+identity)
	prepared, err := registry.PrepareWorktree(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	return fixture, registry, devgit.CandidateSnapshotRequest{
		TaskHandle: request.TaskHandle, RepositoryID: request.RepositoryID,
		WorktreePath: prepared.CanonicalPath,
	}, prepared.CanonicalPath
}

func commitCandidatePaths(
	t *testing.T,
	fixture repositoryFixture,
	worktree string,
	message string,
	paths ...string,
) {
	t.Helper()
	arguments := []string{"--no-optional-locks", "-C", worktree, "add", "--"}
	arguments = append(arguments, paths...)
	runGit(t, fixture.gitExecutable, arguments...)
	runGit(t, fixture.gitExecutable, "--no-optional-locks", "-C", worktree,
		"-c", "user.name=DevCrew Fixture", "-c", "user.email=fixture@example.invalid",
		"commit", "-m", message)
}

func assertCleanCandidate(t *testing.T, registry *devgit.Registry, request devgit.CandidateSnapshotRequest) {
	t.Helper()
	snapshot, err := registry.InspectCandidate(context.Background(), request)
	if err != nil || snapshot.Cleanliness != devgit.CandidateClean {
		t.Fatalf("InspectCandidate() = %#v, %v", snapshot, err)
	}
}
