package git_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/comisai/comis-dev-crew/internal/application"
)

func TestRegistry_RejectsWorktreeFSMonitorBeforeStatus(t *testing.T) {
	fixture := newIntegrationFixture(t)
	marker := filepath.Join(fixture.target.CanonicalPath, "fsmonitor-executed")
	hook := filepath.Join(fixture.target.CanonicalPath, "fsmonitor-hook")
	if err := os.WriteFile(hook, []byte("#!/bin/sh\n: > fsmonitor-executed\nexit 1\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	runGit(t, fixture.repository.gitExecutable, "--no-optional-locks", "-C", fixture.target.CanonicalPath,
		"config", "--local", "extensions.worktreeConfig", "true")
	runGit(t, fixture.repository.gitExecutable, "--no-optional-locks", "-C", fixture.target.CanonicalPath,
		"config", "--worktree", "core.fsmonitor", hook)
	candidateHead := commitIntegrationFile(t, fixture, fixture.candidate.CanonicalPath,
		"component.txt", "component\n")
	targetHead := integrationGitOutput(t, fixture, fixture.target.CanonicalPath, "rev-parse", "HEAD")
	request := fixture.request("integration-worktree-fsmonitor", application.IntegrationMerge, candidateHead, targetHead)

	_, err := fixture.registry.ApplyIntegrationCandidate(context.Background(), request)
	if _, err := os.Lstat(marker); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("worktree fsmonitor executed before refusal: %v", err)
	}
	if !errors.Is(err, application.ErrIntegrationMutationNotStarted) {
		t.Fatalf("ApplyIntegrationCandidate(worktree fsmonitor) error = %v", err)
	}
}

func TestRegistry_RechecksEvidenceImmediatelyBeforeEveryInitialMutation(t *testing.T) {
	for _, strategy := range []application.IntegrationStrategy{
		application.IntegrationMerge,
		application.IntegrationCherryPick,
	} {
		t.Run(string(strategy), func(t *testing.T) {
			fresh := time.Date(2099, time.January, 1, 0, 0, 0, 0, time.UTC)
			expired := fresh.Add(time.Minute)
			armed := false
			checks := 0
			fixture := newIntegrationFixtureWithClock(t, func() time.Time {
				if !armed {
					return fresh
				}
				checks++
				if checks == 1 {
					return fresh
				}
				return expired
			})
			candidateHead := commitIntegrationFile(t, fixture, fixture.candidate.CanonicalPath,
				"component.txt", "component\n")
			targetHead := commitIntegrationFile(t, fixture, fixture.target.CanonicalPath,
				"target.txt", "target\n")
			request := fixture.request("integration-final-expiry-"+strings.ReplaceAll(string(strategy), "_", "-"), strategy, candidateHead, targetHead)
			request.EvidenceExpiresAt = expired
			armed = true

			_, err := fixture.registry.ApplyIntegrationCandidate(context.Background(), request)
			if !errors.Is(err, application.ErrIntegrationMutationNotStarted) {
				t.Fatalf("ApplyIntegrationCandidate(expired at final boundary) error = %v", err)
			}
			if head := integrationGitOutput(t, fixture, fixture.target.CanonicalPath, "rev-parse", "HEAD"); head != targetHead {
				t.Fatalf("target head = %q, want unchanged %q", head, targetHead)
			}
		})
	}
}

func TestRegistry_RechecksEvidenceBeforeRebaseContinuation(t *testing.T) {
	now := time.Date(2099, time.January, 1, 0, 0, 0, 0, time.UTC)
	fixture := newIntegrationFixtureWithClock(t, func() time.Time { return now })
	candidateHead := commitIntegrationFile(t, fixture, fixture.candidate.CanonicalPath,
		"fixture.txt", "candidate\n")
	targetHead := commitIntegrationFile(t, fixture, fixture.target.CanonicalPath,
		"fixture.txt", "integration\n")
	request := fixture.request("integration-recovery-final-expiry", application.IntegrationRebase, candidateHead, targetHead)
	request.EvidenceExpiresAt = now.Add(time.Minute)
	stagePreviouslyAuthorizedRebaseConflict(t, fixture, request)
	if err := os.WriteFile(filepath.Join(fixture.target.CanonicalPath, "fixture.txt"), []byte("resolved\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runGit(t, fixture.repository.gitExecutable, "--no-optional-locks", "-C", fixture.target.CanonicalPath,
		"add", "--", "fixture.txt")
	now = request.EvidenceExpiresAt
	recovery := request
	recovery.OperationID = "integration-recovery-final-expiry-resume"
	recovery.RecoveryOperationID = request.OperationID

	_, err := fixture.registry.ApplyIntegrationCandidate(context.Background(), recovery)
	if !errors.Is(err, application.ErrIntegrationMutationNotStarted) {
		t.Fatalf("ApplyIntegrationCandidate(expired recovery) error = %v", err)
	}
	if head := integrationGitOutput(t, fixture, fixture.repository.primary,
		"rev-parse", "refs/heads/"+fixture.target.Branch); head != targetHead {
		t.Fatalf("target head = %q, want unchanged %q", head, targetHead)
	}
}

func TestRegistry_IsolatedRebaseDoesNotUpdateUnrelatedRefs(t *testing.T) {
	fixture := newIntegrationFixture(t)
	first := commitIntegrationFile(t, fixture, fixture.candidate.CanonicalPath, "first.txt", "first\n")
	runGit(t, fixture.repository.gitExecutable, "--no-optional-locks", "-C", fixture.repository.primary,
		"update-ref", "refs/heads/rebase-unrelated", first)
	candidateHead := commitIntegrationFile(t, fixture, fixture.candidate.CanonicalPath, "second.txt", "second\n")
	targetHead := commitIntegrationFile(t, fixture, fixture.target.CanonicalPath, "target.txt", "target\n")
	runGit(t, fixture.repository.gitExecutable, "--no-optional-locks", "-C", fixture.target.CanonicalPath,
		"config", "--local", "rebase.updateRefs", "true")
	request := fixture.request("integration-isolated-update-refs", application.IntegrationRebase, candidateHead, targetHead)

	result, err := fixture.registry.ApplyIntegrationCandidate(context.Background(), request)
	if err != nil || result.Outcome != application.IntegrationApplied {
		t.Fatalf("ApplyIntegrationCandidate(rebase) = %#v, %v", result, err)
	}
	if head := integrationGitOutput(t, fixture, fixture.repository.primary,
		"rev-parse", "refs/heads/rebase-unrelated"); head != first {
		t.Fatalf("unrelated ref = %q, want unchanged %q", head, first)
	}
}

func TestRegistry_ReconcilesCleanRebaseCrashBeforeProofCompletion(t *testing.T) {
	fixture := newIntegrationFixture(t)
	candidateHead := commitIntegrationFile(t, fixture, fixture.candidate.CanonicalPath,
		"component.txt", "component\n")
	targetHead := commitIntegrationFile(t, fixture, fixture.target.CanonicalPath,
		"target.txt", "target\n")
	request := fixture.request("integration-clean-rebase-crash", application.IntegrationRebase, candidateHead, targetHead)
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

	result, err := newLifecycleRegistry(t, fixture.repository).
		ApplyIntegrationCandidate(context.Background(), request)
	if err != nil || result.Outcome != application.IntegrationApplied {
		t.Fatalf("ApplyIntegrationCandidate(clean crash recovery) = %#v, %v", result, err)
	}
}

func TestRegistry_RebaseRecoveryRejectsRewrittenResolvedCommitAfterCrash(t *testing.T) {
	fixture := newIntegrationFixture(t)
	writeIntegrationFile(t, fixture.candidate.CanonicalPath, "first.txt", "base\n")
	writeIntegrationFile(t, fixture.candidate.CanonicalPath, "second.txt", "base\n")
	commitIntegrationChanges(t, fixture, fixture.candidate.CanonicalPath, "first.txt", "second.txt")
	sharedBase := integrationGitOutput(t, fixture, fixture.candidate.CanonicalPath, "rev-parse", "HEAD")
	runGit(t, fixture.repository.gitExecutable, "--no-optional-locks", "-C", fixture.target.CanonicalPath,
		"reset", "--hard", sharedBase)
	_ = commitIntegrationFile(t, fixture, fixture.candidate.CanonicalPath, "first.txt", "candidate-one\n")
	candidateHead := commitIntegrationFile(t, fixture, fixture.candidate.CanonicalPath, "second.txt", "candidate-two\n")
	writeIntegrationFile(t, fixture.target.CanonicalPath, "first.txt", "target-one\n")
	writeIntegrationFile(t, fixture.target.CanonicalPath, "second.txt", "target-two\n")
	commitIntegrationChanges(t, fixture, fixture.target.CanonicalPath, "first.txt", "second.txt")
	targetHead := integrationGitOutput(t, fixture, fixture.target.CanonicalPath, "rev-parse", "HEAD")
	request := fixture.request("integration-rewrite-resolved-crash", application.IntegrationRebase, candidateHead, targetHead)
	request.Candidate.BaseRevision = sharedBase
	writeServerRebaseProofForTest(t, fixture, request, "")
	targetRef := integrationGitOutput(t, fixture, fixture.target.CanonicalPath, "symbolic-ref", "HEAD")
	runGit(t, fixture.repository.gitExecutable, "--no-optional-locks", "-C", fixture.target.CanonicalPath,
		"symbolic-ref", integrationReceiptRefForTest("target", request), targetRef)
	runGit(t, fixture.repository.gitExecutable, "--no-optional-locks", "-C", fixture.target.CanonicalPath,
		"update-ref", integrationRebaseProofRefForTest(request), candidateHead)
	runIntegrationGitExpectFailure(t, fixture.repository.gitExecutable,
		"--no-optional-locks", "-C", fixture.target.CanonicalPath,
		"-c", "core.hooksPath=/dev/null", "-c", "commit.gpgSign=false",
		"-c", "user.name=DevCrew Integration", "-c", "user.email=integration@example.invalid",
		"rebase", "--no-autostash", "--no-stat", "--reapply-cherry-picks", "--keep-empty",
		"--committer-date-is-author-date", "--onto", targetHead, sharedBase,
		strings.TrimPrefix(integrationRebaseProofRefForTest(request), "refs/heads/"))
	restarted := newLifecycleRegistry(t, fixture.repository)
	conflicted, err := restarted.ApplyIntegrationCandidate(context.Background(), request)
	if err != nil || conflicted.Outcome != application.IntegrationConflicted {
		t.Fatalf("ApplyIntegrationCandidate(first conflict) = %#v, %v", conflicted, err)
	}
	if err := os.WriteFile(filepath.Join(fixture.target.CanonicalPath, "first.txt"), []byte("resolved-one\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runGit(t, fixture.repository.gitExecutable, "--no-optional-locks", "-C", fixture.target.CanonicalPath,
		"add", "--", "first.txt")
	runIntegrationGitExpectFailure(t, fixture.repository.gitExecutable,
		"--no-optional-locks", "-C", fixture.target.CanonicalPath,
		"-c", "core.hooksPath=/dev/null", "-c", "core.fsmonitor=false", "-c", "commit.gpgSign=false",
		"-c", "core.editor=true", "-c", "user.name=DevCrew Integration",
		"-c", "user.email=integration@example.invalid", "rebase", "--continue")
	current := integrationGitOutput(t, fixture, fixture.target.CanonicalPath, "rev-parse", "HEAD")
	tree := integrationGitOutput(t, fixture, fixture.target.CanonicalPath, "rev-parse", current+"^{tree}")
	parent := integrationGitOutput(t, fixture, fixture.target.CanonicalPath, "rev-parse", current+"^")
	forged := integrationGitOutput(t, fixture, fixture.target.CanonicalPath,
		"-c", fixtureAuthorName, "-c", fixtureAuthorEmail,
		"commit-tree", tree, "-p", parent, "-m", "rewritten resolved commit")
	runGit(t, fixture.repository.gitExecutable, "--no-optional-locks", "-C", fixture.target.CanonicalPath,
		"update-ref", "HEAD", forged, current)
	recovery := request
	recovery.OperationID = "integration-rewrite-resolved-recovery"
	recovery.RecoveryOperationID = request.OperationID
	if _, err := restarted.ApplyIntegrationCandidate(context.Background(), recovery); err == nil {
		t.Fatal("ApplyIntegrationCandidate(crashed second conflict) error = nil")
	}
	if err := os.WriteFile(filepath.Join(fixture.target.CanonicalPath, "second.txt"), []byte("resolved-two\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runGit(t, fixture.repository.gitExecutable, "--no-optional-locks", "-C", fixture.target.CanonicalPath,
		"add", "--", "second.txt")
	if _, err := restarted.ApplyIntegrationCandidate(context.Background(), recovery); err == nil {
		t.Fatal("ApplyIntegrationCandidate(rewritten resolved commit) error = nil")
	}
}

func TestRegistry_CherryPickRefusesPartialRangeBeforeTargetMutation(t *testing.T) {
	fixture := newIntegrationFixture(t)
	sharedBase := commitIntegrationFile(t, fixture, fixture.candidate.CanonicalPath, "conflict.txt", "base\n")
	runGit(t, fixture.repository.gitExecutable, "--no-optional-locks", "-C", fixture.target.CanonicalPath,
		"reset", "--hard", sharedBase)
	_ = commitIntegrationFile(t, fixture, fixture.candidate.CanonicalPath, "early.txt", "early\n")
	candidateHead := commitIntegrationFile(t, fixture, fixture.candidate.CanonicalPath, "conflict.txt", "candidate\n")
	targetHead := commitIntegrationFile(t, fixture, fixture.target.CanonicalPath, "conflict.txt", "target\n")
	request := fixture.request("integration-cherry-partial", application.IntegrationCherryPick, candidateHead, targetHead)
	request.Candidate.BaseRevision = sharedBase

	_, err := fixture.registry.ApplyIntegrationCandidate(context.Background(), request)
	if !errors.Is(err, application.ErrIntegrationMutationNotStarted) {
		t.Fatalf("ApplyIntegrationCandidate(partial cherry-pick) error = %v", err)
	}
	if head := integrationGitOutput(t, fixture, fixture.target.CanonicalPath, "rev-parse", "HEAD"); head != targetHead {
		t.Fatalf("target head = %q, want unchanged %q", head, targetHead)
	}
	if status := gitOutputAllowEmpty(t, fixture.repository.gitExecutable, "--no-optional-locks", "-C",
		fixture.target.CanonicalPath, "status", "--porcelain"); status != "" {
		t.Fatalf("target status = %q, want clean", status)
	}
}

func TestRegistry_RebasePreflightCleansTrackedSymlinkWorkspace(t *testing.T) {
	fixture := newIntegrationFixture(t)
	sentinel := filepath.Join(fixture.repository.worktreeRoot, "outside-sentinel")
	if err := os.WriteFile(sentinel, []byte("preserved\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(sentinel, filepath.Join(fixture.candidate.CanonicalPath, "tracked-link")); err != nil {
		t.Fatal(err)
	}
	commitIntegrationChanges(t, fixture, fixture.candidate.CanonicalPath, "tracked-link")
	candidateHead := integrationGitOutput(t, fixture, fixture.candidate.CanonicalPath, "rev-parse", "HEAD")
	targetHead := commitIntegrationFile(t, fixture, fixture.target.CanonicalPath, "target.txt", "target\n")
	request := fixture.request("integration-rebase-symlink-cleanup", application.IntegrationRebase, candidateHead, targetHead)

	result, err := fixture.registry.ApplyIntegrationCandidate(context.Background(), request)
	if err != nil || result.Outcome != application.IntegrationApplied {
		t.Fatalf("ApplyIntegrationCandidate(tracked symlink) = %#v, %v", result, err)
	}
	contents, err := os.ReadFile(sentinel)
	if err != nil || string(contents) != "preserved\n" {
		t.Fatalf("outside sentinel = %q, %v", contents, err)
	}
	matches, err := filepath.Glob(filepath.Join(fixture.repository.worktreeRoot,
		".comis-integration-proofs", ".rebase-workspace-*"))
	if err != nil || len(matches) != 0 {
		t.Fatalf("temporary workspaces = %v, %v", matches, err)
	}
}

func TestRegistry_RecoveryRejectsEveryUnexpectedCompletionReceipt(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(t *testing.T, fixture integrationFixture, original, recovery application.IntegrationAdapterRequest, head string)
	}{
		{name: "original applied", mutate: func(t *testing.T, fixture integrationFixture, original, _ application.IntegrationAdapterRequest, head string) {
			runGit(t, fixture.repository.gitExecutable, "--no-optional-locks", "-C", fixture.repository.primary,
				"update-ref", integrationReceiptRefForTest("applied", original), head)
		}},
		{name: "dangling original rebased", mutate: func(t *testing.T, fixture integrationFixture, original, _ application.IntegrationAdapterRequest, _ string) {
			runGit(t, fixture.repository.gitExecutable, "--no-optional-locks", "-C", fixture.target.CanonicalPath,
				"symbolic-ref", integrationReceiptRefForTest("rebased", original), "refs/heads/missing-rebased")
		}},
		{name: "recovery target", mutate: func(t *testing.T, fixture integrationFixture, _ application.IntegrationAdapterRequest, recovery application.IntegrationAdapterRequest, _ string) {
			runGit(t, fixture.repository.gitExecutable, "--no-optional-locks", "-C", fixture.target.CanonicalPath,
				"symbolic-ref", integrationReceiptRefForTest("target", recovery), "refs/heads/"+fixture.target.Branch)
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := newIntegrationFixture(t)
			candidateHead := commitIntegrationFile(t, fixture, fixture.candidate.CanonicalPath,
				"fixture.txt", "candidate\n")
			targetHead := commitIntegrationFile(t, fixture, fixture.target.CanonicalPath,
				"fixture.txt", "integration\n")
			original := fixture.request("integration-receipts-original-"+strings.ReplaceAll(test.name, " ", "-"),
				application.IntegrationRebase, candidateHead, targetHead)
			stagePreviouslyAuthorizedRebaseConflict(t, fixture, original)
			if err := os.WriteFile(filepath.Join(fixture.target.CanonicalPath, "fixture.txt"), []byte("resolved\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			runGit(t, fixture.repository.gitExecutable, "--no-optional-locks", "-C", fixture.target.CanonicalPath,
				"add", "--", "fixture.txt")
			recovery := original
			recovery.OperationID = "integration-receipts-recovery-" + strings.ReplaceAll(test.name, " ", "-")
			recovery.RecoveryOperationID = original.OperationID
			test.mutate(t, fixture, original, recovery, targetHead)

			if _, err := fixture.registry.ApplyIntegrationCandidate(context.Background(), recovery); err == nil {
				t.Fatal("ApplyIntegrationCandidate(unexpected receipt) error = nil")
			}
			if head := integrationGitOutput(t, fixture, fixture.repository.primary,
				"rev-parse", "refs/heads/"+fixture.target.Branch); head != targetHead {
				t.Fatalf("target head = %q, want unchanged %q", head, targetHead)
			}
		})
	}
}
