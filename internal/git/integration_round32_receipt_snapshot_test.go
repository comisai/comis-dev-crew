package git_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/comisai/comis-dev-crew/internal/application"
)

func TestRegistry_AppliedReplayRejectsDirectToSymbolicReceiptRace(t *testing.T) {
	fixture := newIntegrationFixture(t)
	candidateHead := commitIntegrationFile(
		t, fixture, fixture.candidate.CanonicalPath, "candidate-added.txt", "candidate addition\n",
	)
	targetHead := commitIntegrationFile(
		t, fixture, fixture.target.CanonicalPath, "target-only.txt", "target only\n",
	)
	request := fixture.request("receipt-snapshot-race-0001", application.IntegrationMerge, candidateHead, targetHead)
	applied, err := fixture.registry.ApplyIntegrationCandidate(context.Background(), request)
	if err != nil || applied.Outcome != application.IntegrationApplied {
		t.Fatalf("ApplyIntegrationCandidate(initial) = %#v, %v", applied, err)
	}
	appliedRef := integrationReceiptRefForTest("applied", request)
	runGit(t, fixture.repository.gitExecutable, "--no-optional-locks", "-C", fixture.target.CanonicalPath,
		"update-ref", appliedRef, applied.ResultingHead)
	wrapper, arm := writeDirectToSymbolicReceiptRaceWrapper(t, fixture, appliedRef, applied.ResultingHead)
	registry := newIntegrationRegistryWithExecutable(t, fixture, wrapper)
	if err := os.WriteFile(arm, nil, 0o600); err != nil {
		t.Fatal(err)
	}

	result, err := registry.ApplyIntegrationCandidate(context.Background(), request)
	if err == nil || result.Outcome == application.IntegrationApplied {
		t.Fatalf("ApplyIntegrationCandidate(racing symbolic receipt) = %#v, %v", result, err)
	}
	target := integrationGitOutput(t, fixture, fixture.target.CanonicalPath, "symbolic-ref", "--no-recurse", appliedRef)
	if target != "refs/heads/"+fixture.target.Branch {
		t.Fatalf("racing receipt target = %q", target)
	}
}

func writeDirectToSymbolicReceiptRaceWrapper(
	t *testing.T,
	fixture integrationFixture,
	receipt string,
	head string,
) (string, string) {
	t.Helper()
	root := canonicalTempDir(t)
	wrapper := filepath.Join(root, "git-receipt-snapshot-race")
	arm := filepath.Join(root, "receipt-snapshot-race-armed")
	common := integrationGitOutput(t, fixture, fixture.target.CanonicalPath,
		"rev-parse", "--path-format=absolute", "--git-common-dir")
	branch := "refs/heads/" + fixture.target.Branch
	quote := func(value string) string { return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'" }
	script := fmt.Sprintf(`#!/bin/sh
real=%s
arm=%s
common=%s
receipt=%s
head=%s
branch=%s
is_receipt_symbolic=false
is_ls_files=false
previous=
for argument in "$@"; do
  if [ "$previous" = symbolic-ref ] && [ "$argument" = --quiet ]; then previous=$argument; continue; fi
  if [ "$argument" = "$receipt" ] && [ "$previous" = --no-recurse ]; then is_receipt_symbolic=true; fi
  if [ "$argument" = ls-files ]; then is_ls_files=true; fi
  previous=$argument
done
if [ -f "$arm" ] && [ "$is_receipt_symbolic" = true ]; then
  "$real" --git-dir="$common" update-ref --no-deref "$receipt" "$head" || exit $?
  "$real" "$@"
  status=$?
  "$real" --git-dir="$common" symbolic-ref "$receipt" "$branch" || exit $?
  exit $status
fi
if [ -f "$arm" ] && [ "$is_ls_files" = true ]; then
  "$real" --git-dir="$common" symbolic-ref "$receipt" "$branch" || exit $?
fi
exec "$real" "$@"
`, quote(fixture.repository.gitExecutable), quote(arm), quote(common), quote(receipt), quote(head), quote(branch))
	if err := os.WriteFile(wrapper, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	return wrapper, arm
}
