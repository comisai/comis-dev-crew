package git_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	devgit "github.com/comisai/comis-dev-crew/internal/git"
)

// Synchronization is the one sanctioned way the primary checkout moves, and it
// is fast-forward-only. Every other posture refuses by name and leaves the
// checkout exactly where it was: the primary is the developer's own working
// copy, so an update that discarded or rewrote anything there would destroy
// work no task ever owned.
func TestSynchronizePrimary_FastForwardsOnlyAndNamesEveryRefusal(t *testing.T) {
	t.Run("fast-forwards when the upstream moved ahead", func(t *testing.T) {
		fixture, origin := newSyncFixture(t)
		registry := newLifecycleRegistry(t, fixture)
		before := gitOutput(t, fixture.gitExecutable, "--no-optional-locks", "-C", fixture.primary, "rev-parse", "HEAD")
		advanced := advanceOrigin(t, fixture, origin, "second")

		result, err := registry.SynchronizePrimary(context.Background(), devgit.PrimarySyncRequest{
			RepositoryID: fixture.repositoryID,
		})
		if err != nil {
			t.Fatalf("SynchronizePrimary() error = %v", err)
		}
		if result.Outcome != devgit.PrimarySyncUpdated {
			t.Fatalf("outcome = %q (%q), want updated", result.Outcome, result.Refusal)
		}
		if result.PreviousHead != before || result.Head != advanced {
			t.Fatalf("head moved %q -> %q, want %q -> %q", result.PreviousHead, result.Head, before, advanced)
		}
		if actual := gitOutput(t, fixture.gitExecutable, "--no-optional-locks", "-C", fixture.primary, "rev-parse", "HEAD"); actual != advanced {
			t.Fatalf("primary head = %q, want %q", actual, advanced)
		}
	})

	t.Run("reports an already current checkout without touching it", func(t *testing.T) {
		fixture, _ := newSyncFixture(t)
		registry := newLifecycleRegistry(t, fixture)
		before := gitOutput(t, fixture.gitExecutable, "--no-optional-locks", "-C", fixture.primary, "rev-parse", "HEAD")

		result, err := registry.SynchronizePrimary(context.Background(), devgit.PrimarySyncRequest{
			RepositoryID: fixture.repositoryID,
		})
		if err != nil {
			t.Fatalf("SynchronizePrimary() error = %v", err)
		}
		if result.Outcome != devgit.PrimarySyncAlreadyCurrent || result.Head != before {
			t.Fatalf("result = %#v, want an untouched already-current checkout at %q", result, before)
		}
	})

	// Each refusal leaves the checkout where it was. The assertions check the
	// real head afterwards, not only the reported outcome: a refusal that
	// already moved the tree would report the same string.
	for name, arrange := range map[string]func(*testing.T, repositoryFixture, string) devgit.PrimarySyncRefusal{
		"dirty tracked file": func(t *testing.T, fixture repositoryFixture, origin string) devgit.PrimarySyncRefusal {
			advanceOrigin(t, fixture, origin, "second")
			if err := os.WriteFile(filepath.Join(fixture.primary, "fixture.txt"), []byte("edited\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			return devgit.PrimarySyncRefusalDirty
		},
		"untracked file": func(t *testing.T, fixture repositoryFixture, origin string) devgit.PrimarySyncRefusal {
			advanceOrigin(t, fixture, origin, "second")
			if err := os.WriteFile(filepath.Join(fixture.primary, "scratch.txt"), []byte("scratch\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			return devgit.PrimarySyncRefusalDirty
		},
		"divergent history": func(t *testing.T, fixture repositoryFixture, origin string) devgit.PrimarySyncRefusal {
			advanceOrigin(t, fixture, origin, "second")
			commitInPrimary(t, fixture, "local-only")
			return devgit.PrimarySyncRefusalDivergent
		},
		"non-default branch": func(t *testing.T, fixture repositoryFixture, origin string) devgit.PrimarySyncRefusal {
			advanceOrigin(t, fixture, origin, "second")
			runGit(t, fixture.gitExecutable, "--no-optional-locks", "-C", fixture.primary, "checkout", "-b", "side")
			return devgit.PrimarySyncRefusalNonDefaultBranch
		},
		"detached head": func(t *testing.T, fixture repositoryFixture, origin string) devgit.PrimarySyncRefusal {
			advanceOrigin(t, fixture, origin, "second")
			head := gitOutput(t, fixture.gitExecutable, "--no-optional-locks", "-C", fixture.primary, "rev-parse", "HEAD")
			runGit(t, fixture.gitExecutable, "--no-optional-locks", "-C", fixture.primary, "checkout", "--detach", head)
			return devgit.PrimarySyncRefusalDetached
		},
		"no upstream": func(t *testing.T, fixture repositoryFixture, origin string) devgit.PrimarySyncRefusal {
			runGit(t, fixture.gitExecutable, "--no-optional-locks", "-C", fixture.primary, "branch", "--unset-upstream")
			return devgit.PrimarySyncRefusalUpstreamAbsent
		},
	} {
		t.Run("refuses a "+name, func(t *testing.T) {
			fixture, origin := newSyncFixture(t)
			registry := newLifecycleRegistry(t, fixture)
			wantRefusal := arrange(t, fixture, origin)
			before := gitOutput(t, fixture.gitExecutable, "--no-optional-locks", "-C", fixture.primary, "rev-parse", "HEAD")

			result, err := registry.SynchronizePrimary(context.Background(), devgit.PrimarySyncRequest{
				RepositoryID: fixture.repositoryID,
			})
			if err != nil {
				t.Fatalf("SynchronizePrimary() error = %v", err)
			}
			if result.Outcome != devgit.PrimarySyncRefused || result.Refusal != wantRefusal {
				t.Fatalf("result = %#v, want a refusal named %q", result, wantRefusal)
			}
			if after := gitOutput(t, fixture.gitExecutable, "--no-optional-locks", "-C", fixture.primary, "rev-parse", "HEAD"); after != before {
				t.Fatalf("refused sync still moved the checkout %q -> %q", before, after)
			}
		})
	}
}

// An unconfigured repository is refused as an error, not as a sync outcome: a
// caller naming a repository this deployment never configured has made a
// different mistake from one whose checkout is not ready.
func TestSynchronizePrimary_RejectsUnconfiguredRepositoryAndMissingAuthority(t *testing.T) {
	fixture, _ := newSyncFixture(t)
	registry := newLifecycleRegistry(t, fixture)

	if _, err := registry.SynchronizePrimary(context.Background(), devgit.PrimarySyncRequest{
		RepositoryID: "never-configured",
	}); err == nil {
		t.Error("SynchronizePrimary(unconfigured repository) error = nil")
	}
	if _, err := registry.SynchronizePrimary(context.Background(), devgit.PrimarySyncRequest{}); err == nil {
		t.Error("SynchronizePrimary(no repository) error = nil")
	}
	if _, err := registry.SynchronizePrimary(nil, devgit.PrimarySyncRequest{ //nolint:staticcheck // a nil context is the refusal under test
		RepositoryID: fixture.repositoryID,
	}); err == nil {
		t.Error("SynchronizePrimary(nil context) error = nil")
	}
}

// newSyncFixture returns a registry-valid primary checkout that tracks a real
// upstream, plus the origin path used to advance it.
func newSyncFixture(t *testing.T) (repositoryFixture, string) {
	t.Helper()
	fixture := newRepositoryFixture(t, "product-api")
	origin := filepath.Join(canonicalTempDir(t), "origin.git")
	runGit(t, fixture.gitExecutable, "init", "--bare", "--initial-branch=main", origin)
	runGit(t, fixture.gitExecutable, "--no-optional-locks", "-C", fixture.primary, "remote", "add", "origin", origin)
	runGit(t, fixture.gitExecutable, "--no-optional-locks", "-C", fixture.primary, "push", "-u", "origin", "main")
	return fixture, origin
}

// advanceOrigin adds one commit to the upstream through a scratch clone and
// returns the new upstream head.
func advanceOrigin(t *testing.T, fixture repositoryFixture, origin, message string) string {
	t.Helper()
	scratch := filepath.Join(canonicalTempDir(t), "scratch")
	runGit(t, fixture.gitExecutable, "clone", origin, scratch)
	if err := os.WriteFile(filepath.Join(scratch, message+".txt"), []byte(message+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runGit(t, fixture.gitExecutable, "--no-optional-locks", "-C", scratch, "add", ".")
	runGit(t, fixture.gitExecutable, "--no-optional-locks", "-C", scratch,
		"-c", "user.name=DevCrew Fixture", "-c", "user.email=fixture@example.invalid",
		"commit", "-m", message)
	runGit(t, fixture.gitExecutable, "--no-optional-locks", "-C", scratch, "push", "origin", "main")
	return gitOutput(t, fixture.gitExecutable, "--no-optional-locks", "-C", scratch, "rev-parse", "HEAD")
}

func commitInPrimary(t *testing.T, fixture repositoryFixture, message string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(fixture.primary, message+".txt"), []byte(message+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runGit(t, fixture.gitExecutable, "--no-optional-locks", "-C", fixture.primary, "add", ".")
	runGit(t, fixture.gitExecutable, "--no-optional-locks", "-C", fixture.primary,
		"-c", "user.name=DevCrew Fixture", "-c", "user.email=fixture@example.invalid",
		"commit", "-m", message)
}
