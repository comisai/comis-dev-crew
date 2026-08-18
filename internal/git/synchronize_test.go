package git_test

import (
	"context"
	"errors"
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
			return devgit.PrimarySyncRefusalNonDefault
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
	if _, err := registry.SynchronizePrimary(missingGitContext(), devgit.PrimarySyncRequest{
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
	runGit(t, fixture.gitExecutable, "--no-optional-locks", "-C", scratch, "add", "--force", ".")
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

// A canceled caller and an unreadable checkout are infrastructure failures, not
// sync outcomes. Reporting either as a refusal would tell an operator their
// checkout was in a bad posture when the truth is that nothing was inspected.
func TestSynchronizePrimary_ReportsInfrastructureFailureRatherThanARefusal(t *testing.T) {
	fixture, _ := newSyncFixture(t)
	registry := newLifecycleRegistry(t, fixture)

	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := registry.SynchronizePrimary(canceled, devgit.PrimarySyncRequest{
		RepositoryID: fixture.repositoryID,
	}); !errors.Is(err, context.Canceled) {
		t.Errorf("SynchronizePrimary(canceled) error = %v, want context.Canceled", err)
	}

	// The registry validated this checkout when it was built. Removing its Git
	// data afterwards is the drift a long-lived service actually meets.
	if err := os.RemoveAll(filepath.Join(fixture.primary, ".git")); err != nil {
		t.Fatal(err)
	}
	result, err := registry.SynchronizePrimary(context.Background(), devgit.PrimarySyncRequest{
		RepositoryID: fixture.repositoryID,
	})
	if err == nil {
		t.Fatalf("SynchronizePrimary(unreadable checkout) = %#v, want an error", result)
	}
	if result.Outcome != "" {
		t.Errorf("an unreadable checkout reported outcome %q", result.Outcome)
	}
}

// A branch may track another local branch rather than a remote one. There is
// nothing to fetch from, and the configured upstream names no remote, so the
// posture is ambiguous rather than merely absent.
func TestSynchronizePrimary_RefusesAnUpstreamThatNamesNoRemote(t *testing.T) {
	fixture, _ := newSyncFixture(t)
	registry := newLifecycleRegistry(t, fixture)
	runGit(t, fixture.gitExecutable, "--no-optional-locks", "-C", fixture.primary, "branch", "local-base")
	runGit(t, fixture.gitExecutable, "--no-optional-locks", "-C", fixture.primary, "config", "branch.main.remote", ".")
	runGit(t, fixture.gitExecutable, "--no-optional-locks", "-C", fixture.primary, "config", "branch.main.merge", "refs/heads/local-base")

	result, err := registry.SynchronizePrimary(context.Background(), devgit.PrimarySyncRequest{
		RepositoryID: fixture.repositoryID,
	})
	if err != nil {
		t.Fatalf("SynchronizePrimary() error = %v", err)
	}
	if result.Outcome != devgit.PrimarySyncRefused || result.Refusal != devgit.PrimarySyncRefusalAmbiguous {
		t.Fatalf("result = %#v, want an ambiguous refusal", result)
	}
}

// An unreachable remote is an infrastructure failure. It is not reported as a
// checkout posture: the checkout is fine and the operator's repair is the
// network or the remote, not their working copy.
func TestSynchronizePrimary_ReportsAnUnreachableRemoteAsAFailure(t *testing.T) {
	fixture, _ := newSyncFixture(t)
	registry := newLifecycleRegistry(t, fixture)
	runGit(t, fixture.gitExecutable, "--no-optional-locks", "-C", fixture.primary,
		"remote", "set-url", "origin", filepath.Join(canonicalTempDir(t), "absent.git"))

	if _, err := registry.SynchronizePrimary(context.Background(), devgit.PrimarySyncRequest{
		RepositoryID: fixture.repositoryID,
	}); err == nil {
		t.Fatal("SynchronizePrimary(unreachable remote) error = nil")
	}
}

// A corrupt index is an infrastructure failure discovered after the branch
// checks pass. It is not a posture the operator can fix by tidying their
// checkout, so it surfaces as an error rather than as a named refusal.
func TestSynchronizePrimary_ReportsACorruptIndexAsAFailure(t *testing.T) {
	fixture, _ := newSyncFixture(t)
	registry := newLifecycleRegistry(t, fixture)
	index := filepath.Join(fixture.primary, ".git", "index")
	if err := os.Remove(index); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(index, 0o700); err != nil {
		t.Fatal(err)
	}

	result, err := registry.SynchronizePrimary(context.Background(), devgit.PrimarySyncRequest{
		RepositoryID: fixture.repositoryID,
	})
	if err == nil {
		t.Fatalf("SynchronizePrimary(corrupt index) = %#v, want an error", result)
	}
	if result.Outcome != "" {
		t.Errorf("a corrupt index reported outcome %q", result.Outcome)
	}
}
