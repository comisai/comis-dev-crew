package domain_test

import (
	"testing"

	"github.com/comisai/comis-dev-crew/internal/domain"
)

const (
	workHead    = "0123456789abcdef0123456789abcdef01234567"
	unrelated   = "89abcdef0123456789abcdef0123456789abcdef"
	defaultHead = "fedcba9876543210fedcba9876543210fedcba98"
)

func TestLandedByRemoteTrackingReachability(t *testing.T) {
	proof := domain.ProveLanded(domain.LandedEvidence{
		WorkHead:                workHead,
		ReachableFromRemoteRefs: []string{"origin/feature-x"},
	})
	if !proof.Landed || proof.Route != domain.LandedByRemoteTracking {
		t.Fatalf("proof = %+v", proof)
	}
}

func TestLandedFromAForkRemoteCounts(t *testing.T) {
	// An upstream-contribution pull request pushes to a fork. Refusing that as
	// "not landed" would strand every contributor workflow.
	proof := domain.ProveLanded(domain.LandedEvidence{
		WorkHead:                workHead,
		ReachableFromRemoteRefs: []string{"fork/feature-x"},
	})
	if !proof.Landed {
		t.Fatalf("fork remote refused: %+v", proof)
	}
}

func TestLandedByMergedPullRequestLookedUpByHeadBranch(t *testing.T) {
	// A missing RECORDED pull request must never by itself refuse: the merged PR
	// is looked up by head branch first.
	proof := domain.ProveLanded(domain.LandedEvidence{
		WorkHead:            workHead,
		ForgeTruthAvailable: true,
		RecordedPullRequest: 0,
		MergedPullRequestByHeadBranch: &domain.MergedPullRequest{
			Number: 12, Merged: true, HeadRevisionMatches: true,
		},
	})
	if !proof.Landed || proof.Route != domain.LandedByMergedPullRequest {
		t.Fatalf("proof = %+v", proof)
	}
}

func TestLandedByContainmentInAnUpToDateDefaultBranch(t *testing.T) {
	// Exact ancestry in the refreshed default branch is independent of whether
	// the task's remote-tracking branch still exists.
	proof := domain.ProveLanded(domain.LandedEvidence{
		WorkHead:                  workHead,
		ForgeTruthAvailable:       true,
		DefaultBranchHead:         defaultHead,
		DefaultBranchUpToDate:     true,
		DefaultBranchContainsHead: true,
	})
	if !proof.Landed || proof.Route != domain.LandedByDefaultBranchContainment {
		t.Fatalf("proof = %+v", proof)
	}
}

func TestAStaleDefaultBranchProvesNothing(t *testing.T) {
	// Containment in a default branch we have not refreshed is a claim about an
	// old snapshot, not about the repository now.
	proof := domain.ProveLanded(domain.LandedEvidence{
		WorkHead:                  workHead,
		ForgeTruthAvailable:       true,
		DefaultBranchHead:         defaultHead,
		DefaultBranchUpToDate:     false,
		DefaultBranchContainsHead: true,
	})
	if proof.Landed {
		t.Fatalf("stale default branch accepted: %+v", proof)
	}
}

func TestAnUnmergedPullRequestProvesNothing(t *testing.T) {
	proof := domain.ProveLanded(domain.LandedEvidence{
		WorkHead:            workHead,
		ForgeTruthAvailable: true,
		RecordedPullRequest: 12,
		MergedPullRequestByHeadBranch: &domain.MergedPullRequest{
			Number: 12, Merged: false,
		},
	})
	if proof.Landed {
		t.Fatalf("open pull request accepted: %+v", proof)
	}
}

func TestAMergedPullRequestWhoseHeadIsNotContainedProvesNothing(t *testing.T) {
	proof := domain.ProveLanded(domain.LandedEvidence{
		WorkHead:            workHead,
		ForgeTruthAvailable: true,
		RecordedPullRequest: 12,
		MergedPullRequestByHeadBranch: &domain.MergedPullRequest{
			Number: 12, Merged: true, MergeCommitContainsHead: false,
		},
	})
	if proof.Landed {
		t.Fatalf("merged pull request not containing the head accepted: %+v", proof)
	}
}

func TestInconclusiveEvidenceRefuses(t *testing.T) {
	// No route answered. The work may well have landed; the point is that
	// nothing here PROVES it, and cleanup must refuse rather than guess.
	proof := domain.ProveLanded(domain.LandedEvidence{WorkHead: workHead, ForgeTruthAvailable: true})
	if proof.Landed || proof.Route != domain.LandedRouteNone {
		t.Fatalf("proof = %+v", proof)
	}
	if proof.EvidenceGap == "" {
		t.Fatal("refusal named no evidence gap")
	}
}

func TestUnreadableForgeTruthRefusesRatherThanFallingBack(t *testing.T) {
	// A remote we could not read is not the same as a remote that says no. If
	// the reachability probe failed, an unrelated route must not quietly rescue
	// the answer.
	proof := domain.ProveLanded(domain.LandedEvidence{
		WorkHead:            workHead,
		ForgeTruthAvailable: false,
		RecordedPullRequest: 12,
	})
	if proof.Landed {
		t.Fatalf("unreadable forge truth accepted: %+v", proof)
	}
}

func TestAMissingWorkHeadRefuses(t *testing.T) {
	proof := domain.ProveLanded(domain.LandedEvidence{ReachableFromRemoteRefs: []string{"origin/x"}})
	if proof.Landed {
		t.Fatalf("proof without a work head accepted: %+v", proof)
	}
}
