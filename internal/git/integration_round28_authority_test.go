package git_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/comisai/comis-dev-crew/internal/application"
)

func TestRegistry_PreparedRestorationExpiryNeverReportsMutationNotStarted(t *testing.T) {
	for _, boundary := range []string{"after-index", "before-attachment", "after-attachment"} {
		t.Run(boundary, func(t *testing.T) {
			baseline := time.Date(2099, time.January, 1, 0, 0, 0, 0, time.UTC)
			fixture := newIntegrationFixture(t)
			request, _, _ := stagePreparedRestorationCrash(t, fixture, boundary, baseline)
			expired := newIntegrationRegistryWithExecutableAndClock(
				t, fixture, fixture.repository.gitExecutable, func() time.Time { return request.EvidenceExpiresAt },
			)

			_, err := expired.ApplyIntegrationCandidate(context.Background(), request)
			if err == nil || errors.Is(err, application.ErrIntegrationMutationNotStarted) {
				t.Fatalf("ApplyIntegrationCandidate(expired restoration) error = %v", err)
			}
		})
	}
}

func TestRegistry_ContradictorySymbolicReceiptPreservesPreparedJournal(t *testing.T) {
	baseline := time.Date(2099, time.January, 1, 0, 0, 0, 0, time.UTC)
	fixture := newIntegrationFixture(t)
	request, _, restorationPath := stagePreparedRestorationCrash(t, fixture, "after-index", baseline)
	appliedRef := integrationReceiptRefForTest("applied", request)
	// Receipts are read as an atomic filesystem snapshot rather than through a
	// child process, so the contradiction is planted directly on the ref.
	runGit(t, fixture.repository.gitExecutable, "--no-optional-locks", "-C", fixture.target.CanonicalPath,
		"symbolic-ref", appliedRef, "refs/heads/missing-restoration-receipt")
	registry := newIntegrationRegistryWithExecutableAndClock(
		t, fixture, fixture.repository.gitExecutable, func() time.Time { return baseline },
	)

	if _, err := registry.ApplyIntegrationCandidate(context.Background(), request); err == nil {
		t.Fatal("ApplyIntegrationCandidate(contradictory symbolic receipt) error = nil")
	}
	if _, err := os.Lstat(restorationPath); err != nil {
		t.Fatalf("prepared restoration journal was retired after receipt refusal: %v", err)
	}
	if _, err := integrationGitOutputError(fixture.repository.gitExecutable,
		fixture.target.CanonicalPath, "symbolic-ref", "--no-recurse", appliedRef); err != nil {
		t.Fatalf("contradictory symbolic receipt is unavailable: %v", err)
	}
}

func TestRegistry_FreshRecoveryAdoptsPreparedRestoration(t *testing.T) {
	baseline := time.Date(2099, time.January, 1, 0, 0, 0, 0, time.UTC)
	fixture := newIntegrationFixture(t)
	original, _, restorationPath := stagePreparedRestorationCrash(t, fixture, "after-index", baseline)
	recovery := original
	recovery.OperationID = original.OperationID + "-recovery"
	recovery.RecoveryOperationID = original.OperationID
	recovery.OriginalEvidenceDigest = original.Candidate.EvidenceDigest
	recovery.OriginalEvidenceExpiresAt = original.EvidenceExpiresAt
	recovery.PendingMaterializationRecovery = true
	recovery.EvidenceExpiresAt = baseline.Add(2 * time.Hour)
	registry := newIntegrationRegistryWithExecutableAndClock(
		t, fixture, fixture.repository.gitExecutable, func() time.Time { return baseline.Add(time.Hour) },
	)

	result, err := registry.ApplyIntegrationCandidate(context.Background(), recovery)
	if err != nil || result.Outcome != application.IntegrationApplied {
		t.Fatalf("ApplyIntegrationCandidate(prepared restoration recovery) = %#v, %v", result, err)
	}
	if _, err := os.Lstat(restorationPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("prepared restoration journal remains after recovery: %v", err)
	}
	result, err = registry.ApplyIntegrationCandidate(context.Background(), recovery)
	if err != nil || result.Outcome != application.IntegrationApplied {
		t.Fatalf("ApplyIntegrationCandidate(prepared restoration recovery replay) = %#v, %v", result, err)
	}
}

func TestRegistry_RejectsUntrackedDirectoryBlockerBeforeTargetCAS(t *testing.T) {
	fixture := newIntegrationFixture(t)
	if err := os.MkdirAll(filepath.Join(fixture.candidate.CanonicalPath, "blocked"), 0o700); err != nil {
		t.Fatal(err)
	}
	candidateHead := commitIntegrationFile(t, fixture, fixture.candidate.CanonicalPath,
		"blocked/path.txt", "candidate\n")
	targetHead := commitIntegrationFile(t, fixture, fixture.target.CanonicalPath,
		"target.txt", "target\n")
	blocker := filepath.Join(fixture.target.CanonicalPath, "blocked", "path.txt")
	if err := os.MkdirAll(blocker, 0o700); err != nil {
		t.Fatal(err)
	}
	request := fixture.request("integration-topology-directory-blocker", application.IntegrationMerge,
		candidateHead, targetHead)

	_, err := fixture.registry.ApplyIntegrationCandidate(context.Background(), request)
	if !errors.Is(err, application.ErrIntegrationMutationNotStarted) {
		t.Fatalf("ApplyIntegrationCandidate(directory blocker) error = %v", err)
	}
	if head := integrationGitOutput(t, fixture, fixture.target.CanonicalPath, "rev-parse", "HEAD"); head != targetHead {
		t.Fatalf("target head = %q, want unchanged %q", head, targetHead)
	}
	if info, statErr := os.Lstat(blocker); statErr != nil || !info.IsDir() {
		t.Fatalf("directory blocker changed: %#v, %v", info, statErr)
	}
}

func stagePreparedRestorationCrash(
	t *testing.T,
	fixture integrationFixture,
	boundary string,
	baseline time.Time,
) (application.IntegrationAdapterRequest, string, string) {
	t.Helper()
	candidateHead := commitIntegrationFile(t, fixture, fixture.candidate.CanonicalPath,
		"component.txt", "component\n")
	targetHead := commitIntegrationFile(t, fixture, fixture.target.CanonicalPath,
		"target.txt", "target\n")
	request := fixture.request("integration-prepared-adoption-"+boundary, application.IntegrationRebase,
		candidateHead, targetHead)
	request.EvidenceExpiresAt = baseline.Add(time.Hour)
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
	registry := newIntegrationRegistryWithExecutableAndClock(t, fixture, wrapper, func() time.Time { return baseline })
	if err := os.WriteFile(arm, []byte("armed\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := registry.ApplyIntegrationCandidate(context.Background(), request); err == nil {
		t.Fatal("ApplyIntegrationCandidate(prepared restoration crash) error = nil")
	}
	digest := strings.TrimPrefix(integrationReceiptRefForTest("restoration", request),
		"refs/comis/integration/restoration/")
	path := filepath.Join(fixture.repository.worktreeRoot, ".comis-integration-proofs", "restoration-"+digest)
	if _, err := os.Lstat(path); err != nil {
		t.Fatalf("prepared restoration journal is unavailable: %v", err)
	}
	return request, targetRef, path
}
