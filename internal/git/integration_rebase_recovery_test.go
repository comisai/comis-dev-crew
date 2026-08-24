package git_test

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
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
	replayed, err := restarted.ApplyIntegrationCandidate(context.Background(), recovery)
	if err != nil || !reflect.DeepEqual(replayed, result) {
		t.Fatalf("ApplyIntegrationCandidate(recovery replay) = %#v, %v", replayed, err)
	}
}

func TestRegistry_RebaseRecoveryRefusesUnresolvedOrChangedTarget(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(t *testing.T, fixture integrationFixture, candidateHead string)
	}{
		{name: "unresolved conflict"},
		{name: "changed target branch", mutate: func(t *testing.T, fixture integrationFixture, candidateHead string) {
			runGit(t, fixture.repository.gitExecutable, "--no-optional-locks", "-C", fixture.repository.primary,
				"update-ref", "refs/heads/"+fixture.target.Branch, candidateHead)
		}},
		{name: "missing rebase identity", mutate: func(t *testing.T, fixture integrationFixture, _ string) {
			runGit(t, fixture.repository.gitExecutable, "--no-optional-locks", "-C", fixture.target.CanonicalPath,
				"update-ref", "-d", "REBASE_HEAD")
		}},
		{name: "altered target receipt", mutate: func(t *testing.T, fixture integrationFixture, _ string) {
			receipt := integrationGitOutput(t, fixture, fixture.repository.primary,
				"for-each-ref", "--format=%(refname)", "refs/comis/integration/target")
			runGit(t, fixture.repository.gitExecutable, "--no-optional-locks", "-C", fixture.repository.primary,
				"symbolic-ref", receipt, "refs/heads/main")
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
			if test.mutate != nil {
				test.mutate(t, fixture, candidateHead)
			}
			recovery := request
			recovery.OperationID = "integration-rebase-refusal-resolution"
			recovery.RecoveryOperationID = request.OperationID
			if _, err := fixture.registry.ApplyIntegrationCandidate(context.Background(), recovery); err == nil {
				t.Fatal("ApplyIntegrationCandidate(unsafe recovery) error = nil")
			}
		})
	}
}
