package application

import "context"

// LandedEvidenceRequest asks the forge what it can prove about one head. It
// names a branch and a head and nothing else: the request grants no authority
// and carries none, because proving work landed is a read.
type LandedEvidenceRequest struct {
	RepositoryID string
	Branch       string
	HeadRevision string
}

// MergedPullRequestTruth is forge truth about a pull request found BY HEAD
// BRANCH, not by a locally recorded number. A record that was never written, or
// was written and lost, must not by itself make work look unlanded.
type MergedPullRequestTruth struct {
	Number                  int
	Merged                  bool
	MergeCommitContainsHead bool
}

// LandedEvidenceTruth is everything the forge could establish. Available says
// whether the forge answered at all, which a caller must not confuse with the
// forge answering "no".
type LandedEvidenceTruth struct {
	WorkHead                     string
	Available                    bool
	ReachableFromRemoteRefs      []string
	MergedPullRequest            *MergedPullRequestTruth
	DefaultBranchHead            string
	DefaultBranchUpToDate        bool
	DefaultBranchContainsContent bool
}

// LandedEvidenceGatherer reads what the forge can prove about one head.
type LandedEvidenceGatherer interface {
	GatherLandedEvidence(context.Context, LandedEvidenceRequest) (LandedEvidenceTruth, error)
}
