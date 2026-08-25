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

func TestRegistry_CompletedMaterializationRejectsContradictoryReceiptFamily(t *testing.T) {
	for _, test := range []struct {
		strategy application.IntegrationStrategy
		outcome  string
		mutate   func(t *testing.T, fixture integrationFixture, reference string, head string)
	}{
		{
			strategy: application.IntegrationMerge,
			outcome:  "target",
			mutate: func(t *testing.T, fixture integrationFixture, reference string, _ string) {
				runGit(t, fixture.repository.gitExecutable, "--no-optional-locks", "-C",
					fixture.target.CanonicalPath, "symbolic-ref", reference, "refs/heads/missing-target")
			},
		},
		{
			strategy: application.IntegrationCherryPick,
			outcome:  "rebased",
			mutate: func(t *testing.T, fixture integrationFixture, reference string, head string) {
				runGit(t, fixture.repository.gitExecutable, "--no-optional-locks", "-C",
					fixture.target.CanonicalPath, "update-ref", reference, head)
			},
		},
	} {
		t.Run(string(test.strategy), func(t *testing.T) {
			fixture := newIntegrationFixture(t)
			candidateHead := commitIntegrationFile(t, fixture, fixture.candidate.CanonicalPath,
				"component.txt", "component\n")
			targetHead := commitIntegrationFile(t, fixture, fixture.target.CanonicalPath,
				"target.txt", "target\n")
			identity := strings.ReplaceAll(string(test.strategy), "_", "-")
			request := fixture.request("integration-completed-family-"+identity, test.strategy,
				candidateHead, targetHead)
			result, err := fixture.registry.ApplyIntegrationCandidate(context.Background(), request)
			if err != nil || result.Outcome != application.IntegrationApplied {
				t.Fatalf("ApplyIntegrationCandidate(setup) = %#v, %v", result, err)
			}
			runGit(t, fixture.repository.gitExecutable, "--no-optional-locks", "-C",
				fixture.target.CanonicalPath, "update-ref", "-d", integrationReceiptRefForTest("applied", request),
				result.ResultingHead)
			test.mutate(t, fixture, integrationReceiptRefForTest(test.outcome, request), result.ResultingHead)

			if _, err := fixture.registry.ApplyIntegrationCandidate(context.Background(), request); err == nil {
				t.Fatal("ApplyIntegrationCandidate(contradictory completed transition) error = nil")
			}
		})
	}
}

func TestRegistry_AppliedReplayRejectsUnexpectedProofRef(t *testing.T) {
	for _, test := range []struct {
		strategy application.IntegrationStrategy
		mutate   func(t *testing.T, fixture integrationFixture, reference string, head string)
	}{
		{
			strategy: application.IntegrationMerge,
			mutate: func(t *testing.T, fixture integrationFixture, reference string, _ string) {
				runGit(t, fixture.repository.gitExecutable, "--no-optional-locks", "-C",
					fixture.target.CanonicalPath, "symbolic-ref", reference, "refs/heads/missing-proof")
			},
		},
		{
			strategy: application.IntegrationCherryPick,
			mutate: func(t *testing.T, fixture integrationFixture, reference string, head string) {
				alias := "refs/heads/unexpected-proof-alias"
				runGit(t, fixture.repository.gitExecutable, "--no-optional-locks", "-C",
					fixture.target.CanonicalPath, "update-ref", alias, head)
				runGit(t, fixture.repository.gitExecutable, "--no-optional-locks", "-C",
					fixture.target.CanonicalPath, "symbolic-ref", reference, alias)
			},
		},
		{
			strategy: application.IntegrationRebase,
			mutate: func(t *testing.T, fixture integrationFixture, reference string, head string) {
				runGit(t, fixture.repository.gitExecutable, "--no-optional-locks", "-C",
					fixture.target.CanonicalPath, "update-ref", reference, head)
			},
		},
	} {
		t.Run(string(test.strategy), func(t *testing.T) {
			fixture := newIntegrationFixture(t)
			candidateHead := commitIntegrationFile(t, fixture, fixture.candidate.CanonicalPath,
				"component.txt", "component\n")
			targetHead := commitIntegrationFile(t, fixture, fixture.target.CanonicalPath,
				"target.txt", "target\n")
			identity := strings.ReplaceAll(string(test.strategy), "_", "-")
			request := fixture.request("integration-terminal-proof-"+identity, test.strategy,
				candidateHead, targetHead)
			result, err := fixture.registry.ApplyIntegrationCandidate(context.Background(), request)
			if err != nil || result.Outcome != application.IntegrationApplied {
				t.Fatalf("ApplyIntegrationCandidate(setup) = %#v, %v", result, err)
			}
			test.mutate(t, fixture, integrationRebaseProofRefForTest(request), result.ResultingHead)

			if _, err := fixture.registry.ApplyIntegrationCandidate(context.Background(), request); err == nil {
				t.Fatal("ApplyIntegrationCandidate(unexpected terminal proof) error = nil")
			}
		})
	}
}

func TestRegistry_MaterializationIgnoresRacingDynamicFilter(t *testing.T) {
	fixture := newIntegrationFixture(t)
	filteredPath := filepath.Join(fixture.candidate.CanonicalPath, "filtered.txt")
	if err := os.WriteFile(filteredPath, []byte("exact blob bytes\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("filtered.txt", filepath.Join(fixture.candidate.CanonicalPath, "component-link")); err != nil {
		t.Fatal(err)
	}
	runGit(t, fixture.repository.gitExecutable, "--no-optional-locks", "-C", fixture.candidate.CanonicalPath,
		"add", "--", "filtered.txt", "component-link")
	runGit(t, fixture.repository.gitExecutable, "--no-optional-locks", "-C", fixture.candidate.CanonicalPath,
		"-c", fixtureAuthorName, "-c", fixtureAuthorEmail, "commit", "-m", "fixture filtered change")
	candidateHead := integrationGitOutput(t, fixture, fixture.candidate.CanonicalPath, "rev-parse", "HEAD")
	targetHead := commitIntegrationFile(t, fixture, fixture.target.CanonicalPath, "target.txt", "target\n")
	request := fixture.request("integration-racing-dynamic-filter", application.IntegrationMerge,
		candidateHead, targetHead)
	wrapper, marker := writeRacingDynamicFilterWrapper(t, fixture)
	registry := newIntegrationRegistryWithExecutable(t, fixture, wrapper)

	result, err := registry.ApplyIntegrationCandidate(context.Background(), request)
	if _, statErr := os.Lstat(marker); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("dynamic filter executed: %v", statErr)
	}
	if err != nil || result.Outcome != application.IntegrationApplied {
		t.Fatalf("ApplyIntegrationCandidate(dynamic filter race) = %#v, %v", result, err)
	}
	contents, err := os.ReadFile(filepath.Join(fixture.target.CanonicalPath, "filtered.txt"))
	if err != nil || string(contents) != "exact blob bytes\n" {
		t.Fatalf("materialized blob = %q, %v", contents, err)
	}
	target, err := os.Readlink(filepath.Join(fixture.target.CanonicalPath, "component-link"))
	if err != nil || target != "filtered.txt" {
		t.Fatalf("materialized symlink = %q, %v", target, err)
	}
}

func writeRacingDynamicFilterWrapper(t *testing.T, fixture integrationFixture) (string, string) {
	t.Helper()
	root := canonicalTempDir(t)
	wrapper := filepath.Join(root, "git-racing-dynamic-filter")
	filter := filepath.Join(root, "dynamic-filter")
	marker := filepath.Join(root, "dynamic-filter-ran")
	armed := filepath.Join(root, "armed")
	commonDirectory := integrationGitOutput(t, fixture, fixture.target.CanonicalPath,
		"rev-parse", "--path-format=absolute", "--git-common-dir")
	quote := func(value string) string { return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'" }
	filterScript := fmt.Sprintf("#!/bin/sh\n: > %s\nprintf 'rewritten by filter\\n'\n", quote(marker))
	if err := os.WriteFile(filter, []byte(filterScript), 0o700); err != nil {
		t.Fatal(err)
	}
	script := fmt.Sprintf(`#!/bin/sh
real=%s
target=%s
common=%s
filter=%s
armed=%s
read_tree=false
for argument in "$@"; do
  if [ "$argument" = read-tree ]; then read_tree=true; fi
done
if [ "$read_tree" = true ] && [ "$GIT_WORK_TREE" = "$target" ] && [ ! -f "$armed" ]; then
  : > "$armed"
  "$real" --no-optional-locks -C "$target" config --local filter.reviewrace.smudge "$filter" || exit $?
  printf 'filtered.txt filter=reviewrace\n' > "$common/info/attributes" || exit $?
fi
exec "$real" "$@"
`, quote(fixture.repository.gitExecutable), quote(fixture.target.CanonicalPath), quote(commonDirectory),
		quote(filter), quote(armed))
	if err := os.WriteFile(wrapper, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	return wrapper, marker
}
