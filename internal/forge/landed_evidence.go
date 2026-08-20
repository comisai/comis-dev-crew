package forge

import (
	"github.com/comisai/comis-dev-crew/internal/application"
	"github.com/comisai/comis-dev-crew/internal/domain"
)

// requiredCredentialFor states the authority one landed-evidence read needs.
//
// It is read, always. Reachability, a merged-pull-request lookup and default
// branch containment are all questions about what already happened; none of
// them changes the repository. Keeping this explicit is what stops the merge
// credential drifting into the cleanup path, where every task would hold it.
func requiredCredentialFor(application.LandedEvidenceRequest) CredentialKind {
	return CredentialRead
}

// toLandedEvidence maps forge truth onto the domain proof input.
//
// The mapping is deliberately lossless in one direction only: it never invents
// a route the forge did not establish, and it carries `Available` through
// untouched so an unread remote stays distinguishable from a remote that
// answered.
func toLandedEvidence(truth application.LandedEvidenceTruth) domain.LandedEvidence {
	evidence := domain.LandedEvidence{
		WorkHead:                     truth.WorkHead,
		ForgeTruthAvailable:          truth.Available,
		ReachableFromRemoteRefs:      truth.ReachableFromRemoteRefs,
		DefaultBranchHead:            truth.DefaultBranchHead,
		DefaultBranchUpToDate:        truth.DefaultBranchUpToDate,
		DefaultBranchContainsContent: truth.DefaultBranchContainsContent,
	}
	if merged := truth.MergedPullRequest; merged != nil {
		evidence.MergedPullRequestByHeadBranch = &domain.MergedPullRequest{
			Number:                  merged.Number,
			Merged:                  merged.Merged,
			MergeCommitContainsHead: merged.MergeCommitContainsHead,
		}
	}
	return evidence
}

// ProveLandedFromForge is the one call a caller needs: gather-shaped truth in,
// a routed verdict out.
func ProveLandedFromForge(truth application.LandedEvidenceTruth) domain.LandedProof {
	return domain.ProveLanded(toLandedEvidence(truth))
}
