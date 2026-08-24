package forge

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"

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
		WorkHead:                  truth.WorkHead,
		ForgeTruthAvailable:       truth.Available,
		ReachableFromRemoteRefs:   truth.ReachableFromRemoteRefs,
		DefaultBranchHead:         truth.DefaultBranchHead,
		DefaultBranchUpToDate:     truth.DefaultBranchUpToDate,
		DefaultBranchContainsHead: truth.DefaultBranchContainsHead,
	}
	if merged := truth.MergedPullRequest; merged != nil {
		evidence.MergedPullRequestByHeadBranch = &domain.MergedPullRequest{
			Number:                  merged.Number,
			Merged:                  merged.Merged,
			MergeCommitContainsHead: merged.MergeCommitContainsHead,
			HeadRevisionMatches:     merged.HeadRevisionMatches,
		}
	}
	return evidence
}

// ProveLandedFromForge is the one call a caller needs: gather-shaped truth in,
// a routed verdict out.
func ProveLandedFromForge(truth application.LandedEvidenceTruth) domain.LandedProof {
	return domain.ProveLanded(toLandedEvidence(truth))
}

// githubComparison is the subset of a commit comparison the proof needs.
type githubComparison struct {
	Status string `json:"status"`
}

type githubReference struct {
	Ref    string                `json:"ref"`
	Object githubReferenceObject `json:"object"`
}

type githubReferenceObject struct {
	Type string `json:"type"`
	SHA  string `json:"sha"`
}

// containedStatuses are the comparison results that mean "already contains".
// `behind` means the base is behind the head's ancestor set — the content is in
// — and `identical` is the same thing with nothing left over. `ahead` and
// `diverged` both mean it is not.
func comparisonContains(status string) bool {
	return status == "behind" || status == "identical"
}

// GatherLandedEvidence reads what the forge can prove about one head.
//
// Every read is best-effort in one specific sense: a forge that will not answer
// yields Available=false rather than an error, because a transient outage must
// never reach the proof looking like an answer of "no". A caller that treated
// an unreachable forge as proof of non-delivery would remove work that had in
// fact landed.
func (adapter *GitHubAdapter) GatherLandedEvidence(
	ctx context.Context,
	request application.LandedEvidenceRequest,
) (application.LandedEvidenceTruth, error) {
	if adapter == nil || ctx == nil || request.RepositoryID != adapter.config.RepositoryIdentity ||
		!branchPattern.MatchString(request.Branch) || strings.Contains(request.Branch, "..") ||
		!revisionPattern.MatchString(request.HeadRevision) {
		return application.LandedEvidenceTruth{}, errors.New("gather landed evidence: request identity differs")
	}
	if err := ctx.Err(); err != nil {
		return application.LandedEvidenceTruth{}, err
	}
	credential, err := adapter.config.ReadCredentials.Resolve(ctx)
	if err != nil || !validReadCredential(credential) {
		return application.LandedEvidenceTruth{}, errors.New("gather landed evidence: read authority is unavailable")
	}

	truth := application.LandedEvidenceTruth{WorkHead: request.HeadRevision}
	var reference githubReference
	if err := adapter.requestJSON(ctx, credential.Secret, http.MethodGet,
		adapter.repositoryPath("git", "ref", "heads", request.Branch), nil, nil, &reference); err == nil {
		truth.Available = true
		if reference.Ref == "refs/heads/"+request.Branch && reference.Object.Type == "commit" &&
			reference.Object.SHA == request.HeadRevision {
			truth.ReachableFromRemoteRefs = []string{"github/" + request.Branch}
		}
	}

	// The pull request is looked up BY HEAD BRANCH across every state. A record
	// that was never written, or written and lost, must not make landed work
	// look unlanded.
	query := url.Values{"head": {adapter.config.Owner + ":" + request.Branch}, "state": {"all"}}
	var summaries []githubPullSummary
	if err := adapter.requestJSON(ctx, credential.Secret, http.MethodGet,
		adapter.repositoryPath("pulls"), query, nil, &summaries); err != nil {
		return truth, nil
	}
	truth.Available = true

	for _, summary := range summaries {
		if summary.Number < 1 {
			continue
		}
		var pull githubPull
		if err := adapter.requestJSON(ctx, credential.Secret, http.MethodGet,
			adapter.repositoryPath("pulls", strconv.Itoa(summary.Number)), nil, nil, &pull); err != nil {
			continue
		}
		if pull.Number != summary.Number || !pull.Merged {
			continue
		}
		headMatches := pull.Head.SHA == request.HeadRevision
		contains := false
		if pull.MergeCommitSHA != nil && revisionPattern.MatchString(*pull.MergeCommitSHA) {
			var comparison githubComparison
			if err := adapter.requestJSON(ctx, credential.Secret, http.MethodGet,
				adapter.repositoryPath("compare", *pull.MergeCommitSHA+"..."+request.HeadRevision),
				nil, nil, &comparison); err == nil {
				contains = comparisonContains(comparison.Status)
			}
		}
		observed := &application.MergedPullRequestTruth{
			Number: pull.Number, Merged: true, MergeCommitContainsHead: contains,
			HeadRevisionMatches: headMatches,
		}
		if truth.MergedPullRequest == nil || headMatches || contains {
			truth.MergedPullRequest = observed
		}
		if headMatches || contains {
			break
		}
	}

	// Exact commit containment in the default branch remains a separate proof
	// when no matching merged pull request is available.
	var containment githubComparison
	if err := adapter.requestJSON(ctx, credential.Secret, http.MethodGet,
		adapter.repositoryPath("compare", adapter.config.BaseBranch+"..."+request.HeadRevision),
		nil, nil, &containment); err == nil {
		truth.DefaultBranchContainsHead = comparisonContains(containment.Status)
		// The comparison was answered by the forge just now, so the base it
		// compared against is current by construction.
		truth.DefaultBranchUpToDate = true
		truth.DefaultBranchHead = request.HeadRevision
	}
	return truth, nil
}

var _ application.LandedEvidenceGatherer = (*GitHubAdapter)(nil)
