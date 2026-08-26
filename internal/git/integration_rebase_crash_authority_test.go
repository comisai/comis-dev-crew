package git_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/comisai/comis-dev-crew/internal/application"
)

func TestRegistry_InterruptedConflictRejectsPostCrashProtectedChanges(t *testing.T) {
	fixture := newIntegrationFixture(t)
	candidateHead := commitIntegrationFile(t, fixture, fixture.candidate.CanonicalPath,
		"fixture.txt", "candidate\n")
	_ = commitIntegrationFile(t, fixture, fixture.target.CanonicalPath,
		"protected.txt", "protected\n")
	targetHead := commitIntegrationFile(t, fixture, fixture.target.CanonicalPath,
		"fixture.txt", "integration\n")
	request := fixture.request("integration-rebase-post-crash-protected",
		application.IntegrationRebase, candidateHead, targetHead)
	startInterruptedRebaseConflict(t, fixture, request, candidateHead, targetHead)

	if err := os.WriteFile(filepath.Join(fixture.target.CanonicalPath, "protected.txt"),
		[]byte("unrelated\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runGit(t, fixture.repository.gitExecutable, "--no-optional-locks", "-C",
		fixture.target.CanonicalPath, "add", "--", "protected.txt")

	if _, err := newLifecycleRegistry(t, fixture.repository).
		ApplyIntegrationCandidate(context.Background(), request); err == nil {
		t.Fatal("ApplyIntegrationCandidate(post-crash protected change) error = nil")
	}
	runIntegrationGitExpectFailure(t, fixture.repository.gitExecutable,
		"--no-optional-locks", "-C", fixture.repository.primary,
		"show-ref", "--verify", "--quiet", integrationReceiptRefForTest("conflicted", request))
	if head := integrationGitOutput(t, fixture, fixture.repository.primary,
		"rev-parse", "refs/heads/"+fixture.target.Branch); head != targetHead {
		t.Fatalf("target head = %q, want unchanged %q", head, targetHead)
	}
}

func TestRegistry_RejectsLaterConflictCrashSequenceBeforeMutation(t *testing.T) {
	fixture := newIntegrationFixture(t)
	_ = commitIntegrationFile(t, fixture, fixture.candidate.CanonicalPath,
		"fixture.txt", "candidate-one\n")
	candidateHead := commitIntegrationFile(t, fixture, fixture.candidate.CanonicalPath,
		"fixture.txt", "candidate-two\n")
	targetHead := commitIntegrationFile(t, fixture, fixture.target.CanonicalPath,
		"fixture.txt", "integration\n")
	request := fixture.request("integration-rebase-later-conflict-crash",
		application.IntegrationRebase, candidateHead, targetHead)
	_, err := fixture.registry.ApplyIntegrationCandidate(context.Background(), request)
	if !errors.Is(err, application.ErrIntegrationMutationNotStarted) {
		t.Fatalf("ApplyIntegrationCandidate(later conflict sequence) error = %v", err)
	}
	assertRebaseProofBoundPreservedTarget(t, fixture, request, targetHead)
}

func TestRegistry_RebaseRejectsSequentialSubsumptionBeforeMutation(t *testing.T) {
	fixture := newIntegrationFixture(t)
	baseBody := "one\ntwo\nthree\nfour\nfive\nsix\nseven\neight\nnine\n"
	sharedBase := commitIntegrationFile(t, fixture, fixture.candidate.CanonicalPath,
		"sequence.txt", baseBody)
	runGit(t, fixture.repository.gitExecutable, "--no-optional-locks", "-C",
		fixture.target.CanonicalPath, "reset", "--hard", sharedBase)
	_ = commitIntegrationFile(t, fixture, fixture.candidate.CanonicalPath,
		"sequence.txt", "ONE\ntwo\nthree\nfour\nfive\nsix\nseven\neight\nnine\n")
	candidateHead := commitIntegrationFile(t, fixture, fixture.candidate.CanonicalPath,
		"sequence.txt", "ONE\ntwo\nthree\nFOUR\nfive\nsix\nseven\neight\nnine\n")
	targetHead := commitIntegrationFile(t, fixture, fixture.target.CanonicalPath,
		"sequence.txt", "one\ntwo\nthree\nFOUR\nfive\nsix\nseven\neight\nnine\n")
	request := fixture.request("integration-rebase-sequential-subsumption",
		application.IntegrationRebase, candidateHead, targetHead)
	request.Candidate.BaseRevision = sharedBase

	_, err := fixture.registry.ApplyIntegrationCandidate(context.Background(), request)
	if !errors.Is(err, application.ErrIntegrationMutationNotStarted) {
		t.Fatalf("ApplyIntegrationCandidate(sequential subsumption) error = %v", err)
	}
	assertRebaseProofBoundPreservedTarget(t, fixture, request, targetHead)
}

func startInterruptedRebaseConflict(
	t *testing.T,
	fixture integrationFixture,
	request application.IntegrationAdapterRequest,
	candidateHead string,
	targetHead string,
) {
	t.Helper()
	writeServerRebaseProofForTest(t, fixture, request, "")
	targetRef := integrationGitOutput(t, fixture, fixture.target.CanonicalPath, "symbolic-ref", "HEAD")
	runGit(t, fixture.repository.gitExecutable, "--no-optional-locks", "-C",
		fixture.target.CanonicalPath, "symbolic-ref", integrationReceiptRefForTest("target", request), targetRef)
	runGit(t, fixture.repository.gitExecutable, "--no-optional-locks", "-C",
		fixture.target.CanonicalPath, "update-ref", integrationRebaseProofRefForTest(request), candidateHead)
	runIntegrationGitExpectFailure(t, fixture.repository.gitExecutable,
		"--no-optional-locks", "-C", fixture.target.CanonicalPath,
		"-c", "core.hooksPath=/dev/null", "-c", "commit.gpgSign=false",
		"-c", "user.name=DevCrew Integration", "-c", "user.email=integration@example.invalid",
		"rebase", "--no-autostash", "--no-stat", "--onto", targetHead,
		request.Candidate.BaseRevision,
		integrationRebaseProofRefForTest(request)[len("refs/heads/"):])
}
