package git_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/comisai/comis-dev-crew/internal/application"
)

func TestRegistry_FreshOperationAdoptsExactPendingMaterialization(t *testing.T) {
	fixture := newIntegrationFixture(t)
	candidateHead := commitIntegrationFile(t, fixture, fixture.candidate.CanonicalPath,
		"component.txt", "component\n")
	targetHead := commitIntegrationFile(t, fixture, fixture.target.CanonicalPath,
		"target.txt", "target\n")
	original := fixture.request("integration-pending-original", application.IntegrationMerge,
		candidateHead, targetHead)
	targetRef := "refs/heads/" + fixture.target.Branch
	wrapper, arm := writeIntegrationCASWrapper(t, fixture, "after", targetRef, candidateHead, targetHead)
	registry := newIntegrationRegistryWithExecutable(t, fixture, wrapper)
	if err := os.WriteFile(arm, []byte("armed\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := registry.ApplyIntegrationCandidate(context.Background(), original); err == nil {
		t.Fatal("ApplyIntegrationCandidate(crash after CAS) error = nil")
	}
	resultingHead := integrationGitOutput(t, fixture, fixture.repository.primary, "rev-parse", targetRef)
	if resultingHead == targetHead {
		t.Fatal("target ref did not advance before the simulated crash")
	}

	resumed := original
	resumed.OperationID = "integration-pending-resume"
	resumed.RecoveryOperationID = original.OperationID
	result, err := fixture.registry.ApplyIntegrationCandidate(context.Background(), resumed)
	if err != nil || result.Outcome != application.IntegrationApplied || result.ResultingHead != resultingHead {
		t.Fatalf("ApplyIntegrationCandidate(fresh pending resume) = %#v, %v", result, err)
	}
}

func TestRegistry_FreshPendingMaterializationNeverOverwritesEdits(t *testing.T) {
	fixture := newIntegrationFixture(t)
	candidateHead := commitIntegrationFile(t, fixture, fixture.candidate.CanonicalPath,
		"component.txt", "component\n")
	targetHead := commitIntegrationFile(t, fixture, fixture.target.CanonicalPath,
		"target.txt", "target\n")
	original := fixture.request("integration-pending-edit-original", application.IntegrationMerge,
		candidateHead, targetHead)
	targetRef := "refs/heads/" + fixture.target.Branch
	wrapper, arm := writeIntegrationCASWrapper(t, fixture, "after", targetRef, candidateHead, targetHead)
	registry := newIntegrationRegistryWithExecutable(t, fixture, wrapper)
	if err := os.WriteFile(arm, []byte("armed\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := registry.ApplyIntegrationCandidate(context.Background(), original); err == nil {
		t.Fatal("ApplyIntegrationCandidate(crash after CAS) error = nil")
	}
	if err := os.WriteFile(filepath.Join(fixture.target.CanonicalPath, "target.txt"), []byte("developer edit\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	resumed := original
	resumed.OperationID = "integration-pending-edit-resume"
	resumed.RecoveryOperationID = original.OperationID
	if _, err := fixture.registry.ApplyIntegrationCandidate(context.Background(), resumed); err == nil {
		t.Fatal("ApplyIntegrationCandidate(edited pending resume) error = nil")
	}
	contents, err := os.ReadFile(filepath.Join(fixture.target.CanonicalPath, "target.txt"))
	if err != nil || string(contents) != "developer edit\n" {
		t.Fatalf("developer edit = %q, %v", contents, err)
	}
}

func TestRegistry_ReceiptOnlySettlesPristinePublishedPlan(t *testing.T) {
	fixture := newIntegrationFixture(t)
	candidateHead := commitIntegrationFile(t, fixture, fixture.candidate.CanonicalPath,
		"component.txt", "component\n")
	targetHead := commitIntegrationFile(t, fixture, fixture.target.CanonicalPath,
		"target.txt", "target\n")
	request := fixture.request("integration-pristine-plan", application.IntegrationMerge,
		candidateHead, targetHead)
	digest := strings.TrimPrefix(integrationReceiptRefForTest("materialization", request),
		"refs/comis/integration/materialization/")
	blockedTransition := filepath.Join(fixture.repository.worktreeRoot, ".comis-integration-proofs",
		"materialization-"+digest)
	if err := os.MkdirAll(blockedTransition, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.registry.ApplyIntegrationCandidate(context.Background(), request); err == nil {
		t.Fatal("ApplyIntegrationCandidate(crash before transition publication) error = nil")
	}
	if err := os.Remove(blockedTransition); err != nil {
		t.Fatal(err)
	}
	request.ReceiptOnly = true
	restarted := newLifecycleRegistryWithClock(t, fixture.repository, func() time.Time { return request.EvidenceExpiresAt })
	_, err := restarted.ApplyIntegrationCandidate(context.Background(), request)
	if !errors.Is(err, application.ErrIntegrationMutationNotStarted) {
		t.Fatalf("ApplyIntegrationCandidate(pristine plan replay) error = %v", err)
	}
	if head := integrationGitOutput(t, fixture, fixture.target.CanonicalPath, "rev-parse", "HEAD"); head != targetHead {
		t.Fatalf("target head = %q, want pristine %q", head, targetHead)
	}
}

func TestRegistry_ReceiptOnlySettlesPristinePublishedRebaseProof(t *testing.T) {
	fixture := newIntegrationFixture(t)
	candidateHead := commitIntegrationFile(t, fixture, fixture.candidate.CanonicalPath,
		"component.txt", "component\n")
	targetHead := commitIntegrationFile(t, fixture, fixture.target.CanonicalPath,
		"target.txt", "target\n")
	request := fixture.request("integration-pristine-rebase-proof", application.IntegrationRebase,
		candidateHead, targetHead)
	targetReceipt := integrationReceiptRefForTest("target", request)
	lockPath := integrationReceiptLockPath(t, fixture, targetReceipt)
	if err := os.MkdirAll(filepath.Dir(lockPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(lockPath, []byte("locked"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.registry.ApplyIntegrationCandidate(context.Background(), request); err == nil {
		t.Fatal("ApplyIntegrationCandidate(crash before target receipt) error = nil")
	}
	if err := os.Remove(lockPath); err != nil {
		t.Fatal(err)
	}
	request.ReceiptOnly = true
	restarted := newLifecycleRegistryWithClock(t, fixture.repository, func() time.Time { return request.EvidenceExpiresAt })
	_, err := restarted.ApplyIntegrationCandidate(context.Background(), request)
	if !errors.Is(err, application.ErrIntegrationMutationNotStarted) {
		t.Fatalf("ApplyIntegrationCandidate(pristine rebase proof replay) error = %v", err)
	}
	if head := integrationGitOutput(t, fixture, fixture.target.CanonicalPath, "rev-parse", "HEAD"); head != targetHead {
		t.Fatalf("target head = %q, want pristine %q", head, targetHead)
	}
}

func TestRegistry_RebaseRecoveryRejectsAlteredSequencerBeforeContinue(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(t *testing.T, fixture integrationFixture, todo string) string
	}{
		{name: "exec", mutate: func(t *testing.T, fixture integrationFixture, todo string) string {
			marker := filepath.Join(fixture.target.CanonicalPath, "sequencer-exec-ran")
			t.Cleanup(func() {
				if _, err := os.Lstat(marker); !errors.Is(err, os.ErrNotExist) {
					t.Errorf("sequencer exec side effect exists: %v", err)
				}
			})
			return fmt.Sprintf("exec /usr/bin/touch %s\n%s", marker, todo)
		}},
		{name: "update-ref", mutate: func(_ *testing.T, _ integrationFixture, todo string) string {
			return "update-ref refs/heads/sequencer-attacker\n" + todo
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture, recovery, rebaseHead, todoPath := stagedSequencerRecovery(t, test.name, 1)
			contents, err := os.ReadFile(todoPath)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(todoPath, []byte(test.mutate(t, fixture, string(contents))), 0o600); err != nil {
				t.Fatal(err)
			}
			resolveStagedSequencerConflict(t, fixture)
			if _, err := fixture.registry.ApplyIntegrationCandidate(context.Background(), recovery); err == nil {
				t.Fatal("ApplyIntegrationCandidate(altered sequencer) error = nil")
			}
			if got := integrationGitOutput(t, fixture, fixture.target.CanonicalPath,
				"rev-parse", "REBASE_HEAD"); got != rebaseHead {
				t.Fatalf("REBASE_HEAD = %q, want unchanged %q", got, rebaseHead)
			}
			if _, err := integrationGitOutputError(fixture.repository.gitExecutable,
				fixture.repository.primary, "rev-parse", "--verify", "refs/heads/sequencer-attacker"); err == nil {
				t.Fatal("altered sequencer updated an attacker ref")
			}
		})
	}
}

func TestRegistry_RebaseRecoveryRejectsReorderedRemainingCommits(t *testing.T) {
	fixture, recovery, rebaseHead, todoPath := stagedSequencerRecovery(t, "reorder", 3)
	contents, err := os.ReadFile(todoPath)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSuffix(string(contents), "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("remaining todo lines = %q, want two", lines)
	}
	if err := os.WriteFile(todoPath, []byte(lines[1]+"\n"+lines[0]+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	resolveStagedSequencerConflict(t, fixture)
	if _, err := fixture.registry.ApplyIntegrationCandidate(context.Background(), recovery); err == nil {
		t.Fatal("ApplyIntegrationCandidate(reordered sequencer) error = nil")
	}
	if got := integrationGitOutput(t, fixture, fixture.target.CanonicalPath, "rev-parse", "REBASE_HEAD"); got != rebaseHead {
		t.Fatalf("REBASE_HEAD = %q, want unchanged %q", got, rebaseHead)
	}
}

func stagedSequencerRecovery(
	t *testing.T,
	identity string,
	commitCount int,
) (integrationFixture, application.IntegrationAdapterRequest, string, string) {
	t.Helper()
	fixture := newIntegrationFixture(t)
	var candidateHead string
	for index := 0; index < commitCount; index++ {
		path := fmt.Sprintf("component-%d.txt", index)
		if index == 0 {
			path = "fixture.txt"
		}
		candidateHead = commitIntegrationFile(t, fixture, fixture.candidate.CanonicalPath,
			path, fmt.Sprintf("candidate-%d\n", index))
	}
	targetHead := commitIntegrationFile(t, fixture, fixture.target.CanonicalPath,
		"fixture.txt", "target\n")
	original := fixture.request("integration-sequencer-original-"+identity,
		application.IntegrationRebase, candidateHead, targetHead)
	stagePreviouslyAuthorizedRebaseConflict(t, fixture, original)
	recovery := original
	recovery.OperationID = "integration-sequencer-recovery-" + identity
	recovery.RecoveryOperationID = original.OperationID
	gitDirectory := integrationGitOutput(t, fixture, fixture.target.CanonicalPath,
		"rev-parse", "--absolute-git-dir")
	return fixture, recovery,
		integrationGitOutput(t, fixture, fixture.target.CanonicalPath, "rev-parse", "REBASE_HEAD"),
		filepath.Join(gitDirectory, "rebase-merge", "git-rebase-todo")
}

func resolveStagedSequencerConflict(t *testing.T, fixture integrationFixture) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(fixture.target.CanonicalPath, "fixture.txt"), []byte("resolved\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runGit(t, fixture.repository.gitExecutable, "--no-optional-locks", "-C",
		fixture.target.CanonicalPath, "add", "--", "fixture.txt")
}

func integrationReceiptLockPath(t *testing.T, fixture integrationFixture, receipt string) string {
	t.Helper()
	path := integrationGitOutput(t, fixture, fixture.target.CanonicalPath,
		"rev-parse", "--git-path", receipt) + ".lock"
	if !filepath.IsAbs(path) {
		path = filepath.Join(fixture.target.CanonicalPath, path)
	}
	return path
}
