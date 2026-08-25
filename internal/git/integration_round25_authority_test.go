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

func TestRegistry_MutatedReplayPolicyRefusalIsNeverPreMutation(t *testing.T) {
	for _, posture := range []string{"applied", "pending"} {
		t.Run(posture, func(t *testing.T) {
			fixture := newIntegrationFixture(t)
			candidateHead := commitIntegrationFile(t, fixture, fixture.candidate.CanonicalPath,
				"component.txt", "component\n")
			targetHead := commitIntegrationFile(t, fixture, fixture.target.CanonicalPath,
				"target.txt", "target\n")
			request := fixture.request("integration-policy-after-"+posture, application.IntegrationMerge,
				candidateHead, targetHead)
			if posture == "applied" {
				result, err := fixture.registry.ApplyIntegrationCandidate(context.Background(), request)
				if err != nil || result.Outcome != application.IntegrationApplied {
					t.Fatalf("ApplyIntegrationCandidate(applied setup) = %#v, %v", result, err)
				}
			} else {
				targetRef := "refs/heads/" + fixture.target.Branch
				wrapper, arm := writeIntegrationCASWrapper(t, fixture, "after", targetRef, candidateHead, targetHead)
				registry := newIntegrationRegistryWithExecutable(t, fixture, wrapper)
				if err := os.WriteFile(arm, []byte("armed\n"), 0o600); err != nil {
					t.Fatal(err)
				}
				if _, err := registry.ApplyIntegrationCandidate(context.Background(), request); err == nil {
					t.Fatal("ApplyIntegrationCandidate(pending setup) error = nil")
				}
			}
			runGit(t, fixture.repository.gitExecutable, "--no-optional-locks", "-C",
				fixture.target.CanonicalPath, "config", "--local", "core.fsmonitor", "/usr/bin/false")

			_, err := fixture.registry.ApplyIntegrationCandidate(context.Background(), request)
			if err == nil || errors.Is(err, application.ErrIntegrationMutationNotStarted) {
				t.Fatalf("ApplyIntegrationCandidate(mutated replay policy refusal) error = %v", err)
			}
		})
	}
}

func TestRegistry_AppliedReplayRejectsContradictorySiblingReceipts(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(t *testing.T, fixture integrationFixture, request application.IntegrationAdapterRequest, head string)
	}{
		{name: "dangling target", mutate: func(t *testing.T, fixture integrationFixture, request application.IntegrationAdapterRequest, _ string) {
			runGit(t, fixture.repository.gitExecutable, "--no-optional-locks", "-C", fixture.target.CanonicalPath,
				"symbolic-ref", integrationReceiptRefForTest("target", request), "refs/heads/missing-target")
		}},
		{name: "premature conflict", mutate: func(t *testing.T, fixture integrationFixture, request application.IntegrationAdapterRequest, head string) {
			runGit(t, fixture.repository.gitExecutable, "--no-optional-locks", "-C", fixture.target.CanonicalPath,
				"update-ref", integrationReceiptRefForTest("conflicted", request), head)
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := newIntegrationFixture(t)
			candidateHead := commitIntegrationFile(t, fixture, fixture.candidate.CanonicalPath,
				"component.txt", "component\n")
			targetHead := commitIntegrationFile(t, fixture, fixture.target.CanonicalPath,
				"target.txt", "target\n")
			request := fixture.request("integration-applied-sibling-"+strings.ReplaceAll(test.name, " ", "-"),
				application.IntegrationMerge, candidateHead, targetHead)
			result, err := fixture.registry.ApplyIntegrationCandidate(context.Background(), request)
			if err != nil || result.Outcome != application.IntegrationApplied {
				t.Fatalf("ApplyIntegrationCandidate(setup) = %#v, %v", result, err)
			}
			test.mutate(t, fixture, request, result.ResultingHead)

			if _, err := fixture.registry.ApplyIntegrationCandidate(context.Background(), request); err == nil {
				t.Fatal("ApplyIntegrationCandidate(contradictory applied replay) error = nil")
			}
		})
	}
}

func TestRegistry_CandidateInspectionIgnoresRacingFSMonitor(t *testing.T) {
	fixture := newIntegrationFixture(t)
	candidateHead := commitIntegrationFile(t, fixture, fixture.candidate.CanonicalPath,
		"component.txt", "component\n")
	targetHead := commitIntegrationFile(t, fixture, fixture.target.CanonicalPath,
		"target.txt", "target\n")
	request := fixture.request("integration-racing-fsmonitor", application.IntegrationMerge,
		candidateHead, targetHead)
	wrapper, marker := writeRacingFSMonitorWrapper(t, fixture)
	registry := newIntegrationRegistryWithExecutable(t, fixture, wrapper)

	_, _ = registry.ApplyIntegrationCandidate(context.Background(), request)
	if _, err := os.Lstat(marker); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("racing fsmonitor executed: %v", err)
	}
	if head := integrationGitOutput(t, fixture, fixture.target.CanonicalPath, "rev-parse", "HEAD"); head != targetHead {
		t.Fatalf("target head = %q, want unchanged %q", head, targetHead)
	}
}

func TestRegistry_RebaseRecoveryNeverConsumesReplacedSharedSequencer(t *testing.T) {
	for _, test := range []struct {
		name        string
		commitCount int
		payload     func(todo string, marker string) string
	}{
		{name: "exec", commitCount: 1, payload: func(todo, marker string) string {
			return fmt.Sprintf("exec /usr/bin/touch %s\n%s", marker, todo)
		}},
		{name: "update-ref", commitCount: 1, payload: func(todo, _ string) string {
			return "update-ref refs/heads/sequencer-race-attacker\n" + todo
		}},
		{name: "reorder", commitCount: 3, payload: func(todo, _ string) string {
			lines := strings.Split(strings.TrimSuffix(todo, "\n"), "\n")
			return lines[1] + "\n" + lines[0] + "\n"
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture, recovery, _, todoPath := stagedSequencerRecovery(t, "race-"+test.name, test.commitCount)
			contents, err := os.ReadFile(todoPath)
			if err != nil {
				t.Fatal(err)
			}
			resolveStagedSequencerConflict(t, fixture)
			wrapper, marker := writeSequencerReplacementWrapper(
				t, fixture, todoPath, test.payload(string(contents), filepath.Join(fixture.target.CanonicalPath, "sequencer-race-exec")),
			)
			registry := newIntegrationRegistryWithExecutable(t, fixture, wrapper)

			_, _ = registry.ApplyIntegrationCandidate(context.Background(), recovery)
			if _, err := os.Lstat(marker); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("shared sequencer was consumed: %v", err)
			}
			if _, err := os.Lstat(filepath.Join(fixture.target.CanonicalPath, "sequencer-race-exec")); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("sequencer exec ran: %v", err)
			}
			if _, err := integrationGitOutputError(fixture.repository.gitExecutable, fixture.repository.primary,
				"rev-parse", "--verify", "refs/heads/sequencer-race-attacker"); err == nil {
				t.Fatal("sequencer update-ref ran")
			}
		})
	}
}

func TestRegistry_PendingRecoverySettlesAfterPostMaterializationExpiry(t *testing.T) {
	for _, strategy := range []application.IntegrationStrategy{
		application.IntegrationMerge, application.IntegrationCherryPick, application.IntegrationRebase,
	} {
		t.Run(string(strategy), func(t *testing.T) {
			baseline := time.Date(2099, time.January, 1, 0, 0, 0, 0, time.UTC)
			fixture := newIntegrationFixture(t)
			candidateHead := commitIntegrationFile(t, fixture, fixture.candidate.CanonicalPath,
				"component.txt", "component\n")
			targetHead := commitIntegrationFile(t, fixture, fixture.target.CanonicalPath,
				"target.txt", "target\n")
			identity := strings.ReplaceAll(string(strategy), "_", "-")
			original := fixture.request("integration-post-materialization-expiry-"+identity, strategy,
				candidateHead, targetHead)
			original.EvidenceExpiresAt = baseline.Add(time.Hour)
			targetRef := "refs/heads/" + fixture.target.Branch
			crashWrapper, arm := writeIntegrationCASWrapper(t, fixture, "after", targetRef, candidateHead, targetHead)
			crashRegistry := newIntegrationRegistryWithExecutableAndClock(t, fixture, crashWrapper, func() time.Time { return baseline })
			if err := os.WriteFile(arm, []byte("armed\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := crashRegistry.ApplyIntegrationCandidate(context.Background(), original); err == nil {
				t.Fatal("ApplyIntegrationCandidate(crash after CAS) error = nil")
			}

			recovery := original
			recovery.OperationID += "-recovery"
			recovery.RecoveryOperationID = original.OperationID
			recovery.EvidenceExpiresAt = baseline.Add(time.Hour)
			wrapper, marker := writeExpireAfterReadTreeWrapper(t, fixture)
			clock := func() time.Time {
				if _, err := os.Lstat(marker); err == nil {
					return recovery.EvidenceExpiresAt
				}
				return baseline
			}
			registry := newIntegrationRegistryWithExecutableAndClock(t, fixture, wrapper, clock)
			result, err := registry.ApplyIntegrationCandidate(context.Background(), recovery)
			if err != nil || result.Outcome != application.IntegrationApplied {
				t.Fatalf("ApplyIntegrationCandidate(post-materialization expiry) = %#v, %v", result, err)
			}
		})
	}
}

func writeRacingFSMonitorWrapper(t *testing.T, fixture integrationFixture) (string, string) {
	t.Helper()
	root := canonicalTempDir(t)
	wrapper := filepath.Join(root, "git-racing-fsmonitor")
	hook := filepath.Join(root, "fsmonitor")
	marker := filepath.Join(root, "fsmonitor-ran")
	quote := func(value string) string { return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'" }
	if err := os.WriteFile(hook, []byte(fmt.Sprintf("#!/bin/sh\n: > %s\nexit 1\n", quote(marker))), 0o700); err != nil {
		t.Fatal(err)
	}
	script := fmt.Sprintf(`#!/bin/sh
real=%s
target=%s
hook=%s
armed=%s
is_status=false
for argument in "$@"; do
  if [ "$argument" = status ]; then is_status=true; fi
done
if [ "$is_status" = true ] && [ ! -f "$armed" ]; then
  : > "$armed"
  "$real" --no-optional-locks -C "$target" config --local core.fsmonitor "$hook" || exit $?
fi
exec "$real" "$@"
`, quote(fixture.repository.gitExecutable), quote(fixture.target.CanonicalPath), quote(hook), quote(filepath.Join(root, "armed")))
	if err := os.WriteFile(wrapper, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	return wrapper, marker
}

func writeSequencerReplacementWrapper(
	t *testing.T,
	fixture integrationFixture,
	todoPath string,
	payload string,
) (string, string) {
	t.Helper()
	root := canonicalTempDir(t)
	wrapper := filepath.Join(root, "git-sequencer-race")
	marker := filepath.Join(root, "shared-continue-invoked")
	quote := func(value string) string { return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'" }
	script := fmt.Sprintf(`#!/bin/sh
real=%s
target=%s
todo=%s
payload=%s
marker=%s
previous=
shared=false
rebase=false
continuing=false
for argument in "$@"; do
  if [ "$previous" = -C ] && [ "$argument" = "$target" ]; then shared=true; fi
  if [ "$argument" = rebase ]; then rebase=true; fi
  if [ "$argument" = --continue ]; then continuing=true; fi
  previous=$argument
done
if [ "$shared" = true ] && [ "$rebase" = true ] && [ "$continuing" = true ]; then
  printf '%%s' "$payload" > "$todo" || exit $?
  : > "$marker"
fi
exec "$real" "$@"
`, quote(fixture.repository.gitExecutable), quote(fixture.target.CanonicalPath), quote(todoPath), quote(payload), quote(marker))
	if err := os.WriteFile(wrapper, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	return wrapper, marker
}

func writeExpireAfterReadTreeWrapper(t *testing.T, fixture integrationFixture) (string, string) {
	t.Helper()
	root := canonicalTempDir(t)
	wrapper := filepath.Join(root, "git-expire-after-read-tree")
	marker := filepath.Join(root, "materialized")
	quote := func(value string) string { return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'" }
	script := fmt.Sprintf(`#!/bin/sh
real=%s
marker=%s
is_read_tree=false
for argument in "$@"; do
  if [ "$argument" = read-tree ]; then is_read_tree=true; fi
done
"$real" "$@"
status=$?
if [ $status -eq 0 ] && [ "$is_read_tree" = true ]; then : > "$marker"; fi
exit $status
`, quote(fixture.repository.gitExecutable), quote(marker))
	if err := os.WriteFile(wrapper, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	return wrapper, marker
}
