package git_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/comisai/comis-dev-crew/internal/application"
)

func TestRegistry_ReconcilesPreparedRebaseRestorationCrashes(t *testing.T) {
	for _, boundary := range []string{"after-index", "before-attachment"} {
		t.Run(boundary, func(t *testing.T) {
			fixture := newIntegrationFixture(t)
			candidateHead := commitIntegrationFile(t, fixture, fixture.candidate.CanonicalPath,
				"component.txt", "component\n")
			targetHead := commitIntegrationFile(t, fixture, fixture.target.CanonicalPath,
				"target.txt", "target\n")
			request := fixture.request("integration-prepared-restore-"+boundary,
				application.IntegrationRebase, candidateHead, targetHead)
			targetRef := integrationGitOutput(t, fixture, fixture.target.CanonicalPath, "symbolic-ref", "HEAD")
			proofRef := integrationRebaseProofRefForTest(request)
			runGit(t, fixture.repository.gitExecutable, "--no-optional-locks", "-C",
				fixture.target.CanonicalPath, "symbolic-ref", integrationReceiptRefForTest("target", request), targetRef)
			runGit(t, fixture.repository.gitExecutable, "--no-optional-locks", "-C",
				fixture.target.CanonicalPath, "update-ref", proofRef, candidateHead)
			runGit(t, fixture.repository.gitExecutable, "--no-optional-locks", "-C",
				fixture.target.CanonicalPath, "symbolic-ref", "HEAD", proofRef)
			runGit(t, fixture.repository.gitExecutable, "--no-optional-locks", "-C",
				fixture.target.CanonicalPath, "reset", "--hard", candidateHead)
			wrapper, arm := writeRestorationCrashWrapper(t, fixture, boundary, targetRef)
			registry := newIntegrationRegistryWithExecutable(t, fixture, wrapper)
			if err := os.WriteFile(arm, []byte("armed\n"), 0o600); err != nil {
				t.Fatal(err)
			}

			if _, err := registry.ApplyIntegrationCandidate(context.Background(), request); err == nil {
				t.Fatal("ApplyIntegrationCandidate(crashed prepared restoration) error = nil")
			}
			result, err := fixture.registry.ApplyIntegrationCandidate(context.Background(), request)
			if err != nil || result.Outcome != application.IntegrationApplied {
				t.Fatalf("ApplyIntegrationCandidate(prepared restoration retry) = %#v, %v", result, err)
			}
		})
	}
}

func TestRegistry_ReconcilesRecoveryRestorationCrashAfterIndex(t *testing.T) {
	fixture := newIntegrationFixture(t)
	candidateHead := commitIntegrationFile(t, fixture, fixture.candidate.CanonicalPath,
		"fixture.txt", "candidate\n")
	targetHead := commitIntegrationFile(t, fixture, fixture.target.CanonicalPath,
		"fixture.txt", "target\n")
	original := fixture.request("integration-recovery-restore-original",
		application.IntegrationRebase, candidateHead, targetHead)
	stagePreviouslyAuthorizedRebaseConflict(t, fixture, original)
	if err := os.WriteFile(filepath.Join(fixture.target.CanonicalPath, "fixture.txt"),
		[]byte("resolved\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runGit(t, fixture.repository.gitExecutable, "--no-optional-locks", "-C",
		fixture.target.CanonicalPath, "add", "--", "fixture.txt")
	recovery := original
	recovery.OperationID = "integration-recovery-restore-resume"
	recovery.RecoveryOperationID = original.OperationID
	targetRef := "refs/heads/" + fixture.target.Branch
	wrapper, arm := writeRestorationCrashWrapper(t, fixture, "after-index", targetRef)
	registry := newIntegrationRegistryWithExecutable(t, fixture, wrapper)
	if err := os.WriteFile(arm, []byte("armed\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := registry.ApplyIntegrationCandidate(context.Background(), recovery); err == nil {
		t.Fatal("ApplyIntegrationCandidate(crashed recovery restoration) error = nil")
	}
	result, err := fixture.registry.ApplyIntegrationCandidate(context.Background(), recovery)
	if err != nil || result.Outcome != application.IntegrationApplied {
		t.Fatalf("ApplyIntegrationCandidate(recovery restoration retry) = %#v, %v", result, err)
	}
}

func TestRegistry_RejectsUnrepresentableResultBeforeTargetCAS(t *testing.T) {
	fixture := newIntegrationFixture(t)
	candidateHead := commitLargeIntegrationTree(t, fixture, fixture.candidate.CanonicalPath, "candidate", 1)
	targetHead := commitLargeIntegrationTree(t, fixture, fixture.target.CanonicalPath, "target", 101)
	request := fixture.request("integration-result-tree-bound", application.IntegrationMerge,
		candidateHead, targetHead)

	_, err := fixture.registry.ApplyIntegrationCandidate(context.Background(), request)
	if !errors.Is(err, application.ErrIntegrationMutationNotStarted) {
		t.Fatalf("ApplyIntegrationCandidate(unrepresentable result) error = %v", err)
	}
	if head := integrationGitOutput(t, fixture, fixture.target.CanonicalPath, "rev-parse", "HEAD"); head != targetHead {
		t.Fatalf("target head = %q, want unchanged %q", head, targetHead)
	}
	if status := gitOutputAllowEmpty(t, fixture.repository.gitExecutable, "--no-optional-locks", "-C",
		fixture.target.CanonicalPath, "status", "--porcelain"); status != "" {
		t.Fatalf("target status = %q, want clean", status)
	}
	for _, outcome := range []string{"applied", "target", "rebased", "conflicted"} {
		if _, err := integrationGitOutputError(fixture.repository.gitExecutable,
			fixture.target.CanonicalPath, "show-ref", "--verify", integrationReceiptRefForTest(outcome, request)); err == nil {
			t.Fatalf("%s receipt exists after pre-mutation refusal", outcome)
		}
	}
}

func commitLargeIntegrationTree(
	t *testing.T,
	fixture integrationFixture,
	worktree string,
	prefix string,
	seed byte,
) string {
	t.Helper()
	for index := 0; index < 5; index++ {
		contents := make([]byte, 7<<20)
		for offset := range contents {
			contents[offset] = byte((int(seed)+index+offset%251)%255 + 1)
		}
		name := fmt.Sprintf("%s-%d.bin", prefix, index)
		if err := os.WriteFile(filepath.Join(worktree, name), contents, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	runGit(t, fixture.repository.gitExecutable, "--no-optional-locks", "-C", worktree, "add", "--", ".")
	runGit(t, fixture.repository.gitExecutable, "--no-optional-locks", "-C", worktree,
		"-c", fixtureAuthorName, "-c", fixtureAuthorEmail, "commit", "-m", "fixture large tree")
	return integrationGitOutput(t, fixture, worktree, "rev-parse", "HEAD")
}

func writeRestorationCrashWrapper(
	t *testing.T,
	fixture integrationFixture,
	boundary string,
	targetRef string,
) (string, string) {
	t.Helper()
	root := canonicalTempDir(t)
	wrapper := filepath.Join(root, "git-restoration-crash")
	arm := filepath.Join(root, "armed")
	quote := func(value string) string { return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'" }
	script := fmt.Sprintf(`#!/bin/sh
real=%s
arm=%s
target=%s
target_ref=%s
boundary=%s
is_read_tree=false
is_attachment=false
previous=
has_target=false
shared=false
for argument in "$@"; do
  if [ "$argument" = read-tree ]; then is_read_tree=true; fi
  if [ "$previous" = symbolic-ref ] && [ "$argument" = HEAD ]; then is_attachment=true; fi
  if [ "$previous" = -C ] && [ "$argument" = "$target" ]; then shared=true; fi
  if [ "$argument" = "--work-tree=$target" ]; then shared=true; fi
  if [ "$argument" = "$target_ref" ]; then has_target=true; fi
  previous=$argument
done
if [ -f "$arm" ] && { [ "$GIT_WORK_TREE" = "$target" ] || [ "$shared" = true ]; }; then
  if [ "$boundary" = after-index ] && [ "$is_read_tree" = true ]; then
    "$real" "$@"
    status=$?
    rm -f "$arm"
    if [ $status -eq 0 ]; then exit 78; fi
    exit $status
  fi
  if [ "$boundary" = before-attachment ] && [ "$is_attachment" = true ] && [ "$has_target" = true ]; then
    rm -f "$arm"
    exit 79
  fi
fi
exec "$real" "$@"
`, quote(fixture.repository.gitExecutable), quote(arm), quote(fixture.target.CanonicalPath),
		quote(targetRef), quote(boundary))
	if err := os.WriteFile(wrapper, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	return wrapper, arm
}
