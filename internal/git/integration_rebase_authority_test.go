package git_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/comisai/comis-dev-crew/internal/application"
)

func TestRegistry_RebaseRecoveryRejectsChangesOutsideConflictPaths(t *testing.T) {
	fixture := newIntegrationFixture(t)
	candidateHead := commitIntegrationFile(t, fixture, fixture.candidate.CanonicalPath,
		"fixture.txt", "candidate\n")
	_ = commitIntegrationFile(t, fixture, fixture.target.CanonicalPath, "protected.txt", "protected\n")
	targetHead := commitIntegrationFile(t, fixture, fixture.target.CanonicalPath,
		"fixture.txt", "integration\n")
	request := fixture.request("integration-rebase-protected-conflict",
		application.IntegrationRebase, candidateHead, targetHead)
	result, err := fixture.registry.ApplyIntegrationCandidate(context.Background(), request)
	if err != nil || result.Outcome != application.IntegrationConflicted {
		t.Fatalf("ApplyIntegrationCandidate(conflict) = %#v, %v", result, err)
	}
	for path, contents := range map[string]string{
		"fixture.txt": "resolved\n", "protected.txt": "unrelated\n",
	} {
		if err := os.WriteFile(filepath.Join(fixture.target.CanonicalPath, path), []byte(contents), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	runGit(t, fixture.repository.gitExecutable, "--no-optional-locks", "-C", fixture.target.CanonicalPath,
		"add", "--", "fixture.txt", "protected.txt")
	recovery := request
	recovery.OperationID = "integration-rebase-protected-recovery"
	recovery.RecoveryOperationID = request.OperationID

	if _, err := fixture.registry.ApplyIntegrationCandidate(context.Background(), recovery); err == nil {
		t.Fatal("ApplyIntegrationCandidate(unrelated conflict change) error = nil")
	}
	if head := integrationGitOutput(t, fixture, fixture.target.CanonicalPath,
		"rev-parse", "refs/heads/"+fixture.target.Branch); head != targetHead {
		t.Fatalf("target head = %q, want unchanged %q", head, targetHead)
	}
}

func TestRegistry_ReceiptOnlyRecoveryRequiresOriginalReceiptAuthority(t *testing.T) {
	for _, test := range []struct {
		name   string
		id     string
		mutate func(t *testing.T, fixture integrationFixture, original, recovery application.IntegrationAdapterRequest)
		ok     bool
	}{
		{name: "exact", id: "exact", ok: true},
		{name: "dangling original target", id: "dangling", mutate: func(t *testing.T, fixture integrationFixture, original, _ application.IntegrationAdapterRequest) {
			runGit(t, fixture.repository.gitExecutable, "--no-optional-locks", "-C", fixture.target.CanonicalPath,
				"symbolic-ref", integrationReceiptRefForTest("target", original), "refs/heads/missing-original-target")
		}},
		{name: "unexpected recovery target", id: "unexpected", mutate: func(t *testing.T, fixture integrationFixture, _ application.IntegrationAdapterRequest, recovery application.IntegrationAdapterRequest) {
			runGit(t, fixture.repository.gitExecutable, "--no-optional-locks", "-C", fixture.target.CanonicalPath,
				"symbolic-ref", integrationReceiptRefForTest("target", recovery), "refs/heads/"+fixture.target.Branch)
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := newIntegrationFixture(t)
			candidateHead := commitIntegrationFile(t, fixture, fixture.candidate.CanonicalPath,
				"fixture.txt", "candidate\n")
			targetHead := commitIntegrationFile(t, fixture, fixture.target.CanonicalPath,
				"fixture.txt", "integration\n")
			original := fixture.request("integration-receipt-original-"+test.id,
				application.IntegrationRebase, candidateHead, targetHead)
			if result, err := fixture.registry.ApplyIntegrationCandidate(context.Background(), original); err != nil || result.Outcome != application.IntegrationConflicted {
				t.Fatalf("ApplyIntegrationCandidate(conflict) = %#v, %v", result, err)
			}
			if err := os.WriteFile(filepath.Join(fixture.target.CanonicalPath, "fixture.txt"), []byte("resolved\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			runGit(t, fixture.repository.gitExecutable, "--no-optional-locks", "-C", fixture.target.CanonicalPath,
				"add", "--", "fixture.txt")
			recovery := original
			recovery.OperationID = "integration-receipt-recovery-" + test.id
			recovery.RecoveryOperationID = original.OperationID
			applied, err := fixture.registry.ApplyIntegrationCandidate(context.Background(), recovery)
			if err != nil || applied.Outcome != application.IntegrationApplied {
				t.Fatalf("ApplyIntegrationCandidate(recovery) = %#v, %v", applied, err)
			}
			runGit(t, fixture.repository.gitExecutable, "--no-optional-locks", "-C", fixture.target.CanonicalPath,
				"update-ref", "-d", integrationReceiptRefForTest("applied", recovery), applied.ResultingHead)
			if test.mutate != nil {
				test.mutate(t, fixture, original, recovery)
			}
			recovery.ReceiptOnly = true
			replayed, err := fixture.registry.ApplyIntegrationCandidate(context.Background(), recovery)
			if test.ok && (err != nil || !reflect.DeepEqual(replayed, applied)) {
				t.Fatalf("ApplyIntegrationCandidate(receipt-only) = %#v, %v", replayed, err)
			}
			if !test.ok && err == nil {
				t.Fatal("ApplyIntegrationCandidate(altered receipt-only) error = nil")
			}
		})
	}
}

func TestRegistry_RebaseRejectsDropProneRangesBeforeMutation(t *testing.T) {
	t.Run("represented upstream", func(t *testing.T) {
		fixture := newIntegrationFixture(t)
		candidateHead := commitIntegrationFile(t, fixture, fixture.candidate.CanonicalPath,
			"represented.txt", "represented\n")
		runGit(t, fixture.repository.gitExecutable, "--no-optional-locks", "-C", fixture.target.CanonicalPath,
			"cherry-pick", candidateHead)
		targetHead := integrationGitOutput(t, fixture, fixture.target.CanonicalPath, "rev-parse", "HEAD")
		request := fixture.request("integration-rebase-represented-upstream",
			application.IntegrationRebase, candidateHead, targetHead)

		_, err := fixture.registry.ApplyIntegrationCandidate(context.Background(), request)
		if !errors.Is(err, application.ErrIntegrationMutationNotStarted) {
			t.Fatalf("ApplyIntegrationCandidate(represented content) error = %v", err)
		}
		assertRebaseProofBoundPreservedTarget(t, fixture, request, targetHead)
	})

	t.Run("merge topology", func(t *testing.T) {
		fixture := newIntegrationFixture(t)
		_ = commitIntegrationFile(t, fixture, fixture.candidate.CanonicalPath, "main.txt", "main\n")
		runGit(t, fixture.repository.gitExecutable, "--no-optional-locks", "-C", fixture.candidate.CanonicalPath,
			"branch", "integration-side", fixture.base)
		runGit(t, fixture.repository.gitExecutable, "--no-optional-locks", "-C", fixture.candidate.CanonicalPath,
			"checkout", "integration-side")
		_ = commitIntegrationFile(t, fixture, fixture.candidate.CanonicalPath, "side.txt", "side\n")
		runGit(t, fixture.repository.gitExecutable, "--no-optional-locks", "-C", fixture.candidate.CanonicalPath,
			"checkout", fixture.candidate.Branch)
		runGit(t, fixture.repository.gitExecutable, "--no-optional-locks", "-C", fixture.candidate.CanonicalPath,
			"merge", "--no-ff", "--no-edit", "integration-side")
		candidateHead := integrationGitOutput(t, fixture, fixture.candidate.CanonicalPath, "rev-parse", "HEAD")
		targetHead := commitIntegrationFile(t, fixture, fixture.target.CanonicalPath, "target.txt", "target\n")
		request := fixture.request("integration-rebase-merge-topology",
			application.IntegrationRebase, candidateHead, targetHead)

		_, err := fixture.registry.ApplyIntegrationCandidate(context.Background(), request)
		if !errors.Is(err, application.ErrIntegrationMutationNotStarted) {
			t.Fatalf("ApplyIntegrationCandidate(merge topology) error = %v", err)
		}
		assertRebaseProofBoundPreservedTarget(t, fixture, request, targetHead)
	})
}
