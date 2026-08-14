package forge

import (
	"context"
	"errors"

	"github.com/comisai/comis-dev-crew/internal/application"
)

// VerifyPullRequestDelivery implements the application cleanup port using
// only the adapter's read-only exact-delivery path.
func (adapter *GitHubAdapter) VerifyPullRequestDelivery(
	ctx context.Context,
	request application.PullRequestDeliveryVerification,
) (application.PullRequestDeliveryTruth, error) {
	if adapter == nil || request.RepositoryID != adapter.config.RepositoryIdentity {
		return application.PullRequestDeliveryTruth{}, errors.New("verify pull request delivery: repository identity differs")
	}
	truth, err := adapter.VerifyPullRequest(ctx, PullRequestVerificationRequest{
		Branch: request.Branch, HeadRevision: request.HeadRevision,
		PullRequestID: request.PullRequestID, RequiredChecks: request.RequiredChecks,
	})
	if err != nil {
		if errors.Is(err, errPullRequestTruthDiffers) {
			return application.PullRequestDeliveryTruth{}, application.ErrCleanupStaleForgeTruth
		}
		return application.PullRequestDeliveryTruth{}, err
	}
	checks := make([]application.ForgeCheckTruth, len(truth.Evidence.CheckConclusions))
	for index, check := range truth.Evidence.CheckConclusions {
		checks[index] = application.ForgeCheckTruth{Name: check.Name, Conclusion: check.Conclusion}
	}
	return application.PullRequestDeliveryTruth{
		RepositoryID: truth.Evidence.Repository, PullRequestID: truth.Evidence.PullRequestID,
		HeadRevision: truth.Evidence.HeadRevision, Checks: checks,
	}, nil
}

var _ application.PullRequestDeliveryVerifier = (*GitHubAdapter)(nil)
