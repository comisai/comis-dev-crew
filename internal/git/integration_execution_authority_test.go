package git_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/comisai/comis-dev-crew/internal/application"
)

func TestRegistry_RefusesCommandCapableMergeConfiguration(t *testing.T) {
	fixture := newIntegrationFixture(t)
	marker := filepath.Join(fixture.target.CanonicalPath, "merge-driver-executed")
	runGit(t, fixture.repository.gitExecutable, "--no-optional-locks", "-C", fixture.target.CanonicalPath,
		"config", "--local", "merge.service-authority.name", "untrusted driver")
	runGit(t, fixture.repository.gitExecutable, "--no-optional-locks", "-C", fixture.target.CanonicalPath,
		"config", "--local", "merge.service-authority.driver", "touch merge-driver-executed; cp %B %A")
	writeIntegrationFile(t, fixture.target.CanonicalPath, ".gitattributes", "fixture.txt merge=service-authority\n")
	writeIntegrationFile(t, fixture.target.CanonicalPath, "fixture.txt", "target\n")
	commitIntegrationChanges(t, fixture, fixture.target.CanonicalPath, ".gitattributes", "fixture.txt")
	targetHead := integrationGitOutput(t, fixture, fixture.target.CanonicalPath, "rev-parse", "HEAD")
	candidateHead := commitIntegrationFile(t, fixture, fixture.candidate.CanonicalPath, "fixture.txt", "candidate\n")
	request := fixture.request("integration-command-config", application.IntegrationMerge, candidateHead, targetHead)

	_, err := fixture.registry.ApplyIntegrationCandidate(context.Background(), request)
	if !errors.Is(err, application.ErrIntegrationMutationNotStarted) {
		t.Fatalf("ApplyIntegrationCandidate(command-capable config) error = %v, want ErrIntegrationMutationNotStarted", err)
	}
	if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("merge driver executed with service authority: %v", err)
	}
	if head := integrationGitOutput(t, fixture, fixture.target.CanonicalPath, "rev-parse", "HEAD"); head != targetHead {
		t.Fatalf("target head = %q, want %q", head, targetHead)
	}
}

func TestRegistry_RebasePreflightUsesDirectoryRenameSemantics(t *testing.T) {
	fixture := newIntegrationFixture(t)
	writeIntegrationFile(t, fixture.candidate.CanonicalPath, "old/existing.txt", "base\n")
	commitIntegrationChanges(t, fixture, fixture.candidate.CanonicalPath, "old/existing.txt")
	sharedBase := integrationGitOutput(t, fixture, fixture.candidate.CanonicalPath, "rev-parse", "HEAD")
	runGit(t, fixture.repository.gitExecutable, "--no-optional-locks", "-C", fixture.target.CanonicalPath,
		"reset", "--hard", sharedBase)
	candidateHead := commitIntegrationFile(t, fixture, fixture.candidate.CanonicalPath, "old/added.txt", "candidate\n")
	runGit(t, fixture.repository.gitExecutable, "--no-optional-locks", "-C", fixture.target.CanonicalPath,
		"mv", "old", "new")
	commitIntegrationChanges(t, fixture, fixture.target.CanonicalPath, "new/existing.txt")
	targetHead := integrationGitOutput(t, fixture, fixture.target.CanonicalPath, "rev-parse", "HEAD")
	request := fixture.request("integration-directory-rename", application.IntegrationRebase, candidateHead, targetHead)
	request.Candidate.BaseRevision = sharedBase

	_, err := fixture.registry.ApplyIntegrationCandidate(context.Background(), request)
	if !errors.Is(err, application.ErrIntegrationMutationNotStarted) {
		t.Fatalf("ApplyIntegrationCandidate(directory rename) error = %v, want ErrIntegrationMutationNotStarted", err)
	}
	if head := integrationGitOutput(t, fixture, fixture.target.CanonicalPath, "rev-parse", "HEAD"); head != targetHead {
		t.Fatalf("target head = %q, want %q", head, targetHead)
	}
}

func writeIntegrationFile(t *testing.T, worktree, name, body string) {
	t.Helper()
	path := filepath.Join(worktree, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func commitIntegrationChanges(t *testing.T, fixture integrationFixture, worktree string, names ...string) {
	t.Helper()
	arguments := append([]string{"--no-optional-locks", "-C", worktree, "add", "--"}, names...)
	runGit(t, fixture.repository.gitExecutable, arguments...)
	runGit(t, fixture.repository.gitExecutable, "--no-optional-locks", "-C", worktree,
		"-c", fixtureAuthorName, "-c", fixtureAuthorEmail, "commit", "-m", "fixture change")
}
