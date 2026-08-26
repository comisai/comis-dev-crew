package git_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"reflect"
	"strings"
	"testing"

	"github.com/comisai/comis-dev-crew/internal/application"
)

func TestRegistry_ReceiptOnlyCompletedRebaseRejectsDanglingOperationReceipts(t *testing.T) {
	for _, outcome := range []string{"target", "rebased", "proof"} {
		t.Run(outcome, func(t *testing.T) {
			fixture := newIntegrationFixture(t)
			candidateHead := commitIntegrationFile(t, fixture, fixture.candidate.CanonicalPath,
				"candidate.txt", "candidate\n")
			targetHead := commitIntegrationFile(t, fixture, fixture.target.CanonicalPath,
				"target.txt", "target\n")
			request := fixture.request("integration-receipt-only-dangling-"+outcome,
				application.IntegrationRebase, candidateHead, targetHead)
			applied, err := fixture.registry.ApplyIntegrationCandidate(context.Background(), request)
			if err != nil {
				t.Fatalf("ApplyIntegrationCandidate(initial) error = %v", err)
			}
			appliedRef := integrationReceiptRefForTest("applied", request)
			runGit(t, fixture.repository.gitExecutable, "--no-optional-locks", "-C", fixture.target.CanonicalPath,
				"update-ref", "-d", appliedRef, applied.ResultingHead)
			receipt := integrationReceiptRefForTest(outcome, request)
			if outcome == "proof" {
				receipt = integrationRebaseProofRefForTest(request)
			} else if outcome == "rebased" {
				runGit(t, fixture.repository.gitExecutable, "--no-optional-locks", "-C", fixture.target.CanonicalPath,
					"update-ref", "-d", receipt, applied.ResultingHead)
			}
			runGit(t, fixture.repository.gitExecutable, "--no-optional-locks", "-C", fixture.target.CanonicalPath,
				"symbolic-ref", receipt, "refs/heads/missing-receipt-target")
			headBefore := integrationGitOutput(t, fixture, fixture.target.CanonicalPath, "rev-parse", "HEAD")
			request.ReceiptOnly = true

			if _, err := fixture.registry.ApplyIntegrationCandidate(context.Background(), request); err == nil {
				t.Fatal("ApplyIntegrationCandidate(dangling receipt) error = nil")
			}
			if head := integrationGitOutput(t, fixture, fixture.target.CanonicalPath, "rev-parse", "HEAD"); head != headBefore {
				t.Fatalf("target head = %q, want unchanged %q", head, headBefore)
			}
		})
	}
}

func TestRegistry_ReceiptOnlyReattachesAnExactlyProvenCompletedRebase(t *testing.T) {
	fixture := newIntegrationFixture(t)
	candidateHead := commitIntegrationFile(t, fixture, fixture.candidate.CanonicalPath,
		"candidate.txt", "candidate\n")
	targetHead := commitIntegrationFile(t, fixture, fixture.target.CanonicalPath,
		"target.txt", "target\n")
	request := fixture.request("integration-receipt-only-reattach",
		application.IntegrationRebase, candidateHead, targetHead)
	applied, err := fixture.registry.ApplyIntegrationCandidate(context.Background(), request)
	if err != nil {
		t.Fatalf("ApplyIntegrationCandidate(initial) error = %v", err)
	}
	appliedRef := integrationReceiptRefForTest("applied", request)
	proofRef := integrationRebaseProofRefForTest(request)
	runGit(t, fixture.repository.gitExecutable, "--no-optional-locks", "-C", fixture.target.CanonicalPath,
		"update-ref", "-d", appliedRef, applied.ResultingHead)
	runGit(t, fixture.repository.gitExecutable, "--no-optional-locks", "-C", fixture.target.CanonicalPath,
		"update-ref", proofRef, applied.ResultingHead)
	runGit(t, fixture.repository.gitExecutable, "--no-optional-locks", "-C", fixture.target.CanonicalPath,
		"symbolic-ref", "HEAD", proofRef)
	request.ReceiptOnly = true

	reconciled, err := fixture.registry.ApplyIntegrationCandidate(context.Background(), request)
	if err != nil || !reflect.DeepEqual(reconciled, applied) {
		t.Fatalf("ApplyIntegrationCandidate(receipt-only reattach) = %#v, %v", reconciled, err)
	}
	if branch := integrationGitOutput(t, fixture, fixture.target.CanonicalPath, "symbolic-ref", "--short", "HEAD"); branch != fixture.target.Branch {
		t.Fatalf("reattached branch = %q, want %q", branch, fixture.target.Branch)
	}
	if proofHead := integrationGitOutput(t, fixture, fixture.target.CanonicalPath, "rev-parse", proofRef); proofHead != applied.ResultingHead {
		t.Fatalf("proof ref = %q, want preserved %q", proofHead, applied.ResultingHead)
	}
}

func TestRegistry_RebaseProofBoundsFailBeforeGitMutation(t *testing.T) {
	t.Run("patch bytes", func(t *testing.T) {
		fixture := newIntegrationFixture(t)
		candidateHead := commitIntegrationFile(t, fixture, fixture.candidate.CanonicalPath,
			"large.txt", strings.Repeat("x", 17*1024*1024))
		targetHead := commitIntegrationFile(t, fixture, fixture.target.CanonicalPath,
			"target.txt", "target\n")
		request := fixture.request("integration-rebase-large-patch",
			application.IntegrationRebase, candidateHead, targetHead)

		_, err := fixture.registry.ApplyIntegrationCandidate(context.Background(), request)
		if !errors.Is(err, application.ErrIntegrationMutationNotStarted) {
			t.Fatalf("ApplyIntegrationCandidate(large patch) error = %v", err)
		}
		assertRebaseProofBoundPreservedTarget(t, fixture, request, targetHead)
	})

	t.Run("commit count", func(t *testing.T) {
		fixture := newIntegrationFixture(t)
		candidateHead := importEmptyIntegrationCommits(t, fixture, 4097)
		targetHead := commitIntegrationFile(t, fixture, fixture.target.CanonicalPath,
			"target.txt", "target\n")
		request := fixture.request("integration-rebase-many-commits",
			application.IntegrationRebase, candidateHead, targetHead)

		_, err := fixture.registry.ApplyIntegrationCandidate(context.Background(), request)
		if !errors.Is(err, application.ErrIntegrationMutationNotStarted) {
			t.Fatalf("ApplyIntegrationCandidate(many commits) error = %v", err)
		}
		assertRebaseProofBoundPreservedTarget(t, fixture, request, targetHead)
	})
}

func importEmptyIntegrationCommits(t *testing.T, fixture integrationFixture, count int) string {
	t.Helper()
	ref := "refs/heads/integration-oversized-history"
	var stream strings.Builder
	for index := 1; index <= count; index++ {
		fmt.Fprintf(&stream, "commit %s\nmark :%d\nauthor Fixture <fixture@example.invalid> 1800000000 +0000\n", ref, index)
		stream.WriteString("committer Fixture <fixture@example.invalid> 1800000000 +0000\ndata 1\nx\nfrom ")
		if index == 1 {
			stream.WriteString(fixture.base)
		} else {
			fmt.Fprintf(&stream, ":%d", index-1)
		}
		stream.WriteString("\n\n")
	}
	stream.WriteString("done\n")
	command := exec.Command(fixture.repository.gitExecutable, "--no-optional-locks", "-C",
		fixture.repository.primary, "fast-import", "--quiet")
	command.Env = gitTestEnvironment(nil)
	command.Stdin = bytes.NewBufferString(stream.String())
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("fast-import: %v: %s", err, output)
	}
	head := integrationGitOutput(t, fixture, fixture.repository.primary, "rev-parse", ref)
	runGit(t, fixture.repository.gitExecutable, "--no-optional-locks", "-C", fixture.candidate.CanonicalPath,
		"reset", "--hard", head)
	return head
}

func assertRebaseProofBoundPreservedTarget(
	t *testing.T,
	fixture integrationFixture,
	request application.IntegrationAdapterRequest,
	targetHead string,
) {
	t.Helper()
	if head := integrationGitOutput(t, fixture, fixture.target.CanonicalPath, "rev-parse", "HEAD"); head != targetHead {
		t.Fatalf("target head = %q, want unchanged %q", head, targetHead)
	}
	for _, reference := range []string{
		integrationReceiptRefForTest("target", request), integrationReceiptRefForTest("rebased", request),
		integrationRebaseProofRefForTest(request),
	} {
		command := exec.Command(fixture.repository.gitExecutable, "--no-optional-locks", "-C",
			fixture.repository.primary, "show-ref", "--verify", "--quiet", reference)
		command.Env = gitTestEnvironment(nil)
		if err := command.Run(); err == nil {
			t.Fatalf("pre-mutation proof ref %q exists", reference)
		}
	}
}
