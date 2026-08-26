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
	devgit "github.com/comisai/comis-dev-crew/internal/git"
)

func TestRegistry_RefusesNewIsolatedRebaseConflictBeforeSharedMutation(t *testing.T) {
	fixture := newIntegrationFixture(t)
	candidateHead := commitIntegrationFile(t, fixture, fixture.candidate.CanonicalPath,
		"fixture.txt", "candidate\n")
	targetHead := commitIntegrationFile(t, fixture, fixture.target.CanonicalPath,
		"fixture.txt", "target\n")
	request := fixture.request("integration-isolated-rebase-conflict", application.IntegrationRebase,
		candidateHead, targetHead)

	_, err := fixture.registry.ApplyIntegrationCandidate(context.Background(), request)
	if !errors.Is(err, application.ErrIntegrationMutationNotStarted) {
		t.Fatalf("ApplyIntegrationCandidate(isolated rebase conflict) error = %v", err)
	}
	if head := integrationGitOutput(t, fixture, fixture.target.CanonicalPath, "rev-parse", "HEAD"); head != targetHead {
		t.Fatalf("target head = %q, want unchanged %q", head, targetHead)
	}
	if status := gitOutputAllowEmpty(t, fixture.repository.gitExecutable, "--no-optional-locks", "-C",
		fixture.target.CanonicalPath, "status", "--porcelain"); status != "" {
		t.Fatalf("target status = %q, want clean", status)
	}
	if refs := gitOutputAllowEmpty(t, fixture.repository.gitExecutable, "--no-optional-locks", "-C",
		fixture.repository.primary, "for-each-ref", "--format=%(refname)", "refs/comis/integration",
		"refs/heads/comis-integration-proof-"); refs != "" {
		t.Fatalf("integration refs = %q, want none", refs)
	}
}

func TestRegistry_PostCASMaterializationRequiresFreshAuthorization(t *testing.T) {
	baseline := time.Date(2099, time.January, 1, 0, 0, 0, 0, time.UTC)
	fixture := newIntegrationFixture(t)
	candidateHead := commitIntegrationFile(t, fixture, fixture.candidate.CanonicalPath,
		"component.txt", "component\n")
	targetHead := commitIntegrationFile(t, fixture, fixture.target.CanonicalPath,
		"target.txt", "target\n")
	request := fixture.request("integration-expire-after-cas", application.IntegrationMerge,
		candidateHead, targetHead)
	request.EvidenceExpiresAt = baseline.Add(time.Hour)
	targetRef := "refs/heads/" + fixture.target.Branch
	wrapper, marker := writePostCASMarkerWrapper(t, fixture, targetRef)
	expiredOnce := false
	clock := func() time.Time {
		if _, err := os.Stat(marker); err == nil && !expiredOnce {
			expiredOnce = true
			return request.EvidenceExpiresAt
		}
		return baseline
	}
	registry := newIntegrationRegistryWithExecutableAndClock(t, fixture, wrapper, clock)

	_, err := registry.ApplyIntegrationCandidate(context.Background(), request)
	if err == nil || errors.Is(err, application.ErrIntegrationMutationNotStarted) {
		t.Fatalf("ApplyIntegrationCandidate(expired after CAS) error = %v", err)
	}
	resultingHead := integrationGitOutput(t, fixture, fixture.repository.primary, "rev-parse", targetRef)
	if resultingHead == targetHead {
		t.Fatal("target ref did not advance before post-CAS refusal")
	}
	if _, err := os.Stat(filepath.Join(fixture.target.CanonicalPath, "component.txt")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("component materialized after authorization expiry: %v", err)
	}
	contents, err := os.ReadFile(filepath.Join(fixture.target.CanonicalPath, "target.txt"))
	if err != nil || string(contents) != "target\n" {
		t.Fatalf("target contents = %q, %v", contents, err)
	}

	replayed, err := registry.ApplyIntegrationCandidate(context.Background(), request)
	if err != nil || replayed.Outcome != application.IntegrationApplied || replayed.ResultingHead != resultingHead {
		t.Fatalf("ApplyIntegrationCandidate(reauthorized retry) = %#v, %v", replayed, err)
	}
}

func TestRegistry_ReceiptOnlyReconcilesCompletedMaterializationBeforeRebasedReceipt(t *testing.T) {
	baseline := time.Date(2099, time.January, 1, 0, 0, 0, 0, time.UTC)
	fixture := newIntegrationFixture(t)
	candidateHead := commitIntegrationFile(t, fixture, fixture.candidate.CanonicalPath,
		"component.txt", "component\n")
	targetHead := commitIntegrationFile(t, fixture, fixture.target.CanonicalPath,
		"target.txt", "target\n")
	request := fixture.request("integration-completed-before-rebased-receipt", application.IntegrationRebase,
		candidateHead, targetHead)
	request.EvidenceExpiresAt = baseline.Add(time.Hour)
	rebasedRef := integrationReceiptRefForTest("rebased", request)
	wrapper := writeRefusalWrapper(t, fixture, rebasedRef)
	registry := newIntegrationRegistryWithExecutableAndClock(t, fixture, wrapper, func() time.Time { return baseline })

	if _, err := registry.ApplyIntegrationCandidate(context.Background(), request); err == nil {
		t.Fatal("ApplyIntegrationCandidate(crash before rebased receipt) error = nil")
	}
	if _, err := integrationGitOutputError(fixture.repository.gitExecutable,
		fixture.repository.primary, "rev-parse", "--verify", rebasedRef); err == nil {
		t.Fatal("rebased receipt exists after simulated crash")
	}
	request.ReceiptOnly = true
	restarted := newLifecycleRegistryWithClock(t, fixture.repository, func() time.Time { return request.EvidenceExpiresAt })
	result, err := restarted.ApplyIntegrationCandidate(context.Background(), request)
	if err != nil || result.Outcome != application.IntegrationApplied || result.PreviousHead != targetHead ||
		result.ResultingHead == "" || result.ResultingHead == targetHead {
		t.Fatalf("ApplyIntegrationCandidate(receipt-only completed materialization) = %#v, %v", result, err)
	}
	if _, err := integrationGitOutputError(fixture.repository.gitExecutable,
		fixture.repository.primary, "rev-parse", "--verify", rebasedRef); err == nil {
		t.Fatal("read-only reconciliation created the rebased receipt")
	}

	runGit(t, fixture.repository.gitExecutable, "--no-optional-locks", "-C", fixture.repository.primary,
		"symbolic-ref", rebasedRef, "refs/heads/missing-rebased-receipt")
	if _, err := restarted.ApplyIntegrationCandidate(context.Background(), request); err == nil {
		t.Fatal("ApplyIntegrationCandidate(dangling rebased receipt) error = nil")
	}
}

func TestRegistry_IsolatedEnginesDisableAutomaticObjectPacking(t *testing.T) {
	fixture := newIntegrationFixture(t)
	var candidateHead string
	for index := 0; index < 32; index++ {
		candidateHead = commitIntegrationFile(t, fixture, fixture.candidate.CanonicalPath,
			"series.txt", fmt.Sprintf("candidate-%03d\n", index))
	}
	targetHead := commitIntegrationFile(t, fixture, fixture.target.CanonicalPath,
		"target.txt", "target\n")
	request := fixture.request("integration-isolated-maintenance-disabled", application.IntegrationRebase,
		candidateHead, targetHead)
	wrapper, marker := writeIsolatedMaintenanceWrapper(t, fixture)
	registry := newIntegrationRegistryWithExecutableAndClock(t, fixture, wrapper, time.Now)

	result, err := registry.ApplyIntegrationCandidate(context.Background(), request)
	if err != nil || result.Outcome != application.IntegrationApplied {
		t.Fatalf("ApplyIntegrationCandidate(large isolated rebase) = %#v, %v", result, err)
	}
	contents, err := os.ReadFile(marker)
	if err != nil || string(contents) != "loose\n" {
		t.Fatalf("isolated engine object posture = %q, %v", contents, err)
	}
}

func newIntegrationRegistryWithExecutableAndClock(
	t *testing.T,
	fixture integrationFixture,
	executable string,
	clock func() time.Time,
) *devgit.Registry {
	t.Helper()
	registry, err := devgit.NewRegistry(context.Background(), devgit.RegistryConfig{
		GitExecutable: executable, Clock: clock,
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

func writePostCASMarkerWrapper(t *testing.T, fixture integrationFixture, targetRef string) (string, string) {
	t.Helper()
	root := canonicalTempDir(t)
	wrapper := filepath.Join(root, "git-post-cas-wrapper")
	marker := filepath.Join(root, "cas-completed")
	quote := func(value string) string { return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'" }
	script := fmt.Sprintf(`#!/bin/sh
real=%s
target=%s
marker=%s
previous=
matched=false
for argument in "$@"; do
  if [ "$previous" = update-ref ] && [ "$argument" = "$target" ]; then
    matched=true
  fi
  previous=$argument
done
"$real" "$@"
status=$?
if [ $status -eq 0 ] && [ "$matched" = true ]; then
  printf 'completed\n' > "$marker"
fi
exit $status
`, quote(fixture.repository.gitExecutable), quote(targetRef), quote(marker))
	if err := os.WriteFile(wrapper, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	return wrapper, marker
}

func writeRefusalWrapper(t *testing.T, fixture integrationFixture, refusedRef string) string {
	t.Helper()
	root := canonicalTempDir(t)
	wrapper := filepath.Join(root, "git-refusal-wrapper")
	quote := func(value string) string { return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'" }
	script := fmt.Sprintf(`#!/bin/sh
real=%s
refused=%s
previous=
for argument in "$@"; do
  if [ "$previous" = update-ref ] && [ "$argument" = "$refused" ]; then
    exit 74
  fi
  previous=$argument
done
exec "$real" "$@"
`, quote(fixture.repository.gitExecutable), quote(refusedRef))
	if err := os.WriteFile(wrapper, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	return wrapper
}

func writeIsolatedMaintenanceWrapper(t *testing.T, fixture integrationFixture) (string, string) {
	t.Helper()
	root := canonicalTempDir(t)
	wrapper := filepath.Join(root, "git-isolated-maintenance-wrapper")
	marker := filepath.Join(root, "object-posture")
	quote := func(value string) string { return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'" }
	script := fmt.Sprintf(`#!/bin/sh
real=%s
marker=%s
engine=false
gc_disabled=false
maintenance_disabled=false
for argument in "$@"; do
  case "$argument" in
    rebase|merge|cherry-pick) engine=true ;;
    gc.auto=0) gc_disabled=true ;;
    maintenance.auto=false) maintenance_disabled=true ;;
  esac
done
"$real" "$@"
status=$?
case "$GIT_OBJECT_DIRECTORY" in
  *rebase-workspace-*) isolated=true ;;
  *) isolated=false ;;
esac
if [ $status -eq 0 ] && [ "$engine" = true ] && [ "$isolated" = true ]; then
  if [ "$gc_disabled" = true ] && [ "$maintenance_disabled" = true ]; then
    printf 'loose\n' > "$marker"
  else
    "$real" --git-dir="$GIT_DIR" --work-tree="$GIT_WORK_TREE" gc --quiet --prune=now || exit $?
    printf 'packed\n' > "$marker"
  fi
fi
exit $status
`, quote(fixture.repository.gitExecutable), quote(marker))
	if err := os.WriteFile(wrapper, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	return wrapper, marker
}
