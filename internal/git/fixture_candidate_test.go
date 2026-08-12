package git_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	devgit "github.com/comisai/comis-dev-crew/internal/git"
)

func TestRegistry_PrepareFixtureCandidateRejectsUnavailableOrAlteredAuthority(t *testing.T) {
	fixture := newRepositoryFixture(t, "product-api")
	registry := newLifecycleRegistry(t, fixture)
	request := lifecycleRequest(t, fixture, "prepare-fixture-guards", "task-fixture-guards")
	prepared, err := registry.PrepareWorktree(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	candidateRequest := devgit.FixtureCandidateRequest{
		TaskHandle: request.TaskHandle, RepositoryID: request.RepositoryID,
		WorktreePath: prepared.CanonicalPath, BaseRevision: request.BaseRevision,
		ArtifactRelativePath: "report.md",
	}
	if _, err := (*devgit.Registry)(nil).PrepareFixtureCandidate(context.Background(), candidateRequest); err == nil {
		t.Fatal("PrepareFixtureCandidate(nil registry) error = nil")
	}
	//lint:ignore SA1012 nil context is an explicit public-boundary rejection case.
	if _, err := registry.PrepareFixtureCandidate(nil, candidateRequest); err == nil {
		t.Fatal("PrepareFixtureCandidate(nil context) error = nil")
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := registry.PrepareFixtureCandidate(cancelled, candidateRequest); !errors.Is(err, context.Canceled) {
		t.Fatalf("PrepareFixtureCandidate(cancelled) error = %v", err)
	}
	invalidIdentity := candidateRequest
	invalidIdentity.TaskHandle = "bad task"
	if _, err := registry.PrepareFixtureCandidate(context.Background(), invalidIdentity); err == nil {
		t.Fatal("PrepareFixtureCandidate(invalid identity) error = nil")
	}
	missingRepository := candidateRequest
	missingRepository.RepositoryID = "missing-repository"
	if _, err := registry.PrepareFixtureCandidate(context.Background(), missingRepository); err == nil {
		t.Fatal("PrepareFixtureCandidate(missing repository) error = nil")
	}
	wrongWorktree := candidateRequest
	wrongWorktree.WorktreePath = fixture.primary
	if _, err := registry.PrepareFixtureCandidate(context.Background(), wrongWorktree); err == nil {
		t.Fatal("PrepareFixtureCandidate(wrong worktree) error = nil")
	}
	missingBase := candidateRequest
	missingBase.BaseRevision = strings.Repeat("f", 40)
	if _, err := registry.PrepareFixtureCandidate(context.Background(), missingBase); err == nil {
		t.Fatal("PrepareFixtureCandidate(missing base) error = nil")
	}
	if err := os.Chmod(prepared.CanonicalPath, 0o500); err != nil {
		t.Fatal(err)
	}
	_, creationErr := registry.PrepareFixtureCandidate(context.Background(), candidateRequest)
	if err := os.Chmod(prepared.CanonicalPath, 0o700); err != nil {
		t.Fatal(err)
	}
	if creationErr == nil {
		t.Fatal("PrepareFixtureCandidate(read-only worktree) error = nil")
	}

	for _, test := range []struct {
		name     string
		artifact func(string)
	}{
		{name: "different tree", artifact: func(worktree string) {
			if err := os.WriteFile(filepath.Join(worktree, "other.md"), []byte("other\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			runGit(t, fixture.gitExecutable, "-C", worktree, "add", "other.md")
			runGit(t, fixture.gitExecutable, "-C", worktree, "commit", "-m", "different tree")
		}},
		{name: "unsafe artifact", artifact: func(worktree string) {
			if err := os.Symlink("README.md", filepath.Join(worktree, "report.md")); err != nil {
				t.Fatal(err)
			}
			runGit(t, fixture.gitExecutable, "-C", worktree, "add", "report.md")
			runGit(t, fixture.gitExecutable, "-C", worktree, "commit", "-m", "unsafe artifact")
		}},
		{name: "different artifact", artifact: func(worktree string) {
			if err := os.WriteFile(filepath.Join(worktree, "report.md"), []byte("different\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			runGit(t, fixture.gitExecutable, "-C", worktree, "add", "report.md")
			runGit(t, fixture.gitExecutable, "-C", worktree, "commit", "-m", "different artifact")
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			alteredRequest := lifecycleRequest(t, fixture, "prepare-fixture-"+strings.ReplaceAll(test.name, " ", "-"), "task-fixture-"+strings.ReplaceAll(test.name, " ", "-"))
			altered, err := registry.PrepareWorktree(context.Background(), alteredRequest)
			if err != nil {
				t.Fatal(err)
			}
			test.artifact(altered.CanonicalPath)
			if _, err := registry.PrepareFixtureCandidate(context.Background(), devgit.FixtureCandidateRequest{
				TaskHandle: alteredRequest.TaskHandle, RepositoryID: alteredRequest.RepositoryID,
				WorktreePath: altered.CanonicalPath, BaseRevision: alteredRequest.BaseRevision,
				ArtifactRelativePath: "report.md",
			}); err == nil {
				t.Fatalf("PrepareFixtureCandidate(%s) error = nil", test.name)
			}
		})
	}

	ancestryRequest := lifecycleRequest(t, fixture, "prepare-fixture-ancestry", "task-fixture-ancestry")
	ancestry, err := registry.PrepareWorktree(context.Background(), ancestryRequest)
	if err != nil {
		t.Fatal(err)
	}
	ancestryCandidate := devgit.FixtureCandidateRequest{
		TaskHandle: ancestryRequest.TaskHandle, RepositoryID: ancestryRequest.RepositoryID,
		WorktreePath: ancestry.CanonicalPath, BaseRevision: ancestryRequest.BaseRevision,
		ArtifactRelativePath: "report.md",
	}
	if _, err := registry.PrepareFixtureCandidate(context.Background(), ancestryCandidate); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ancestry.CanonicalPath, "second.md"), []byte("second\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runGit(t, fixture.gitExecutable, "-C", ancestry.CanonicalPath, "add", "second.md")
	runGit(t, fixture.gitExecutable, "-C", ancestry.CanonicalPath, "commit", "-m", "second candidate")
	if _, err := registry.PrepareFixtureCandidate(context.Background(), ancestryCandidate); err == nil {
		t.Fatal("PrepareFixtureCandidate(altered ancestry) error = nil")
	}
}
