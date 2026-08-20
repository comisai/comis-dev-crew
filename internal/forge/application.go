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

// MergeApprovedPullRequest implements the application mutation port while
// keeping forge DTOs and the configured strategy inside the adapter package.
func (adapter *GitHubAdapter) MergeApprovedPullRequest(
	ctx context.Context,
	request application.PullRequestMergeRequest,
) (application.PullRequestMergeReceipt, error) {
	if adapter == nil || request.RepositoryID != adapter.config.RepositoryIdentity {
		return application.PullRequestMergeReceipt{}, errors.New("merge approved pull request: repository identity differs")
	}
	receipt, err := adapter.MergePullRequest(ctx, PullRequestMergeRequest{
		OperationID: request.OperationID, PullRequestID: request.PullRequestID,
		Branch: request.Branch, HeadRevision: request.HeadRevision,
		RequiredChecks: append([]string(nil), request.RequiredChecks...),
	})
	if err != nil {
		return application.PullRequestMergeReceipt{}, err
	}
	method, err := applicationMergeMethod(receipt.Method)
	if err != nil {
		return application.PullRequestMergeReceipt{}, err
	}
	return application.PullRequestMergeReceipt{
		RepositoryID: receipt.RepositoryID, PullRequestID: receipt.PullRequestID,
		HeadRevision: receipt.HeadRevision, MergeCommitRevision: receipt.MergeCommitRevision,
		Method: method,
	}, nil
}

func applicationMergeMethod(method MergeMethod) (application.PullRequestMergeMethod, error) {
	switch method {
	case MergeCommit:
		return application.PullRequestMergeCommit, nil
	case MergeSquash:
		return application.PullRequestMergeSquash, nil
	case MergeRebase:
		return application.PullRequestMergeRebase, nil
	default:
		return "", errors.New("merge approved pull request: method is invalid")
	}
}

var _ application.ApprovedPullRequestMerger = (*GitHubAdapter)(nil)
