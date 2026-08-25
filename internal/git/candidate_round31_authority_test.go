package git_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	devgit "github.com/comisai/comis-dev-crew/internal/git"
)

func TestRegistry_InspectCandidateRejectsDirtyGitlinks(t *testing.T) {
	for _, test := range []struct {
		name  string
		dirty func(t *testing.T, fixture repositoryFixture, nested string)
	}{
		{
			name: "modified tracked file",
			dirty: func(t *testing.T, _ repositoryFixture, nested string) {
				t.Helper()
				if err := os.WriteFile(filepath.Join(nested, "nested.txt"), []byte("modified\n"), 0o600); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "untracked file",
			dirty: func(t *testing.T, _ repositoryFixture, nested string) {
				t.Helper()
				if err := os.WriteFile(filepath.Join(nested, "untracked.txt"), []byte("untracked\n"), 0o600); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "missing worktree",
			dirty: func(t *testing.T, _ repositoryFixture, nested string) {
				t.Helper()
				if err := os.RemoveAll(nested); err != nil {
					t.Fatal(err)
				}
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			identity := "dirty-gitlink-" + strings.ReplaceAll(test.name, " ", "-")
			fixture, registry, request, worktree := preparedCandidateFixture(t, identity)
			nested := createEmbeddedGitlink(t, fixture, worktree, "nested")
			test.dirty(t, fixture, nested)

			snapshot, err := registry.InspectCandidate(context.Background(), request)
			if err == nil && snapshot.Cleanliness == devgit.CandidateClean {
				t.Fatalf("InspectCandidate(dirty gitlink) = %#v, %v", snapshot, err)
			}
		})
	}
}

func TestRegistry_InspectCandidateRejectsDirtyNestedGitlink(t *testing.T) {
	fixture, registry, request, worktree := preparedCandidateFixture(t, "dirty-nested-gitlink")
	nested := filepath.Join(worktree, "nested")
	runGit(t, fixture.gitExecutable, "init", "--initial-branch=main", nested)
	child := createEmbeddedGitlink(t, fixture, nested, "child")
	commitCandidatePaths(t, fixture, worktree, "outer gitlink", "nested")
	if err := os.WriteFile(filepath.Join(child, "nested.txt"), []byte("nested dirty\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	snapshot, err := registry.InspectCandidate(context.Background(), request)
	if err == nil && snapshot.Cleanliness == devgit.CandidateClean {
		t.Fatalf("InspectCandidate(dirty nested gitlink) = %#v, %v", snapshot, err)
	}
}

func TestRegistry_InspectCandidateSupportsBuiltInAttributes(t *testing.T) {
	for _, test := range []struct {
		name       string
		attributes string
		contents   []byte
	}{
		{name: "text auto", attributes: "*.txt text=auto\n", contents: []byte("normalized\n")},
		{name: "crlf", attributes: "*.txt text eol=crlf\n", contents: []byte("normalized\r\n")},
		{name: "ident", attributes: "*.txt ident\n", contents: []byte("$Id$\n")},
	} {
		t.Run(test.name, func(t *testing.T) {
			identity := "builtin-" + strings.ReplaceAll(test.name, " ", "-")
			fixture, registry, request, worktree := preparedCandidateFixture(t, identity)
			if err := os.WriteFile(filepath.Join(worktree, ".gitattributes"), []byte(test.attributes), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(worktree, "normalized.txt"), test.contents, 0o600); err != nil {
				t.Fatal(err)
			}
			commitCandidatePaths(t, fixture, worktree, "built-in attributes", ".gitattributes", "normalized.txt")
			if test.name == "crlf" {
				if err := os.WriteFile(filepath.Join(worktree, "normalized.txt"), test.contents, 0o600); err != nil {
					t.Fatal(err)
				}
			}
			assertCleanCandidate(t, registry, request)
		})
	}
}

func TestRegistry_InspectCandidateDiffAcceptsLargeTreesAndGitlinks(t *testing.T) {
	t.Run("large blob and tree", func(t *testing.T) {
		fixture, registry, request, worktree := preparedCandidateFixture(t, "large-diff")
		base := gitOutput(t, fixture.gitExecutable, "--no-optional-locks", "-C", worktree, "rev-parse", "HEAD")
		contents := make([]byte, (17<<20)+1)
		for index := 0; index < 4; index++ {
			name := "large-" + string(rune('a'+index))
			if err := os.WriteFile(filepath.Join(worktree, name), contents, 0o600); err != nil {
				t.Fatal(err)
			}
		}
		commitCandidatePaths(t, fixture, worktree, "large diff tree", ".")
		diff, err := registry.InspectCandidateDiff(context.Background(), devgit.CandidateDiffRequest{
			TaskHandle: request.TaskHandle, RepositoryID: request.RepositoryID,
			WorktreePath: worktree, BaseRevision: base,
		})
		if err != nil {
			t.Fatalf("InspectCandidateDiff(large tree) error = %v", err)
		}
		if len(diff.Committed) != 4 || !diff.FileListTruncated {
			t.Fatalf("InspectCandidateDiff(large tree) = %#v", diff)
		}
	})

	t.Run("gitlink identity", func(t *testing.T) {
		fixture, registry, request, worktree := preparedCandidateFixture(t, "gitlink-diff")
		base := gitOutput(t, fixture.gitExecutable, "--no-optional-locks", "-C", worktree, "rev-parse", "HEAD")
		createEmbeddedGitlink(t, fixture, worktree, "nested")
		diff, err := registry.InspectCandidateDiff(context.Background(), devgit.CandidateDiffRequest{
			TaskHandle: request.TaskHandle, RepositoryID: request.RepositoryID,
			WorktreePath: worktree, BaseRevision: base,
		})
		if err != nil {
			t.Fatalf("InspectCandidateDiff(gitlink) error = %v", err)
		}
		if len(diff.Committed) != 1 || diff.Committed[0].Path != "nested" {
			t.Fatalf("InspectCandidateDiff(gitlink) = %#v", diff)
		}
	})
}

func createEmbeddedGitlink(t *testing.T, fixture repositoryFixture, parent, name string) string {
	t.Helper()
	nested := filepath.Join(parent, name)
	runGit(t, fixture.gitExecutable, "init", "--initial-branch=main", nested)
	if err := os.WriteFile(filepath.Join(nested, "nested.txt"), []byte("nested\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runGit(t, fixture.gitExecutable, "--no-optional-locks", "-C", nested, "add", "nested.txt")
	runGit(t, fixture.gitExecutable, "--no-optional-locks", "-C", nested,
		"-c", "user.name=DevCrew Fixture", "-c", "user.email=fixture@example.invalid",
		"commit", "-m", "nested fixture")
	commitCandidatePaths(t, fixture, parent, "gitlink", name)
	return nested
}
