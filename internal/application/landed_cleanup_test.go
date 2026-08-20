package application

import (
	"context"
	"errors"
	"testing"

	"github.com/comisai/comis-dev-crew/internal/domain"
)

type stubGatherer struct {
	truth LandedEvidenceTruth
	err   error
	calls int
}

func (stub *stubGatherer) GatherLandedEvidence(
	context.Context, LandedEvidenceRequest,
) (LandedEvidenceTruth, error) {
	stub.calls++
	return stub.truth, stub.err
}

const gatherHead = "0123456789abcdef0123456789abcdef01234567"

func TestLandedFallbackAcceptsWorkWithNoRecordedPullRequest(t *testing.T) {
	// The E0 rule refuses here: no recorded pull request and no report hash. The
	// work may still have landed — a squash-merge that deleted the branch leaves
	// exactly this state — so the landed proof is consulted before refusing.
	gatherer := &stubGatherer{truth: LandedEvidenceTruth{
		WorkHead: gatherHead, Available: true,
		DefaultBranchHead: gatherHead, DefaultBranchUpToDate: true, DefaultBranchContainsContent: true,
	}}
	proof, err := proveCleanupLanded(context.Background(), gatherer, TaskCleanupRecord{
		RepositoryID: "repo-primary", HeadRevision: gatherHead,
	}, "devcrew/task-a")
	if err != nil {
		t.Fatalf("proveCleanupLanded() error = %v", err)
	}
	if !proof.Landed || proof.Route != domain.LandedByDefaultBranchContainment {
		t.Fatalf("proof = %+v", proof)
	}
	if gatherer.calls != 1 {
		t.Fatalf("gatherer calls = %d", gatherer.calls)
	}
}

func TestLandedFallbackRefusesWhenNothingProvesIt(t *testing.T) {
	gatherer := &stubGatherer{truth: LandedEvidenceTruth{WorkHead: gatherHead, Available: true}}
	proof, err := proveCleanupLanded(context.Background(), gatherer, TaskCleanupRecord{
		RepositoryID: "repo-primary", HeadRevision: gatherHead,
	}, "devcrew/task-a")
	if err != nil {
		t.Fatalf("proveCleanupLanded() error = %v", err)
	}
	if proof.Landed || proof.EvidenceGap == "" {
		t.Fatalf("proof = %+v", proof)
	}
}

func TestLandedFallbackTreatsAGathererErrorAsUnproven(t *testing.T) {
	// A gatherer that failed did not say the work is missing. It said nothing,
	// and nothing is not proof.
	gatherer := &stubGatherer{err: errors.New("forge unreachable")}
	proof, err := proveCleanupLanded(context.Background(), gatherer, TaskCleanupRecord{
		RepositoryID: "repo-primary", HeadRevision: gatherHead,
	}, "devcrew/task-a")
	if err != nil {
		t.Fatalf("a gatherer failure must not become a cleanup error: %v", err)
	}
	if proof.Landed {
		t.Fatalf("unreachable forge proved landed: %+v", proof)
	}
}

func TestLandedFallbackIsSkippedWithoutAGatherer(t *testing.T) {
	// A deployment that configured no gatherer keeps exactly the E0 behaviour:
	// the fallback cannot silently accept anything it never asked about.
	proof, err := proveCleanupLanded(context.Background(), nil, TaskCleanupRecord{
		RepositoryID: "repo-primary", HeadRevision: gatherHead,
	}, "devcrew/task-a")
	if err != nil {
		t.Fatalf("proveCleanupLanded() error = %v", err)
	}
	if proof.Landed {
		t.Fatalf("absent gatherer proved landed: %+v", proof)
	}
}
