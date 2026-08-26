package git_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/comisai/comis-dev-crew/internal/application"
	devgit "github.com/comisai/comis-dev-crew/internal/git"
)

func TestRegistry_CompletedInitialRebaseRechecksDeadlineBeforeMutation(t *testing.T) {
	now := time.Date(2099, time.January, 1, 0, 0, 0, 0, time.UTC)
	fixture := newIntegrationFixtureWithClock(t, func() time.Time { return now })
	candidateHead := commitIntegrationFile(t, fixture, fixture.candidate.CanonicalPath,
		"component.txt", "component\n")
	targetHead := commitIntegrationFile(t, fixture, fixture.target.CanonicalPath,
		"target.txt", "target\n")
	request := fixture.request("integration-completed-proof-expiry", application.IntegrationRebase,
		candidateHead, targetHead)
	request.EvidenceExpiresAt = now.Add(time.Minute)
	writeServerRebaseProofForTest(t, fixture, request, "")
	targetRef := integrationGitOutput(t, fixture, fixture.target.CanonicalPath, "symbolic-ref", "HEAD")
	runGit(t, fixture.repository.gitExecutable, "--no-optional-locks", "-C", fixture.target.CanonicalPath,
		"symbolic-ref", integrationReceiptRefForTest("target", request), targetRef)
	runGit(t, fixture.repository.gitExecutable, "--no-optional-locks", "-C", fixture.target.CanonicalPath,
		"update-ref", integrationRebaseProofRefForTest(request), candidateHead)
	runGit(t, fixture.repository.gitExecutable, "--no-optional-locks", "-C", fixture.target.CanonicalPath,
		"-c", "core.hooksPath=/dev/null", "-c", "commit.gpgSign=false",
		"-c", "user.name=DevCrew Integration", "-c", "user.email=integration@example.invalid",
		"rebase", "--no-autostash", "--no-stat", "--reapply-cherry-picks", "--keep-empty",
		"--committer-date-is-author-date", "--onto", targetHead, request.Candidate.BaseRevision,
		strings.TrimPrefix(integrationRebaseProofRefForTest(request), "refs/heads/"))
	now = request.EvidenceExpiresAt

	_, err := fixture.registry.ApplyIntegrationCandidate(context.Background(), request)
	if !errors.Is(err, application.ErrIntegrationMutationNotStarted) {
		t.Fatalf("ApplyIntegrationCandidate(expired completed proof) error = %v", err)
	}
	if head := integrationGitOutput(t, fixture, fixture.repository.primary, "rev-parse", targetRef); head != targetHead {
		t.Fatalf("target head = %q, want unchanged %q", head, targetHead)
	}
}

func TestRegistry_CompletedRecoveryRechecksEveryReceiptAndDeadline(t *testing.T) {
	for _, test := range []struct {
		name   string
		id     string
		mutate func(t *testing.T, fixture integrationFixture, original application.IntegrationAdapterRequest, now *time.Time)
	}{
		{name: "expired evidence", id: "expired", mutate: func(_ *testing.T, _ integrationFixture, original application.IntegrationAdapterRequest, now *time.Time) {
			*now = original.EvidenceExpiresAt
		}},
		{name: "dangling original completion", id: "dangling", mutate: func(t *testing.T, fixture integrationFixture, original application.IntegrationAdapterRequest, _ *time.Time) {
			runGit(t, fixture.repository.gitExecutable, "--no-optional-locks", "-C", fixture.target.CanonicalPath,
				"symbolic-ref", integrationReceiptRefForTest("rebased", original), "refs/heads/missing-rebased")
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			now := time.Date(2099, time.January, 1, 0, 0, 0, 0, time.UTC)
			fixture := newIntegrationFixtureWithClock(t, func() time.Time { return now })
			candidateHead := commitIntegrationFile(t, fixture, fixture.candidate.CanonicalPath,
				"fixture.txt", "candidate\n")
			targetHead := commitIntegrationFile(t, fixture, fixture.target.CanonicalPath,
				"fixture.txt", "target\n")
			original := fixture.request("integration-completed-recovery-original-"+test.id,
				application.IntegrationRebase, candidateHead, targetHead)
			original.EvidenceExpiresAt = now.Add(time.Hour)
			stagePreviouslyAuthorizedRebaseConflict(t, fixture, original)
			if err := os.WriteFile(filepath.Join(fixture.target.CanonicalPath, "fixture.txt"), []byte("resolved\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			runGit(t, fixture.repository.gitExecutable, "--no-optional-locks", "-C", fixture.target.CanonicalPath,
				"add", "--", "fixture.txt")
			recovery := original
			recovery.OperationID = "integration-completed-recovery-resume-" + test.id
			recovery.RecoveryOperationID = original.OperationID
			applied, err := fixture.registry.ApplyIntegrationCandidate(context.Background(), recovery)
			if err != nil || applied.Outcome != application.IntegrationApplied {
				t.Fatalf("ApplyIntegrationCandidate(recovery) = %#v, %v", applied, err)
			}
			appliedRef := integrationReceiptRefForTest("applied", recovery)
			runGit(t, fixture.repository.gitExecutable, "--no-optional-locks", "-C", fixture.target.CanonicalPath,
				"update-ref", "-d", appliedRef, applied.ResultingHead)
			test.mutate(t, fixture, original, &now)

			if _, err := fixture.registry.ApplyIntegrationCandidate(context.Background(), recovery); err == nil {
				t.Fatal("ApplyIntegrationCandidate(unauthorized completed recovery) error = nil")
			}
			if _, err := integrationGitOutputError(fixture.repository.gitExecutable,
				fixture.target.CanonicalPath, "rev-parse", "--verify", appliedRef); err == nil {
				t.Fatal("completed recovery recreated the applied receipt")
			}
		})
	}
}

func TestRegistry_IsolatedMergeConflictRefusesSharedMutation(t *testing.T) {
	fixture := newIntegrationFixture(t)
	candidateHead := commitIntegrationFile(t, fixture, fixture.candidate.CanonicalPath,
		"fixture.txt", "candidate\n")
	targetHead := commitIntegrationFile(t, fixture, fixture.target.CanonicalPath,
		"fixture.txt", "target\n")
	request := fixture.request("integration-isolated-merge-conflict", application.IntegrationMerge,
		candidateHead, targetHead)

	_, err := fixture.registry.ApplyIntegrationCandidate(context.Background(), request)
	if !errors.Is(err, application.ErrIntegrationMutationNotStarted) {
		t.Fatalf("ApplyIntegrationCandidate(isolated merge conflict) error = %v", err)
	}
	if head := integrationGitOutput(t, fixture, fixture.target.CanonicalPath, "rev-parse", "HEAD"); head != targetHead {
		t.Fatalf("target head = %q, want unchanged %q", head, targetHead)
	}
	if status := gitOutputAllowEmpty(t, fixture.repository.gitExecutable, "--no-optional-locks", "-C",
		fixture.target.CanonicalPath, "status", "--porcelain"); status != "" {
		t.Fatalf("target status = %q, want clean", status)
	}
}

func TestRegistry_MaterializationPreservesEditsAcrossCASFailures(t *testing.T) {
	for _, mode := range []string{"before", "failed"} {
		t.Run(mode, func(t *testing.T) {
			fixture := newIntegrationFixture(t)
			candidateHead := commitIntegrationFile(t, fixture, fixture.candidate.CanonicalPath,
				"component.txt", "component\n")
			targetHead := commitIntegrationFile(t, fixture, fixture.target.CanonicalPath,
				"target.txt", "target\n")
			request := fixture.request("integration-materialize-"+mode, application.IntegrationMerge,
				candidateHead, targetHead)
			targetRef := "refs/heads/" + fixture.target.Branch
			wrapper, arm := writeIntegrationCASWrapper(t, fixture, mode, targetRef, candidateHead, targetHead)
			registry := newIntegrationRegistryWithExecutable(t, fixture, wrapper)
			if err := os.WriteFile(arm, []byte("armed\n"), 0o600); err != nil {
				t.Fatal(err)
			}

			if _, err := registry.ApplyIntegrationCandidate(context.Background(), request); err == nil {
				t.Fatal("ApplyIntegrationCandidate(faulted CAS) error = nil")
			}
			contents, err := os.ReadFile(filepath.Join(fixture.target.CanonicalPath, "target.txt"))
			if err != nil || string(contents) != "developer edit\n" {
				t.Fatalf("developer edit = %q, %v", contents, err)
			}
		})
	}
}

func TestRegistry_ReconcilesCrashAfterResultCAS(t *testing.T) {
	fixture := newIntegrationFixture(t)
	candidateHead := commitIntegrationFile(t, fixture, fixture.candidate.CanonicalPath,
		"component.txt", "component\n")
	targetHead := commitIntegrationFile(t, fixture, fixture.target.CanonicalPath,
		"target.txt", "target\n")
	request := fixture.request("integration-materialize-after", application.IntegrationMerge,
		candidateHead, targetHead)
	targetRef := "refs/heads/" + fixture.target.Branch
	wrapper, arm := writeIntegrationCASWrapper(t, fixture, "after", targetRef, candidateHead, targetHead)
	registry := newIntegrationRegistryWithExecutable(t, fixture, wrapper)
	if err := os.WriteFile(arm, []byte("armed\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := registry.ApplyIntegrationCandidate(context.Background(), request); err == nil {
		t.Fatal("ApplyIntegrationCandidate(crash after CAS) error = nil")
	}
	resultingHead := integrationGitOutput(t, fixture, fixture.repository.primary, "rev-parse", targetRef)
	if resultingHead == targetHead {
		t.Fatal("target ref did not advance before the simulated crash")
	}

	replayed, err := fixture.registry.ApplyIntegrationCandidate(context.Background(), request)
	if err != nil || replayed.Outcome != application.IntegrationApplied || replayed.ResultingHead != resultingHead {
		t.Fatalf("ApplyIntegrationCandidate(crash retry) = %#v, %v", replayed, err)
	}
	if status := gitOutputAllowEmpty(t, fixture.repository.gitExecutable, "--no-optional-locks", "-C",
		fixture.target.CanonicalPath, "status", "--porcelain"); status != "" {
		t.Fatalf("target status after retry = %q", status)
	}
}

func newIntegrationRegistryWithExecutable(t *testing.T, fixture integrationFixture, executable string) *devgit.Registry {
	t.Helper()
	registry, err := devgit.NewRegistry(context.Background(), devgit.RegistryConfig{
		GitExecutable: executable, Clock: time.Now,
		ApprovedRoots: []string{fixture.repository.approvedRoot},
		Repositories: []devgit.RepositoryConfig{{
			ID: fixture.repository.repositoryID, PrimaryCheckout: fixture.repository.primary,
			WorktreeRoot: fixture.repository.worktreeRoot, DefaultBranch: "main",
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	return registry
}

func writeIntegrationCASWrapper(
	t *testing.T,
	fixture integrationFixture,
	mode string,
	targetRef string,
	divergentHead string,
	expectedHead string,
) (string, string) {
	t.Helper()
	root := canonicalTempDir(t)
	wrapper := filepath.Join(root, "git-cas-wrapper")
	arm := filepath.Join(root, "armed")
	quote := func(value string) string { return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'" }
	script := fmt.Sprintf(`#!/bin/sh
real=%s
arm=%s
target=%s
worktree=%s
mode=%s
divergent=%s
expected=%s
previous=
matched=false
for argument in "$@"; do
  if [ "$previous" = update-ref ] && [ "$argument" = "$target" ]; then
    matched=true
  fi
  previous=$argument
done
if [ -f "$arm" ] && [ "$matched" = true ]; then
  rm -f "$arm"
  if [ "$mode" = before ]; then
    printf 'developer edit\n' > "$worktree/target.txt"
    exit 71
  fi
  if [ "$mode" = failed ]; then
    "$real" --no-optional-locks -C "$worktree" update-ref "$target" "$divergent" "$expected" || exit $?
    printf 'developer edit\n' > "$worktree/target.txt"
    exit 72
  fi
  "$real" "$@"
  status=$?
  if [ $status -eq 0 ]; then
    exit 73
  fi
  exit $status
fi
exec "$real" "$@"
`, quote(fixture.repository.gitExecutable), quote(arm), quote(targetRef),
		quote(fixture.target.CanonicalPath), quote(mode), quote(divergentHead), quote(expectedHead))
	if err := os.WriteFile(wrapper, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	return wrapper, arm
}

func integrationGitOutputError(executable, worktree string, arguments ...string) (string, error) {
	command := append([]string{"--no-optional-locks", "-C", worktree}, arguments...)
	output, err := exec.Command(executable, command...).Output()
	return strings.TrimSpace(string(output)), err
}
