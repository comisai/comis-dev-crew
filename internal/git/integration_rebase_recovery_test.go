package git_test

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
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
	conflicted, err := fixture.registry.ApplyIntegrationCandidate(context.Background(), request)
	if err != nil || conflicted.Outcome != application.IntegrationConflicted {
		t.Fatalf("ApplyIntegrationCandidate(conflict) = %#v, %v", conflicted, err)
	}
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
			if result, err := fixture.registry.ApplyIntegrationCandidate(context.Background(), request); err != nil || result.Outcome != application.IntegrationConflicted {
				t.Fatalf("ApplyIntegrationCandidate(conflict) = %#v, %v", result, err)
			}
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
			if result, err := fixture.registry.ApplyIntegrationCandidate(context.Background(), request); err != nil || result.Outcome != application.IntegrationConflicted {
				t.Fatalf("ApplyIntegrationCandidate(conflict) = %#v, %v", result, err)
			}
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

func TestRegistry_RebaseContinuationRefusesALaterConflict(t *testing.T) {
	fixture := newIntegrationFixture(t)
	commitIntegrationFile(t, fixture, fixture.candidate.CanonicalPath, "fixture.txt", "candidate-one\n")
	candidateHead := commitIntegrationFile(t, fixture, fixture.candidate.CanonicalPath, "fixture.txt", "candidate-two\n")
	targetHead := commitIntegrationFile(t, fixture, fixture.target.CanonicalPath, "fixture.txt", "integration\n")
	request := fixture.request("integration-rebase-later-conflict", application.IntegrationRebase, candidateHead, targetHead)
	if result, err := fixture.registry.ApplyIntegrationCandidate(context.Background(), request); err != nil || result.Outcome != application.IntegrationConflicted {
		t.Fatalf("ApplyIntegrationCandidate(conflict) = %#v, %v", result, err)
	}
	if err := os.WriteFile(filepath.Join(fixture.target.CanonicalPath, "fixture.txt"), []byte("resolved-first\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runGit(t, fixture.repository.gitExecutable, "--no-optional-locks", "-C", fixture.target.CanonicalPath,
		"add", "--", "fixture.txt")
	recovery := request
	recovery.OperationID = "integration-rebase-later-conflict-recovery"
	recovery.RecoveryOperationID = request.OperationID
	if _, err := fixture.registry.ApplyIntegrationCandidate(context.Background(), recovery); err == nil {
		t.Fatal("ApplyIntegrationCandidate(later conflict) error = nil")
	}
}

func integrationReceiptRefForTest(outcome string, request application.IntegrationAdapterRequest) string {
	canonical, _ := json.Marshal(request)
	digest := sha256.Sum256(canonical)
	return fmt.Sprintf("refs/comis/integration/%s/%x", outcome, digest)
}

func lockIntegrationReceiptForTest(t *testing.T, fixture integrationFixture, receipt string) {
	t.Helper()
	lockPath := integrationGitOutput(t, fixture, fixture.target.CanonicalPath,
		"rev-parse", "--git-path", receipt) + ".lock"
	if !filepath.IsAbs(lockPath) {
		lockPath = filepath.Join(fixture.target.CanonicalPath, lockPath)
	}
	if err := os.MkdirAll(filepath.Dir(lockPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(lockPath, []byte("locked"), 0o600); err != nil {
		t.Fatal(err)
	}
}
