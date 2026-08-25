package git_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/comisai/comis-dev-crew/internal/application"
)

func TestRegistry_RecoversResolvedRebaseConflictAndReattachesExactTarget(t *testing.T) {
	fixture := newIntegrationFixture(t)
	candidateHead := commitIntegrationFile(
		t, fixture, fixture.candidate.CanonicalPath, "fixture.txt", "candidate\n",
	)
	targetHead := commitIntegrationFile(
		t, fixture, fixture.target.CanonicalPath, "fixture.txt", "integration\n",
	)
	request := fixture.request("integration-rebase-conflict", application.IntegrationRebase, candidateHead, targetHead)
	stagePreviouslyAuthorizedRebaseConflict(t, fixture, request)
	if err := os.WriteFile(filepath.Join(fixture.target.CanonicalPath, "fixture.txt"), []byte("resolved\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runGit(t, fixture.repository.gitExecutable, "--no-optional-locks", "-C", fixture.target.CanonicalPath,
		"add", "--", "fixture.txt")

	recovery := request
	recovery.OperationID = "integration-rebase-resolution"
	recovery.RecoveryOperationID = request.OperationID
	restarted := newLifecycleRegistry(t, fixture.repository)
	result, err := restarted.ApplyIntegrationCandidate(context.Background(), recovery)
	if err != nil {
		t.Fatalf("ApplyIntegrationCandidate(recovery) error = %v", err)
	}
	if result.Outcome != application.IntegrationApplied || result.PreviousHead != targetHead ||
		result.ResultingHead == "" || result.ResultingHead == targetHead {
		t.Fatalf("recovery result = %#v", result)
	}
	if branch := integrationGitOutput(t, fixture, fixture.target.CanonicalPath, "symbolic-ref", "--short", "HEAD"); branch != fixture.target.Branch {
		t.Fatalf("recovered target branch = %q, want %q", branch, fixture.target.Branch)
	}
	if head := integrationGitOutput(t, fixture, fixture.target.CanonicalPath, "rev-parse", "HEAD"); head != result.ResultingHead {
		t.Fatalf("recovered target head = %q, want %q", head, result.ResultingHead)
	}
	appliedRef := integrationReceiptRefForTest("applied", recovery)
	rebasedRef := integrationReceiptRefForTest("rebased", recovery)
	runGit(t, fixture.repository.gitExecutable, "--no-optional-locks", "-C", fixture.repository.primary,
		"update-ref", "-d", appliedRef)
	runGit(t, fixture.repository.gitExecutable, "--no-optional-locks", "-C", fixture.repository.primary,
		"update-ref", rebasedRef, targetHead, result.ResultingHead)
	if _, err := restarted.ApplyIntegrationCandidate(context.Background(), recovery); err == nil {
		t.Fatal("ApplyIntegrationCandidate(contradictory rebased receipt) error = nil")
	}
	runGit(t, fixture.repository.gitExecutable, "--no-optional-locks", "-C", fixture.repository.primary,
		"update-ref", rebasedRef, result.ResultingHead, targetHead)
	replayed, err := restarted.ApplyIntegrationCandidate(context.Background(), recovery)
	if err != nil || !reflect.DeepEqual(replayed, result) {
		t.Fatalf("ApplyIntegrationCandidate(recovery replay) = %#v, %v", replayed, err)
	}
}

func TestRegistry_ReconcilesInterruptedRebaseConflictBeforeReceipt(t *testing.T) {
	fixture := newIntegrationFixture(t)
	candidateHead := commitIntegrationFile(t, fixture, fixture.candidate.CanonicalPath, "fixture.txt", "candidate\n")
	targetHead := commitIntegrationFile(t, fixture, fixture.target.CanonicalPath, "fixture.txt", "integration\n")
	request := fixture.request("integration-rebase-interrupted-conflict", application.IntegrationRebase, candidateHead, targetHead)
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
		"rebase", "--no-autostash", "--no-stat", "--onto", targetHead, request.Candidate.BaseRevision,
		strings.TrimPrefix(integrationRebaseProofRefForTest(request), "refs/heads/"))

	restarted := newLifecycleRegistry(t, fixture.repository)
	runGit(t, fixture.repository.gitExecutable, "--no-optional-locks", "-C", fixture.target.CanonicalPath,
		"update-ref", targetRef, candidateHead, targetHead)
	if _, err := restarted.ApplyIntegrationCandidate(context.Background(), request); err == nil {
		t.Fatal("ApplyIntegrationCandidate(conflict with moved target) error = nil")
	}
	runGit(t, fixture.repository.gitExecutable, "--no-optional-locks", "-C", fixture.target.CanonicalPath,
		"update-ref", targetRef, targetHead, candidateHead)
	runGit(t, fixture.repository.gitExecutable, "--no-optional-locks", "-C", fixture.target.CanonicalPath,
		"update-ref", "ORIG_HEAD", targetHead, candidateHead)
	if _, err := restarted.ApplyIntegrationCandidate(context.Background(), request); err == nil {
		t.Fatal("ApplyIntegrationCandidate(conflict with changed origin) error = nil")
	}
	runGit(t, fixture.repository.gitExecutable, "--no-optional-locks", "-C", fixture.target.CanonicalPath,
		"update-ref", "ORIG_HEAD", candidateHead, targetHead)

	result, err := restarted.ApplyIntegrationCandidate(context.Background(), request)
	if err != nil || result.Outcome != application.IntegrationConflicted ||
		result.PreviousHead != targetHead || !reflect.DeepEqual(result.ConflictPaths, []string{"fixture.txt"}) {
		t.Fatalf("ApplyIntegrationCandidate(interrupted conflict) = %#v, %v", result, err)
	}
	replayed, err := restarted.ApplyIntegrationCandidate(context.Background(), request)
	if err != nil || !reflect.DeepEqual(replayed, result) {
		t.Fatalf("ApplyIntegrationCandidate(interrupted conflict replay) = %#v, %v", replayed, err)
	}
	if err := os.WriteFile(filepath.Join(fixture.target.CanonicalPath, "fixture.txt"), []byte("resolved\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runGit(t, fixture.repository.gitExecutable, "--no-optional-locks", "-C", fixture.target.CanonicalPath,
		"add", "--", "fixture.txt")
	recovery := request
	recovery.OperationID = "integration-rebase-interrupted-conflict-recovery"
	recovery.RecoveryOperationID = request.OperationID
	applied, err := restarted.ApplyIntegrationCandidate(context.Background(), recovery)
	if err != nil || applied.Outcome != application.IntegrationApplied {
		t.Fatalf("ApplyIntegrationCandidate(interrupted conflict recovery) = %#v, %v", applied, err)
	}
}

func TestRegistry_ReconcilesCompletedRebaseBeforeConflictReceipt(t *testing.T) {
	fixture := newIntegrationFixture(t)
	candidateHead := commitIntegrationFile(t, fixture, fixture.candidate.CanonicalPath, "fixture.txt", "candidate\n")
	targetHead := commitIntegrationFile(t, fixture, fixture.target.CanonicalPath, "fixture.txt", "integration\n")
	request := fixture.request("integration-rebase-completed-before-receipt", application.IntegrationRebase, candidateHead, targetHead)
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
		"rebase", "--no-autostash", "--no-stat", "--onto", targetHead, request.Candidate.BaseRevision,
		strings.TrimPrefix(integrationRebaseProofRefForTest(request), "refs/heads/"))
	if err := os.WriteFile(filepath.Join(fixture.target.CanonicalPath, "fixture.txt"), []byte("resolved\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runGit(t, fixture.repository.gitExecutable, "--no-optional-locks", "-C", fixture.target.CanonicalPath,
		"add", "--", "fixture.txt")
	runGit(t, fixture.repository.gitExecutable,
		"--no-optional-locks", "-C", fixture.target.CanonicalPath,
		"-c", "core.hooksPath=/dev/null", "-c", "commit.gpgSign=false", "-c", "core.editor=true",
		"-c", "user.name=DevCrew Integration", "-c", "user.email=integration@example.invalid",
		"rebase", "--continue")
	rebasedHead := integrationGitOutput(t, fixture, fixture.target.CanonicalPath, "rev-parse", "HEAD")
	writeServerRebaseProofForTest(t, fixture, request, rebasedHead, candidateHead)

	restarted := newLifecycleRegistry(t, fixture.repository)
	dirtyPath := filepath.Join(fixture.target.CanonicalPath, "untracked.txt")
	if err := os.WriteFile(dirtyPath, []byte("untracked\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := restarted.ApplyIntegrationCandidate(context.Background(), request); err == nil {
		t.Fatal("ApplyIntegrationCandidate(dirty completed rebase) error = nil")
	}
	if err := os.Remove(dirtyPath); err != nil {
		t.Fatal(err)
	}
	runGit(t, fixture.repository.gitExecutable, "--no-optional-locks", "-C", fixture.target.CanonicalPath,
		"update-ref", targetRef, candidateHead, targetHead)
	if _, err := restarted.ApplyIntegrationCandidate(context.Background(), request); err == nil {
		t.Fatal("ApplyIntegrationCandidate(moved target before reconciliation) error = nil")
	}
	runGit(t, fixture.repository.gitExecutable, "--no-optional-locks", "-C", fixture.target.CanonicalPath,
		"update-ref", targetRef, targetHead, candidateHead)
	runGit(t, fixture.repository.gitExecutable, "--no-optional-locks", "-C", fixture.target.CanonicalPath,
		"update-ref", "refs/tags/invalid-recovery-attachment", rebasedHead)
	runGit(t, fixture.repository.gitExecutable, "--no-optional-locks", "-C", fixture.target.CanonicalPath,
		"symbolic-ref", "HEAD", "refs/tags/invalid-recovery-attachment")
	if _, err := restarted.ApplyIntegrationCandidate(context.Background(), request); err == nil {
		t.Fatal("ApplyIntegrationCandidate(invalid recovery attachment) error = nil")
	}
	runGit(t, fixture.repository.gitExecutable, "--no-optional-locks", "-C", fixture.target.CanonicalPath,
		"symbolic-ref", "HEAD", "refs/heads/missing-recovery-attachment")
	if _, err := restarted.ApplyIntegrationCandidate(context.Background(), request); err == nil {
		t.Fatal("ApplyIntegrationCandidate(missing recovery head) error = nil")
	}
	runGit(t, fixture.repository.gitExecutable, "--no-optional-locks", "-C", fixture.target.CanonicalPath,
		"checkout", "--detach", "--no-guess", rebasedHead)

	result, err := restarted.ApplyIntegrationCandidate(context.Background(), request)
	if err != nil || result.Outcome != application.IntegrationApplied || result.PreviousHead != targetHead ||
		result.ResultingHead == "" || result.ResultingHead == targetHead {
		t.Fatalf("ApplyIntegrationCandidate(completed before receipt) = %#v, %v", result, err)
	}
	if branch := integrationGitOutput(t, fixture, fixture.target.CanonicalPath, "symbolic-ref", "HEAD"); branch != targetRef {
		t.Fatalf("reconciled target ref = %q, want %q", branch, targetRef)
	}
	if head := integrationGitOutput(t, fixture, fixture.repository.primary, "rev-parse",
		integrationReceiptRefForTest("rebased", request)); head != result.ResultingHead {
		t.Fatalf("rebased receipt = %q, want %q", head, result.ResultingHead)
	}
	runIntegrationGitExpectFailure(t, fixture.repository.gitExecutable, "--no-optional-locks", "-C",
		fixture.repository.primary, "show-ref", "--verify", "--quiet", integrationRebaseProofRefForTest(request))
}

func TestRegistry_ReconcilesCompletedRecoveryBeforeRebasedReceipt(t *testing.T) {
	fixture := newIntegrationFixture(t)
	candidateHead := commitIntegrationFile(t, fixture, fixture.candidate.CanonicalPath, "fixture.txt", "candidate\n")
	targetHead := commitIntegrationFile(t, fixture, fixture.target.CanonicalPath, "fixture.txt", "integration\n")
	request := fixture.request("integration-rebase-completed-conflict", application.IntegrationRebase, candidateHead, targetHead)
	stagePreviouslyAuthorizedRebaseConflict(t, fixture, request)
	if err := os.WriteFile(filepath.Join(fixture.target.CanonicalPath, "fixture.txt"), []byte("resolved\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runGit(t, fixture.repository.gitExecutable, "--no-optional-locks", "-C", fixture.target.CanonicalPath,
		"add", "--", "fixture.txt")
	runGit(t, fixture.repository.gitExecutable,
		"--no-optional-locks", "-C", fixture.target.CanonicalPath,
		"-c", "core.hooksPath=/dev/null", "-c", "commit.gpgSign=false", "-c", "core.editor=true",
		"-c", "user.name=DevCrew Integration", "-c", "user.email=integration@example.invalid",
		"rebase", "--continue")
	rebasedHead := integrationGitOutput(t, fixture, fixture.target.CanonicalPath, "rev-parse", "HEAD")
	writeServerRebaseProofForTest(t, fixture, request, rebasedHead, candidateHead)
	recovery := request
	recovery.OperationID = "integration-rebase-completed-recovery"
	recovery.RecoveryOperationID = request.OperationID

	restarted := newLifecycleRegistry(t, fixture.repository)
	result, err := restarted.ApplyIntegrationCandidate(context.Background(), recovery)
	if err != nil || result.Outcome != application.IntegrationApplied || result.PreviousHead != targetHead ||
		result.ResultingHead == "" || result.ResultingHead == targetHead {
		t.Fatalf("ApplyIntegrationCandidate(completed recovery) = %#v, %v", result, err)
	}
	if branch := integrationGitOutput(t, fixture, fixture.target.CanonicalPath, "symbolic-ref", "--short", "HEAD"); branch != fixture.target.Branch {
		t.Fatalf("reconciled target branch = %q, want %q", branch, fixture.target.Branch)
	}
	if head := integrationGitOutput(t, fixture, fixture.repository.primary, "rev-parse",
		integrationReceiptRefForTest("rebased", recovery)); head != result.ResultingHead {
		t.Fatalf("recovery rebased receipt = %q, want %q", head, result.ResultingHead)
	}
	runIntegrationGitExpectFailure(t, fixture.repository.gitExecutable, "--no-optional-locks", "-C",
		fixture.repository.primary, "show-ref", "--verify", "--quiet", integrationRebaseProofRefForTest(request))
}

func TestRegistry_RebaseRecoveryRefusesUnresolvedOrChangedTarget(t *testing.T) {
	for _, test := range []struct {
		name          string
		mutateState   func(t *testing.T, fixture integrationFixture, candidateHead, targetHead string)
		mutateRequest func(*application.IntegrationAdapterRequest)
	}{
		{name: "unresolved conflict"},
		{name: "changed target branch", mutateState: func(t *testing.T, fixture integrationFixture, candidateHead, _ string) {
			runGit(t, fixture.repository.gitExecutable, "--no-optional-locks", "-C", fixture.repository.primary,
				"update-ref", "refs/heads/"+fixture.target.Branch, candidateHead)
		}},
		{name: "missing rebase identity", mutateState: func(t *testing.T, fixture integrationFixture, _, _ string) {
			runGit(t, fixture.repository.gitExecutable, "--no-optional-locks", "-C", fixture.target.CanonicalPath,
				"update-ref", "-d", "REBASE_HEAD")
		}},
		{name: "changed rebase origin", mutateState: func(t *testing.T, fixture integrationFixture, _, targetHead string) {
			runGit(t, fixture.repository.gitExecutable, "--no-optional-locks", "-C", fixture.target.CanonicalPath,
				"update-ref", "ORIG_HEAD", targetHead)
		}},
		{name: "changed conflict commit", mutateState: func(t *testing.T, fixture integrationFixture, _, targetHead string) {
			runGit(t, fixture.repository.gitExecutable, "--no-optional-locks", "-C", fixture.target.CanonicalPath,
				"update-ref", "REBASE_HEAD", targetHead)
		}},
		{name: "changed detached head", mutateState: func(t *testing.T, fixture integrationFixture, candidateHead, _ string) {
			runGit(t, fixture.repository.gitExecutable, "--no-optional-locks", "-C", fixture.target.CanonicalPath,
				"update-ref", "HEAD", candidateHead)
		}},
		{name: "altered target receipt", mutateState: func(t *testing.T, fixture integrationFixture, _, _ string) {
			receipt := integrationGitOutput(t, fixture, fixture.repository.primary,
				"for-each-ref", "--format=%(refname)", "refs/comis/integration/target")
			runGit(t, fixture.repository.gitExecutable, "--no-optional-locks", "-C", fixture.repository.primary,
				"symbolic-ref", receipt, "refs/heads/main")
		}},
		{name: "different recovery strategy", mutateRequest: func(request *application.IntegrationAdapterRequest) {
			request.Strategy = application.IntegrationMerge
		}},
		{name: "missing conflict operation", mutateRequest: func(request *application.IntegrationAdapterRequest) {
			request.RecoveryOperationID = "integration-rebase-missing-conflict"
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := newIntegrationFixture(t)
			candidateHead := commitIntegrationFile(t, fixture, fixture.candidate.CanonicalPath, "fixture.txt", "candidate\n")
			targetHead := commitIntegrationFile(t, fixture, fixture.target.CanonicalPath, "fixture.txt", "integration\n")
			request := fixture.request("integration-rebase-refusal", application.IntegrationRebase, candidateHead, targetHead)
			stagePreviouslyAuthorizedRebaseConflict(t, fixture, request)
			if test.mutateState != nil {
				test.mutateState(t, fixture, candidateHead, targetHead)
			}
			recovery := request
			recovery.OperationID = "integration-rebase-refusal-resolution"
			recovery.RecoveryOperationID = request.OperationID
			if test.mutateRequest != nil {
				test.mutateRequest(&recovery)
			}
			if _, err := fixture.registry.ApplyIntegrationCandidate(context.Background(), recovery); err == nil {
				t.Fatal("ApplyIntegrationCandidate(unsafe recovery) error = nil")
			}
		})
	}
}

func TestRegistry_RebaseTargetReceiptRejectsAlteredOrAmbiguousIdentity(t *testing.T) {
	for _, test := range []struct {
		name    string
		prepare func(t *testing.T, fixture integrationFixture, receipt, targetRef, targetHead string)
		wantErr bool
	}{
		{name: "identical replay", prepare: func(t *testing.T, fixture integrationFixture, receipt, targetRef, _ string) {
			runGit(t, fixture.repository.gitExecutable, "--no-optional-locks", "-C", fixture.repository.primary,
				"symbolic-ref", receipt, targetRef)
		}},
		{name: "altered symbolic identity", prepare: func(t *testing.T, fixture integrationFixture, receipt, _, _ string) {
			runGit(t, fixture.repository.gitExecutable, "--no-optional-locks", "-C", fixture.repository.primary,
				"symbolic-ref", receipt, "refs/heads/main")
		}, wantErr: true},
		{name: "dangling symbolic identity", prepare: func(t *testing.T, fixture integrationFixture, receipt, _, _ string) {
			runGit(t, fixture.repository.gitExecutable, "--no-optional-locks", "-C", fixture.repository.primary,
				"symbolic-ref", receipt, "refs/heads/missing-integration-target")
		}, wantErr: true},
		{name: "multi-hop symbolic identity", prepare: func(t *testing.T, fixture integrationFixture, receipt, targetRef, _ string) {
			alias := "refs/heads/integration-receipt-alias"
			runGit(t, fixture.repository.gitExecutable, "--no-optional-locks", "-C", fixture.repository.primary,
				"symbolic-ref", alias, targetRef)
			runGit(t, fixture.repository.gitExecutable, "--no-optional-locks", "-C", fixture.repository.primary,
				"symbolic-ref", receipt, alias)
		}, wantErr: true},
		{name: "direct ref is ambiguous", prepare: func(t *testing.T, fixture integrationFixture, receipt, _, targetHead string) {
			runGit(t, fixture.repository.gitExecutable, "--no-optional-locks", "-C", fixture.repository.primary,
				"update-ref", receipt, targetHead)
		}, wantErr: true},
		{name: "receipt ref is locked", prepare: func(t *testing.T, fixture integrationFixture, receipt, _, _ string) {
			lockIntegrationReceiptForTest(t, fixture, receipt)
		}, wantErr: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := newIntegrationFixture(t)
			candidateHead := commitIntegrationFile(t, fixture, fixture.candidate.CanonicalPath, "candidate.txt", "candidate\n")
			targetHead := commitIntegrationFile(t, fixture, fixture.target.CanonicalPath, "target.txt", "target\n")
			request := fixture.request("integration-target-receipt-"+strings.ReplaceAll(test.name, " ", "-"),
				application.IntegrationRebase, candidateHead, targetHead)
			test.prepare(t, fixture, integrationReceiptRefForTest("target", request),
				"refs/heads/"+fixture.target.Branch, targetHead)
			result, err := fixture.registry.ApplyIntegrationCandidate(context.Background(), request)
			if test.wantErr {
				if err == nil {
					t.Fatalf("ApplyIntegrationCandidate(%s) error = nil", test.name)
				}
				return
			}
			if err != nil || result.Outcome != application.IntegrationApplied {
				t.Fatalf("ApplyIntegrationCandidate(%s) = %#v, %v", test.name, result, err)
			}
		})
	}
}

func TestRegistry_RejectsDanglingOutcomeReceiptBeforeMutation(t *testing.T) {
	for _, test := range []struct {
		name     string
		outcome  string
		strategy application.IntegrationStrategy
	}{
		{name: "applied receipt", outcome: "applied", strategy: application.IntegrationMerge},
		{name: "rebased receipt", outcome: "rebased", strategy: application.IntegrationRebase},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := newIntegrationFixture(t)
			candidateHead := commitIntegrationFile(t, fixture, fixture.candidate.CanonicalPath, "candidate.txt", "candidate\n")
			targetHead := commitIntegrationFile(t, fixture, fixture.target.CanonicalPath, "target.txt", "target\n")
			request := fixture.request("integration-dangling-"+strings.ReplaceAll(test.name, " ", "-"),
				test.strategy, candidateHead, targetHead)
			runGit(t, fixture.repository.gitExecutable, "--no-optional-locks", "-C", fixture.repository.primary,
				"symbolic-ref", integrationReceiptRefForTest(test.outcome, request), "refs/heads/missing-integration-result")

			if _, err := fixture.registry.ApplyIntegrationCandidate(context.Background(), request); err == nil {
				t.Fatalf("ApplyIntegrationCandidate(dangling %s) error = nil", test.name)
			}
			if head := integrationGitOutput(t, fixture, fixture.target.CanonicalPath, "rev-parse", "HEAD"); head != targetHead {
				t.Fatalf("target head = %q, want unchanged %q", head, targetHead)
			}
			if branch := integrationGitOutput(t, fixture, fixture.target.CanonicalPath, "symbolic-ref", "HEAD"); branch != "refs/heads/"+fixture.target.Branch {
				t.Fatalf("target branch = %q, want %q", branch, "refs/heads/"+fixture.target.Branch)
			}
			if _, err := os.Stat(filepath.Join(fixture.target.CanonicalPath, "candidate.txt")); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("candidate content reached target: %v", err)
			}
			runIntegrationGitExpectFailure(t, fixture.repository.gitExecutable, "--no-optional-locks", "-C",
				fixture.repository.primary, "show-ref", "--verify", "--quiet", integrationReceiptRefForTest("target", request))
			runIntegrationGitExpectFailure(t, fixture.repository.gitExecutable, "--no-optional-locks", "-C",
				fixture.repository.primary, "show-ref", "--verify", "--quiet", integrationRebaseProofRefForTest(request))
		})
	}
}

func TestRegistry_RestartsPreparedRebaseBeforeGitStarts(t *testing.T) {
	fixture := newIntegrationFixture(t)
	candidateHead := commitIntegrationFile(t, fixture, fixture.candidate.CanonicalPath, "candidate.txt", "candidate\n")
	targetHead := commitIntegrationFile(t, fixture, fixture.target.CanonicalPath, "target.txt", "target\n")
	request := fixture.request("integration-prepared-rebase-restart", application.IntegrationRebase, candidateHead, targetHead)
	writeServerRebaseProofForTest(t, fixture, request, "")
	targetRef := integrationGitOutput(t, fixture, fixture.target.CanonicalPath, "symbolic-ref", "HEAD")
	proofRef := integrationRebaseProofRefForTest(request)
	runGit(t, fixture.repository.gitExecutable, "--no-optional-locks", "-C", fixture.target.CanonicalPath,
		"symbolic-ref", integrationReceiptRefForTest("target", request), targetRef)
	runGit(t, fixture.repository.gitExecutable, "--no-optional-locks", "-C", fixture.target.CanonicalPath,
		"update-ref", proofRef, candidateHead)
	runGit(t, fixture.repository.gitExecutable, "--no-optional-locks", "-C", fixture.target.CanonicalPath,
		"checkout", "--no-guess", strings.TrimPrefix(proofRef, "refs/heads/"))
	runGit(t, fixture.repository.gitExecutable, "--no-optional-locks", "-C", fixture.target.CanonicalPath,
		"update-ref", "-d", "ORIG_HEAD")

	result, err := fixture.registry.ApplyIntegrationCandidate(context.Background(), request)
	if err != nil || result.Outcome != application.IntegrationApplied || result.PreviousHead != targetHead ||
		result.ResultingHead == "" || result.ResultingHead == targetHead {
		t.Fatalf("ApplyIntegrationCandidate(prepared restart) = %#v, %v", result, err)
	}
	if branch := integrationGitOutput(t, fixture, fixture.target.CanonicalPath, "symbolic-ref", "HEAD"); branch != targetRef {
		t.Fatalf("restarted target branch = %q, want %q", branch, targetRef)
	}
	for _, name := range []string{"candidate.txt", "target.txt"} {
		if _, err := os.Stat(filepath.Join(fixture.target.CanonicalPath, name)); err != nil {
			t.Fatalf("restarted target omits %q: %v", name, err)
		}
	}
}

func TestRegistry_RefusesCleanPartialRebaseCompletion(t *testing.T) {
	for _, test := range []struct {
		name     string
		recovery bool
		quit     bool
	}{
		{name: "active original operation"},
		{name: "active recovery operation", recovery: true},
		{name: "forged proof original operation", quit: true},
		{name: "forged proof recovery operation", recovery: true, quit: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := newIntegrationFixture(t)
			commitIntegrationFile(t, fixture, fixture.candidate.CanonicalPath, "candidate-one.txt", "one\n")
			candidateHead := commitIntegrationFile(t, fixture, fixture.candidate.CanonicalPath, "candidate-two.txt", "two\n")
			targetHead := commitIntegrationFile(t, fixture, fixture.target.CanonicalPath, "target.txt", "target\n")
			request := fixture.request("integration-clean-partial-"+strings.ReplaceAll(test.name, " ", "-"),
				application.IntegrationRebase, candidateHead, targetHead)
			writeServerRebaseProofForTest(t, fixture, request, "")
			targetRef := integrationGitOutput(t, fixture, fixture.target.CanonicalPath, "symbolic-ref", "HEAD")
			runGit(t, fixture.repository.gitExecutable, "--no-optional-locks", "-C", fixture.target.CanonicalPath,
				"symbolic-ref", integrationReceiptRefForTest("target", request), targetRef)
			runGit(t, fixture.repository.gitExecutable, "--no-optional-locks", "-C", fixture.target.CanonicalPath,
				"update-ref", integrationRebaseProofRefForTest(request), candidateHead)
			if test.recovery {
				runGit(t, fixture.repository.gitExecutable, "--no-optional-locks", "-C", fixture.repository.primary,
					"update-ref", integrationReceiptRefForTest("conflicted", request), targetHead)
			}
			runIntegrationGitExpectFailure(t, fixture.repository.gitExecutable,
				"--no-optional-locks", "-C", fixture.target.CanonicalPath,
				"-c", "core.hooksPath=/dev/null", "-c", "commit.gpgSign=false",
				"-c", "user.name=DevCrew Integration", "-c", "user.email=integration@example.invalid",
				"rebase", "--no-autostash", "--no-stat", "--exec=false",
				"--onto", targetHead, request.Candidate.BaseRevision,
				strings.TrimPrefix(integrationRebaseProofRefForTest(request), "refs/heads/"))
			if _, err := os.Stat(filepath.Join(fixture.target.CanonicalPath, "candidate-two.txt")); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("pending candidate content exists: %v", err)
			}
			if test.quit {
				runGit(t, fixture.repository.gitExecutable, "--no-optional-locks", "-C", fixture.target.CanonicalPath,
					"rebase", "--quit")
				partialHead := integrationGitOutput(t, fixture, fixture.target.CanonicalPath, "rev-parse", "HEAD")
				runGit(t, fixture.repository.gitExecutable, "--no-optional-locks", "-C", fixture.target.CanonicalPath,
					"update-ref", integrationRebaseProofRefForTest(request), partialHead)
			}
			attempt := request
			if test.recovery {
				attempt.OperationID = "integration-clean-partial-rebase-recovery"
				attempt.RecoveryOperationID = request.OperationID
			}
			if _, err := fixture.registry.ApplyIntegrationCandidate(context.Background(), attempt); err == nil {
				t.Fatal("ApplyIntegrationCandidate(clean partial rebase) error = nil")
			}
			if head := integrationGitOutput(t, fixture, fixture.repository.primary, "rev-parse", targetRef); head != targetHead {
				t.Fatalf("target ref = %q, want unchanged %q", head, targetHead)
			}
		})
	}
}

func TestRegistry_RejectsEqualCountFillerFromCompletedRebaseProof(t *testing.T) {
	fixture := newIntegrationFixture(t)
	firstCandidate := commitIntegrationFile(
		t, fixture, fixture.candidate.CanonicalPath, "candidate-one.txt", "one\n",
	)
	candidateHead := commitIntegrationFile(
		t, fixture, fixture.candidate.CanonicalPath, "candidate-two.txt", "two\n",
	)
	targetHead := commitIntegrationFile(t, fixture, fixture.target.CanonicalPath, "target.txt", "target\n")
	request := fixture.request("integration-rebase-equal-count-filler", application.IntegrationRebase, candidateHead, targetHead)
	targetRef := integrationGitOutput(t, fixture, fixture.target.CanonicalPath, "symbolic-ref", "HEAD")
	runGit(t, fixture.repository.gitExecutable, "--no-optional-locks", "-C", fixture.target.CanonicalPath,
		"-c", "core.hooksPath=/dev/null", "-c", "commit.gpgSign=false",
		"-c", "user.name=DevCrew Integration", "-c", "user.email=integration@example.invalid",
		"cherry-pick", firstCandidate)
	runGit(t, fixture.repository.gitExecutable, "--no-optional-locks", "-C", fixture.target.CanonicalPath,
		"-c", "core.hooksPath=/dev/null", "-c", "commit.gpgSign=false",
		"-c", "user.name=DevCrew Integration", "-c", "user.email=integration@example.invalid",
		"commit", "--allow-empty", "-m", "filler")
	fillerHead := integrationGitOutput(t, fixture, fixture.target.CanonicalPath, "rev-parse", "HEAD")
	writeServerRebaseProofForTest(t, fixture, request, fillerHead)
	runGit(t, fixture.repository.gitExecutable, "--no-optional-locks", "-C", fixture.target.CanonicalPath,
		"symbolic-ref", integrationReceiptRefForTest("target", request), targetRef)
	runGit(t, fixture.repository.gitExecutable, "--no-optional-locks", "-C", fixture.target.CanonicalPath,
		"update-ref", integrationReceiptRefForTest("rebased", request), fillerHead)
	runGit(t, fixture.repository.gitExecutable, "--no-optional-locks", "-C", fixture.target.CanonicalPath,
		"update-ref", "ORIG_HEAD", candidateHead)

	if _, err := fixture.registry.ApplyIntegrationCandidate(context.Background(), request); err == nil {
		t.Fatal("ApplyIntegrationCandidate(equal-count filler) error = nil")
	}
	if head := integrationGitOutput(t, fixture, fixture.target.CanonicalPath, "rev-parse", "HEAD"); head != fillerHead {
		t.Fatalf("refused filler head = %q, want unchanged %q", head, fillerHead)
	}
	runIntegrationGitExpectFailure(t, fixture.repository.gitExecutable, "--no-optional-locks", "-C",
		fixture.repository.primary, "show-ref", "--verify", "--quiet", integrationReceiptRefForTest("applied", request))
}

func TestRegistry_ReceiptOnlyReconcilesExactCompletedRebaseWithoutWritingGit(t *testing.T) {
	fixture := newIntegrationFixture(t)
	candidateHead := commitIntegrationFile(t, fixture, fixture.candidate.CanonicalPath, "candidate.txt", "candidate\n")
	targetHead := commitIntegrationFile(t, fixture, fixture.target.CanonicalPath, "target.txt", "target\n")
	request := fixture.request("integration-rebase-receipt-only-complete", application.IntegrationRebase, candidateHead, targetHead)
	applied, err := fixture.registry.ApplyIntegrationCandidate(context.Background(), request)
	if err != nil {
		t.Fatalf("ApplyIntegrationCandidate(initial rebase) error = %v", err)
	}
	appliedRef := integrationReceiptRefForTest("applied", request)
	runGit(t, fixture.repository.gitExecutable, "--no-optional-locks", "-C", fixture.target.CanonicalPath,
		"update-ref", "-d", appliedRef, applied.ResultingHead)
	proofPath := serverRebaseProofPathForTest(fixture, request)
	proofBefore, err := os.ReadFile(proofPath)
	if err != nil {
		t.Fatal(err)
	}
	branchBefore := integrationGitOutput(t, fixture, fixture.target.CanonicalPath, "symbolic-ref", "HEAD")
	request.ReceiptOnly = true

	reconciled, err := fixture.registry.ApplyIntegrationCandidate(context.Background(), request)
	if err != nil || !reflect.DeepEqual(reconciled, applied) {
		t.Fatalf("ApplyIntegrationCandidate(receipt-only completion) = %#v, %v", reconciled, err)
	}
	if branch := integrationGitOutput(t, fixture, fixture.target.CanonicalPath, "symbolic-ref", "HEAD"); branch != branchBefore {
		t.Fatalf("receipt-only branch = %q, want unchanged %q", branch, branchBefore)
	}
	if proofAfter, readErr := os.ReadFile(proofPath); readErr != nil || string(proofAfter) != string(proofBefore) {
		t.Fatalf("receipt-only proof changed: %v", readErr)
	}
	runIntegrationGitExpectFailure(t, fixture.repository.gitExecutable, "--no-optional-locks", "-C",
		fixture.repository.primary, "show-ref", "--verify", "--quiet", appliedRef)
}

func TestRegistry_RecoversIncompleteTemporaryServerRebaseProof(t *testing.T) {
	fixture := newIntegrationFixture(t)
	candidateHead := commitIntegrationFile(t, fixture, fixture.candidate.CanonicalPath, "candidate.txt", "candidate\n")
	targetHead := commitIntegrationFile(t, fixture, fixture.target.CanonicalPath, "target.txt", "target\n")
	request := fixture.request("integration-rebase-proof-orphan", application.IntegrationRebase, candidateHead, targetHead)
	proofPath := serverRebaseProofPathForTest(fixture, request)
	if err := os.MkdirAll(filepath.Dir(proofPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(proofPath+".pending", []byte("version 2\npartial"), 0o600); err != nil {
		t.Fatal(err)
	}

	result, err := fixture.registry.ApplyIntegrationCandidate(context.Background(), request)
	if err != nil || result.Outcome != application.IntegrationApplied {
		t.Fatalf("ApplyIntegrationCandidate(orphan proof) = %#v, %v", result, err)
	}
	if _, err := os.Lstat(proofPath + ".pending"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("orphan proof remains: %v", err)
	}
}

func TestRegistry_RebaseRecoveryRejectsUnverifiableCompletion(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(t *testing.T, fixture integrationFixture, recovery application.IntegrationAdapterRequest)
	}{
		{name: "dirty recovered head", mutate: func(t *testing.T, fixture integrationFixture, _ application.IntegrationAdapterRequest) {
			if err := os.WriteFile(filepath.Join(fixture.target.CanonicalPath, "untracked.txt"), []byte("untracked\n"), 0o600); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "rebased receipt lock", mutate: func(t *testing.T, fixture integrationFixture, recovery application.IntegrationAdapterRequest) {
			lockIntegrationReceiptForTest(t, fixture, integrationReceiptRefForTest("rebased", recovery))
		}},
		{name: "applied receipt lock", mutate: func(t *testing.T, fixture integrationFixture, recovery application.IntegrationAdapterRequest) {
			lockIntegrationReceiptForTest(t, fixture, integrationReceiptRefForTest("applied", recovery))
		}},
		{name: "invalid detached head", mutate: func(t *testing.T, fixture integrationFixture, _ application.IntegrationAdapterRequest) {
			headPath := integrationGitOutput(t, fixture, fixture.target.CanonicalPath, "rev-parse", "--git-path", "HEAD")
			if !filepath.IsAbs(headPath) {
				headPath = filepath.Join(fixture.target.CanonicalPath, headPath)
			}
			if err := os.WriteFile(headPath, []byte("invalid\n"), 0o600); err != nil {
				t.Fatal(err)
			}
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := newIntegrationFixture(t)
			candidateHead := commitIntegrationFile(t, fixture, fixture.candidate.CanonicalPath, "fixture.txt", "candidate\n")
			targetHead := commitIntegrationFile(t, fixture, fixture.target.CanonicalPath, "fixture.txt", "integration\n")
			request := fixture.request("integration-rebase-unverifiable", application.IntegrationRebase, candidateHead, targetHead)
			stagePreviouslyAuthorizedRebaseConflict(t, fixture, request)
			if err := os.WriteFile(filepath.Join(fixture.target.CanonicalPath, "fixture.txt"), []byte("resolved\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			runGit(t, fixture.repository.gitExecutable, "--no-optional-locks", "-C", fixture.target.CanonicalPath,
				"add", "--", "fixture.txt")
			recovery := request
			recovery.OperationID = "integration-rebase-unverifiable-recovery"
			recovery.RecoveryOperationID = request.OperationID
			test.mutate(t, fixture, recovery)
			if _, err := fixture.registry.ApplyIntegrationCandidate(context.Background(), recovery); err == nil {
				t.Fatal("ApplyIntegrationCandidate(unverifiable recovery) error = nil")
			}
		})
	}
}

func TestRegistry_RebaseRefusesALaterConflictBeforeMutation(t *testing.T) {
	fixture := newIntegrationFixture(t)
	commitIntegrationFile(t, fixture, fixture.candidate.CanonicalPath, "fixture.txt", "candidate-one\n")
	candidateHead := commitIntegrationFile(t, fixture, fixture.candidate.CanonicalPath, "fixture.txt", "candidate-two\n")
	targetHead := commitIntegrationFile(t, fixture, fixture.target.CanonicalPath, "fixture.txt", "integration\n")
	request := fixture.request("integration-rebase-later-conflict", application.IntegrationRebase, candidateHead, targetHead)
	_, err := fixture.registry.ApplyIntegrationCandidate(context.Background(), request)
	if !errors.Is(err, application.ErrIntegrationMutationNotStarted) {
		t.Fatalf("ApplyIntegrationCandidate(later conflict sequence) error = %v", err)
	}
	assertRebaseProofBoundPreservedTarget(t, fixture, request, targetHead)
}

func TestRegistry_RebaseRecoveryRefusesRepointedWorktreeBeforeMutation(t *testing.T) {
	fixture := newIntegrationFixture(t)
	candidateHead := commitIntegrationFile(t, fixture, fixture.candidate.CanonicalPath, "fixture.txt", "candidate\n")
	targetHead := commitIntegrationFile(t, fixture, fixture.target.CanonicalPath, "fixture.txt", "integration\n")
	request := fixture.request("integration-rebase-repointed", application.IntegrationRebase, candidateHead, targetHead)
	stagePreviouslyAuthorizedRebaseConflict(t, fixture, request)
	if err := os.WriteFile(filepath.Join(fixture.target.CanonicalPath, "fixture.txt"), []byte("resolved\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runGit(t, fixture.repository.gitExecutable, "--no-optional-locks", "-C", fixture.target.CanonicalPath,
		"add", "--", "fixture.txt")
	moved := fixture.target.CanonicalPath + "-moved"
	if err := os.Rename(fixture.target.CanonicalPath, moved); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(moved, fixture.target.CanonicalPath); err != nil {
		t.Fatal(err)
	}
	recovery := request
	recovery.OperationID = "integration-rebase-repointed-recovery"
	recovery.RecoveryOperationID = request.OperationID
	if _, err := newLifecycleRegistry(t, fixture.repository).ApplyIntegrationCandidate(context.Background(), recovery); err == nil {
		t.Fatal("ApplyIntegrationCandidate(repointed recovery) error = nil")
	}
	if info, err := os.Lstat(fixture.target.CanonicalPath); err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("repointed target = %#v, %v", info, err)
	}
	if head := integrationGitOutput(t, fixture, fixture.repository.primary, "rev-parse", "refs/heads/"+fixture.target.Branch); head != targetHead {
		t.Fatalf("target branch head after refused recovery = %q, want %q", head, targetHead)
	}
}
