package domain

// LandedRoute names which route proved the work landed. Recording the route
// matters as much as the verdict: an operator reading a cleanup decision needs
// to know WHICH evidence carried it, because the three routes fail differently.
type LandedRoute string

const (
	LandedRouteNone                  LandedRoute = "none"
	LandedByRemoteTracking           LandedRoute = "remote_tracking_reachable"
	LandedByMergedPullRequest        LandedRoute = "merged_pull_request"
	LandedByDefaultBranchContainment LandedRoute = "default_branch_contains_content"
)

// MergedPullRequest is forge truth about one pull request, looked up by head
// branch rather than trusted from a local record.
type MergedPullRequest struct {
	Number                  int
	Merged                  bool
	MergeCommitContainsHead bool
}

// LandedEvidence is everything the proof is allowed to consider. Nothing is
// inferred from a task's own state: a task that believes it delivered is not
// evidence that anything landed.
type LandedEvidence struct {
	WorkHead                      string
	ForgeTruthAvailable           bool
	ReachableFromRemoteRefs       []string
	RecordedPullRequest           int
	MergedPullRequestByHeadBranch *MergedPullRequest
	DefaultBranchHead             string
	DefaultBranchUpToDate         bool
	DefaultBranchContainsContent  bool
}

// LandedProof is the verdict plus the route that carried it.
type LandedProof struct {
	Landed      bool
	Route       LandedRoute
	EvidenceGap string
}

// ProveLanded decides whether work is provably landed.
//
// "Landed" is proven, never assumed, and inconclusive evidence refuses. Three
// routes can each carry the proof on their own:
//
//   - the head is reachable from any remote-tracking branch, a fork remote
//     included, so an upstream-contribution pull request qualifies;
//   - a MERGED pull request, looked up by head branch, whose merge commit
//     contains the head — a missing local record never refuses by itself; or
//   - the content is contained in an up-to-date default branch, which is the
//     squash-merge-then-delete-branch case where no branch and no matching head
//     survive.
//
// A refusal always names the gap, because "not proven" is only actionable if an
// operator can tell which evidence was missing.
func ProveLanded(evidence LandedEvidence) LandedProof {
	if validateRevision(evidence.WorkHead) != nil {
		return LandedProof{
			Route:       LandedRouteNone,
			EvidenceGap: "no exact work head to prove anything about",
		}
	}

	if len(evidence.ReachableFromRemoteRefs) > 0 {
		return LandedProof{Landed: true, Route: LandedByRemoteTracking}
	}

	// Every remaining route reads forge truth. A remote we could not read is not
	// a remote that said no, so an unavailable forge refuses here instead of
	// letting a later route answer a question the earlier one never asked.
	if !evidence.ForgeTruthAvailable {
		return LandedProof{
			Route:       LandedRouteNone,
			EvidenceGap: "forge truth was unavailable, so merge and containment could not be checked",
		}
	}

	if merged := evidence.MergedPullRequestByHeadBranch; merged != nil {
		if merged.Merged && merged.MergeCommitContainsHead {
			return LandedProof{Landed: true, Route: LandedByMergedPullRequest}
		}
	}

	if evidence.DefaultBranchUpToDate && evidence.DefaultBranchContainsContent &&
		validateRevision(evidence.DefaultBranchHead) == nil {
		return LandedProof{Landed: true, Route: LandedByDefaultBranchContainment}
	}

	return LandedProof{Route: LandedRouteNone, EvidenceGap: landedGap(evidence)}
}

// landedGap names the nearest route to satisfying, so the refusal tells an
// operator what to go and get rather than only that something was missing.
func landedGap(evidence LandedEvidence) string {
	if merged := evidence.MergedPullRequestByHeadBranch; merged != nil {
		if !merged.Merged {
			return "the pull request for this head branch is not merged"
		}
		return "the merged pull request does not contain this head"
	}
	if evidence.DefaultBranchContainsContent && !evidence.DefaultBranchUpToDate {
		return "the default branch contains the content but was not refreshed, so containment is a claim about an old snapshot"
	}
	return "no remote-tracking branch reaches this head, no merged pull request was found for its head branch, and the default branch does not contain it"
}
