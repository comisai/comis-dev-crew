package forge

import (
	"testing"

	"github.com/comisai/comis-dev-crew/internal/application"
	"github.com/comisai/comis-dev-crew/internal/domain"
)

func TestLandedEvidenceRequestCarriesNoMergeAuthority(t *testing.T) {
	// Gathering landed evidence is a READ. If it needed the merge credential,
	// every cleanup would hold merge authority, which is exactly what the
	// separate credential exists to prevent.
	request := application.LandedEvidenceRequest{
		RepositoryID: "repo-primary",
		Branch:       "feature-x",
		HeadRevision: workHeadFixture,
	}
	if requiredCredentialFor(request) != CredentialRead {
		t.Fatal("landed evidence gathering asked for more than read authority")
	}
}

func TestLandedEvidenceMapsForgeTruthOntoTheDomainProof(t *testing.T) {
	evidence := toLandedEvidence(application.LandedEvidenceTruth{
		WorkHead:                workHeadFixture,
		Available:               true,
		ReachableFromRemoteRefs: []string{"origin/feature-x"},
	})
	proof := domain.ProveLanded(evidence)
	if !proof.Landed || proof.Route != domain.LandedByRemoteTracking {
		t.Fatalf("proof = %+v", proof)
	}
}

func TestUnavailableForgeTruthMapsToARefusingProof(t *testing.T) {
	// A remote we could not read must not arrive at the proof looking like a
	// remote that answered "no".
	evidence := toLandedEvidence(application.LandedEvidenceTruth{
		WorkHead:  workHeadFixture,
		Available: false,
	})
	proof := domain.ProveLanded(evidence)
	if proof.Landed || proof.EvidenceGap == "" {
		t.Fatalf("proof = %+v", proof)
	}
}

func TestMergedPullRequestTruthMapsByHeadBranch(t *testing.T) {
	evidence := toLandedEvidence(application.LandedEvidenceTruth{
		WorkHead:  workHeadFixture,
		Available: true,
		MergedPullRequest: &application.MergedPullRequestTruth{
			Number: 12, Merged: true, MergeCommitContainsHead: true,
		},
	})
	proof := domain.ProveLanded(evidence)
	if !proof.Landed || proof.Route != domain.LandedByMergedPullRequest {
		t.Fatalf("proof = %+v", proof)
	}
}

func TestDefaultBranchContainmentMapsOnlyWhenRefreshed(t *testing.T) {
	refreshed := toLandedEvidence(application.LandedEvidenceTruth{
		WorkHead:                     workHeadFixture,
		Available:                    true,
		DefaultBranchHead:            defaultHeadFixture,
		DefaultBranchUpToDate:        true,
		DefaultBranchContainsContent: true,
	})
	if proof := domain.ProveLanded(refreshed); !proof.Landed {
		t.Fatalf("refreshed containment refused: %+v", proof)
	}
	stale := toLandedEvidence(application.LandedEvidenceTruth{
		WorkHead:                     workHeadFixture,
		Available:                    true,
		DefaultBranchHead:            defaultHeadFixture,
		DefaultBranchUpToDate:        false,
		DefaultBranchContainsContent: true,
	})
	if proof := domain.ProveLanded(stale); proof.Landed {
		t.Fatalf("stale containment accepted: %+v", proof)
	}
}

const (
	workHeadFixture    = "0123456789abcdef0123456789abcdef01234567"
	defaultHeadFixture = "fedcba9876543210fedcba9876543210fedcba98"
)
