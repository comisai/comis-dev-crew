package git_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/comisai/comis-dev-crew/internal/application"
	devgit "github.com/comisai/comis-dev-crew/internal/git"
)

func TestRegistry_AppliesEveryReviewedIntegrationStrategyAndReplays(t *testing.T) {
	strategies := []application.IntegrationStrategy{
		application.IntegrationMerge,
		application.IntegrationRebase,
		application.IntegrationCherryPick,
	}
	for _, strategy := range strategies {
		t.Run(string(strategy), func(t *testing.T) {
			fixture := newIntegrationFixture(t)
			candidateHead := commitIntegrationFile(t, fixture, fixture.candidate.CanonicalPath, "component.txt", "component\n")
			targetHead := commitIntegrationFile(t, fixture, fixture.target.CanonicalPath, "integration.txt", "integration\n")
			operationID := "integration-apply-" + strings.ReplaceAll(string(strategy), "_", "-")
			request := fixture.request(operationID, strategy, candidateHead, targetHead)

			result, err := fixture.registry.ApplyIntegrationCandidate(context.Background(), request)
			if err != nil {
				t.Fatalf("ApplyIntegrationCandidate() error = %v", err)
			}
			if result.Outcome != application.IntegrationApplied || result.PreviousHead != targetHead ||
				result.ResultingHead == "" || result.ResultingHead == targetHead || len(result.ConflictPaths) != 0 {
				t.Fatalf("result = %#v", result)
			}
			for _, name := range []string{"component.txt", "integration.txt"} {
				if _, err := os.Stat(filepath.Join(fixture.target.CanonicalPath, name)); err != nil {
					t.Fatalf("integrated file %q is unavailable: %v", name, err)
				}
			}
			replayed, err := fixture.registry.ApplyIntegrationCandidate(context.Background(), request)
			if err != nil || !reflect.DeepEqual(replayed, result) {
				t.Fatalf("ApplyIntegrationCandidate(replay) = %#v, %v", replayed, err)
			}
		})
	}
}

func TestRegistry_RecordsAndReplaysExactConflictPaths(t *testing.T) {
	for _, strategy := range []application.IntegrationStrategy{application.IntegrationMerge, application.IntegrationRebase} {
		t.Run(string(strategy), func(t *testing.T) {
			fixture := newIntegrationFixture(t)
			candidateHead := commitIntegrationFile(t, fixture, fixture.candidate.CanonicalPath, "fixture.txt", "candidate\n")
			targetHead := commitIntegrationFile(t, fixture, fixture.target.CanonicalPath, "fixture.txt", "integration\n")
			request := fixture.request("integration-conflict-"+string(strategy), strategy, candidateHead, targetHead)

			result, err := fixture.registry.ApplyIntegrationCandidate(context.Background(), request)
			if err != nil {
				t.Fatalf("ApplyIntegrationCandidate(conflict) error = %v", err)
			}
			if result.Outcome != application.IntegrationConflicted || result.PreviousHead != targetHead ||
				result.ResultingHead != "" || !reflect.DeepEqual(result.ConflictPaths, []string{"fixture.txt"}) {
				t.Fatalf("conflict result = %#v", result)
			}
			if head := integrationGitOutput(t, fixture, fixture.target.CanonicalPath, "rev-parse", "HEAD"); head != targetHead {
				t.Fatalf("conflicted target head = %q, want %q", head, targetHead)
			}
			replayed, err := fixture.registry.ApplyIntegrationCandidate(context.Background(), request)
			if err != nil || !reflect.DeepEqual(replayed, result) {
				t.Fatalf("ApplyIntegrationCandidate(conflict replay) = %#v, %v", replayed, err)
			}
		})
	}
}

func TestRegistry_RebaseConflictReplayRejectsAlteredGitState(t *testing.T) {
	for _, test := range []struct {
		name   string
		tamper func(t *testing.T, fixture integrationFixture, targetHead string)
	}{
		{
			name: "changed origin",
			tamper: func(t *testing.T, fixture integrationFixture, targetHead string) {
				runGit(t, fixture.repository.gitExecutable, "--no-optional-locks", "-C",
					fixture.target.CanonicalPath, "update-ref", "ORIG_HEAD", targetHead)
			},
		},
		{
			name: "missing rebase head",
			tamper: func(t *testing.T, fixture integrationFixture, _ string) {
				runGit(t, fixture.repository.gitExecutable, "--no-optional-locks", "-C",
					fixture.target.CanonicalPath, "update-ref", "-d", "REBASE_HEAD")
			},
		},
		{
			name: "changed rebase head",
			tamper: func(t *testing.T, fixture integrationFixture, targetHead string) {
				runGit(t, fixture.repository.gitExecutable, "--no-optional-locks", "-C",
					fixture.target.CanonicalPath, "update-ref", "REBASE_HEAD", targetHead)
			},
		},
		{
			name: "changed current head",
			tamper: func(t *testing.T, fixture integrationFixture, _ string) {
				candidateHead := integrationGitOutput(
					t, fixture, fixture.candidate.CanonicalPath, "rev-parse", "HEAD",
				)
				runGit(t, fixture.repository.gitExecutable, "--no-optional-locks", "-C",
					fixture.target.CanonicalPath, "update-ref", "HEAD", candidateHead)
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := newIntegrationFixture(t)
			candidateHead := commitIntegrationFile(
				t, fixture, fixture.candidate.CanonicalPath, "fixture.txt", "candidate\n",
			)
			targetHead := commitIntegrationFile(
				t, fixture, fixture.target.CanonicalPath, "fixture.txt", "integration\n",
			)
			request := fixture.request(
				"integration-rebase-tamper-"+strings.ReplaceAll(test.name, " ", "-"),
				application.IntegrationRebase,
				candidateHead,
				targetHead,
			)
			result, err := fixture.registry.ApplyIntegrationCandidate(context.Background(), request)
			if err != nil || result.Outcome != application.IntegrationConflicted {
				t.Fatalf("ApplyIntegrationCandidate(conflict) = %#v, %v", result, err)
			}
			test.tamper(t, fixture, targetHead)
			if _, err := fixture.registry.ApplyIntegrationCandidate(context.Background(), request); err == nil {
				t.Fatal("ApplyIntegrationCandidate(altered replay) error = nil")
			}
		})
	}
}

func TestRegistry_RebaseAppliesLaterCandidateAfterCurrentTarget(t *testing.T) {
	fixture := newIntegrationFixture(t)
	firstHead := commitIntegrationFile(t, fixture, fixture.candidate.CanonicalPath, "first.txt", "first\n")
	targetHead := commitIntegrationFile(t, fixture, fixture.target.CanonicalPath, "target.txt", "target\n")
	first, err := fixture.registry.ApplyIntegrationCandidate(context.Background(),
		fixture.request("integration-rebase-first", application.IntegrationRebase, firstHead, targetHead))
	if err != nil {
		t.Fatal(err)
	}
	secondCandidate, err := fixture.registry.PrepareWorktree(context.Background(), devgit.PrepareWorktreeRequest{
		OperationID: "prepare-component-0002", TaskHandle: "task-component-two",
		RepositoryID: fixture.repository.repositoryID, BaseRevision: fixture.base,
	})
	if err != nil {
		t.Fatal(err)
	}
	secondHead := commitIntegrationFile(t, fixture, secondCandidate.CanonicalPath, "second.txt", "second\n")
	request := fixture.request("integration-rebase-second", application.IntegrationRebase, secondHead, first.ResultingHead)
	request.Candidate.TaskHandle = secondCandidate.TaskHandle
	request.Candidate.WorktreePath = secondCandidate.CanonicalPath
	second, err := fixture.registry.ApplyIntegrationCandidate(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if first.ResultingHead == second.ResultingHead {
		t.Fatal("second rebase did not advance the integration target")
	}
	runGit(t, fixture.repository.gitExecutable, "--no-optional-locks", "-C", fixture.target.CanonicalPath,
		"merge-base", "--is-ancestor", first.ResultingHead, second.ResultingHead)
	for _, name := range []string{"first.txt", "second.txt", "target.txt"} {
		if _, err := os.Stat(filepath.Join(fixture.target.CanonicalPath, name)); err != nil {
			t.Fatalf("rebased target omits %q: %v", name, err)
		}
	}
}

func TestRegistry_RevalidatesCandidateAndTargetHeadsImmediatelyBeforeMutation(t *testing.T) {
	tests := []struct {
		name       string
		moveTarget bool
	}{
		{name: "candidate moved"},
		{name: "target moved", moveTarget: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newIntegrationFixture(t)
			candidateHead := commitIntegrationFile(t, fixture, fixture.candidate.CanonicalPath, "component.txt", "component\n")
			targetHead := integrationGitOutput(t, fixture, fixture.target.CanonicalPath, "rev-parse", "HEAD")
			request := fixture.request("integration-head-check-0001", application.IntegrationCherryPick, candidateHead, targetHead)
			changedPath := fixture.candidate.CanonicalPath
			if test.moveTarget {
				changedPath = fixture.target.CanonicalPath
			}
			commitIntegrationFile(t, fixture, changedPath, "late.txt", "late\n")

			result, err := fixture.registry.ApplyIntegrationCandidate(context.Background(), request)
			if test.moveTarget {
				if err == nil {
					t.Fatal("ApplyIntegrationCandidate(changed target head) error = nil")
				}
			} else if err != nil || result.Outcome != application.IntegrationInvalidated {
				t.Fatalf("ApplyIntegrationCandidate(changed candidate head) = %#v, %v", result, err)
			}
			if _, err := os.Stat(filepath.Join(fixture.target.CanonicalPath, "component.txt")); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("target changed despite refused head: %v", err)
			}
		})
	}
}

func TestRegistry_RefusesDirtyCandidateAndAlteredReplay(t *testing.T) {
	fixture := newIntegrationFixture(t)
	candidateHead := commitIntegrationFile(t, fixture, fixture.candidate.CanonicalPath, "component.txt", "component\n")
	targetHead := integrationGitOutput(t, fixture, fixture.target.CanonicalPath, "rev-parse", "HEAD")
	request := fixture.request("integration-boundaries-0001", application.IntegrationCherryPick, candidateHead, targetHead)
	if err := os.WriteFile(filepath.Join(fixture.candidate.CanonicalPath, "dirty.txt"), []byte("dirty\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	invalidated, err := fixture.registry.ApplyIntegrationCandidate(context.Background(), request)
	if err != nil || invalidated.Outcome != application.IntegrationInvalidated {
		t.Fatalf("ApplyIntegrationCandidate(dirty candidate) = %#v, %v", invalidated, err)
	}
	if err := os.Remove(filepath.Join(fixture.candidate.CanonicalPath, "dirty.txt")); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.registry.ApplyIntegrationCandidate(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	altered := request
	altered.Candidate.HeadRevision = strings.Repeat("f", 40)
	if _, err := fixture.registry.ApplyIntegrationCandidate(context.Background(), altered); err == nil {
		t.Fatal("ApplyIntegrationCandidate(altered replay) error = nil")
	}
}

type integrationFixture struct {
	repository repositoryFixture
	registry   *devgit.Registry
	base       string
	candidate  devgit.PreparedWorktree
	target     devgit.PreparedWorktree
}

func newIntegrationFixture(t *testing.T) integrationFixture {
	t.Helper()
	repository := newRepositoryFixture(t, "product-api")
	registry := newLifecycleRegistry(t, repository)
	base := integrationGitOutput(t, integrationFixture{repository: repository}, repository.primary, "rev-parse", "HEAD")
	prepare := func(operationID, taskHandle string) devgit.PreparedWorktree {
		prepared, err := registry.PrepareWorktree(context.Background(), devgit.PrepareWorktreeRequest{
			OperationID: operationID, TaskHandle: taskHandle,
			RepositoryID: repository.repositoryID, BaseRevision: base,
		})
		if err != nil {
			t.Fatal(err)
		}
		return prepared
	}
	return integrationFixture{
		repository: repository, registry: registry, base: base,
		candidate: prepare("prepare-component-0001", "task-component"),
		target:    prepare("prepare-integration-0001", "task-integration"),
	}
}

func (fixture integrationFixture) request(
	operationID string,
	strategy application.IntegrationStrategy,
	candidateHead string,
	targetHead string,
) application.IntegrationAdapterRequest {
	return application.IntegrationAdapterRequest{
		OperationID: operationID, Strategy: strategy,
		Target: application.IntegrationTargetReference{
			TaskHandle: fixture.target.TaskHandle, RepositoryID: fixture.repository.repositoryID,
			WorktreePath: fixture.target.CanonicalPath, ExpectedHead: targetHead,
		},
		Candidate: application.IntegrationCandidateReference{
			TaskHandle: fixture.candidate.TaskHandle, RepositoryID: fixture.repository.repositoryID,
			WorktreePath: fixture.candidate.CanonicalPath, BaseRevision: fixture.base,
			HeadRevision: candidateHead, EvidenceDigest: strings.Repeat("e", 64),
		},
	}
}

func commitIntegrationFile(t *testing.T, fixture integrationFixture, worktree, name, body string) string {
	t.Helper()
	if err := os.WriteFile(filepath.Join(worktree, name), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	runGit(t, fixture.repository.gitExecutable, "--no-optional-locks", "-C", worktree, "add", "--", name)
	runGit(t, fixture.repository.gitExecutable, "--no-optional-locks", "-C", worktree,
		"-c", fixtureAuthorName, "-c", fixtureAuthorEmail, "commit", "-m", "fixture change")
	return integrationGitOutput(t, fixture, worktree, "rev-parse", "HEAD")
}

func integrationGitOutput(t *testing.T, fixture integrationFixture, worktree string, arguments ...string) string {
	t.Helper()
	return gitOutput(t, fixture.repository.gitExecutable,
		append([]string{"--no-optional-locks", "-C", worktree}, arguments...)...)
}
