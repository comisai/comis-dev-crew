package application

import (
	"context"
	"fmt"

	"github.com/comisai/comis-dev-crew/internal/domain"
)

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

// proveCleanupLanded asks the forge whether work landed, for the one case the
// delivery rule cannot answer: no recorded pull request and no report artifact.
//
// It only ever ADDS acceptance. Every refusal the delivery rule already makes
// stays a refusal, because this is consulted after that rule has declined and
// its verdict can only turn a refusal into an acceptance, never the reverse.
//
// A gatherer that fails is not a cleanup failure. It said nothing, and nothing
// is not proof — so the caller keeps refusing, which is what it would have done
// anyway.
func proveCleanupLanded(
	ctx context.Context,
	gatherer LandedEvidenceGatherer,
	record TaskCleanupRecord,
	branch string,
) (domain.LandedProof, error) {
	if gatherer == nil {
		// A deployment that configured no gatherer keeps exactly the prior
		// behaviour rather than acquiring a route it never opted into.
		return domain.LandedProof{
			Route:       domain.LandedRouteNone,
			EvidenceGap: "no landed-evidence source is configured",
		}, nil
	}
	truth, err := gatherer.GatherLandedEvidence(ctx, LandedEvidenceRequest{
		RepositoryID: record.RepositoryID,
		Branch:       branch,
		HeadRevision: record.HeadRevision,
	})
	if err != nil {
		return domain.LandedProof{
			Route:       domain.LandedRouteNone,
			EvidenceGap: "the landed-evidence read failed, so nothing was established either way",
		}, nil
	}
	return domain.ProveLanded(domain.LandedEvidence{
		WorkHead:                      truth.WorkHead,
		ForgeTruthAvailable:           truth.Available,
		ReachableFromRemoteRefs:       truth.ReachableFromRemoteRefs,
		MergedPullRequestByHeadBranch: mergedPullRequest(truth.MergedPullRequest),
		DefaultBranchHead:             truth.DefaultBranchHead,
		DefaultBranchUpToDate:         truth.DefaultBranchUpToDate,
		DefaultBranchContainsContent:  truth.DefaultBranchContainsContent,
	}), nil
}

func mergedPullRequest(truth *MergedPullRequestTruth) *domain.MergedPullRequest {
	if truth == nil {
		return nil
	}
	return &domain.MergedPullRequest{
		Number:                  truth.Number,
		Merged:                  truth.Merged,
		MergeCommitContainsHead: truth.MergeCommitContainsHead,
	}
}

// acceptUndeliveredIfLanded decides the one case the delivery rule cannot
// answer: neither a recorded pull request nor a report artifact hash.
//
// That used to refuse outright, and a missing record is not evidence that
// nothing landed — a squash merge that deleted the branch leaves exactly this
// state. Consulting the proof here can only turn that refusal into an
// acceptance, so every removal the delivery rule already refused stays refused.
func (coordinator *CleanupCoordinator) acceptUndeliveredIfLanded(
	ctx context.Context,
	record TaskCleanupRecord,
	snapshot WorkspaceSnapshot,
) error {
	if record.ReportArtifactHash != "" {
		return nil
	}
	proof, err := proveCleanupLanded(ctx, coordinator.config.Landed, record, snapshot.Branch)
	if err != nil {
		return err
	}
	if !proof.Landed {
		return fmt.Errorf(
			"cleanup delivery evidence is unavailable and the work is not provably landed: %s",
			proof.EvidenceGap,
		)
	}
	return nil
}
