package forge

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/comisai/comis-dev-crew/internal/domain"
)

// MergePullRequest revalidates exact protected forge truth before resolving
// the separately configured merge credential. It returns only a mutation
// acknowledgement corroborated by exact post-mutation forge truth.
func (adapter *GitHubAdapter) MergePullRequest(
	ctx context.Context,
	request PullRequestMergeRequest,
) (PullRequestMergeReceipt, error) {
	if adapter == nil || ctx == nil {
		return PullRequestMergeReceipt{}, errors.New("merge GitHub pull request: adapter and context are required")
	}
	if err := ctx.Err(); err != nil {
		return PullRequestMergeReceipt{}, err
	}
	if err := validatePullRequestMergeRequest(request); err != nil {
		return PullRequestMergeReceipt{}, err
	}
	if adapter.config.MergeCredentials == nil || !validMergeMethod(adapter.config.MergeMethod) {
		return PullRequestMergeReceipt{}, errors.New("merge GitHub pull request: merge authority is disabled")
	}
	number, err := pullRequestNumber(request.PullRequestID)
	if err != nil {
		return PullRequestMergeReceipt{}, err
	}
	readCredential, err := adapter.config.ReadCredentials.Resolve(ctx)
	if err != nil || !validReadCredential(readCredential) {
		return PullRequestMergeReceipt{}, errors.New("merge GitHub pull request: read credential is unavailable")
	}
	pull, err := adapter.readPullRequest(ctx, readCredential.Secret, number)
	if err != nil {
		return PullRequestMergeReceipt{}, err
	}
	if _, merged := adapter.exactMergedRevision(request, pull); merged {
		return PullRequestMergeReceipt{}, fmt.Errorf(
			"merge GitHub pull request: actual merge method is unavailable: %w",
			ErrPullRequestMergeOutcomeUnknown,
		)
	}
	if pull.State != "open" || pull.Merged || pull.Head.SHA != request.HeadRevision ||
		pull.Head.Ref != request.Branch || pull.Base.Ref != adapter.config.BaseBranch {
		return PullRequestMergeReceipt{}, errors.New("merge GitHub pull request: approved pull-request identity changed")
	}
	checks, err := adapter.readChecks(ctx, readCredential.Secret, request.HeadRevision, request.RequiredChecks)
	if err != nil {
		return PullRequestMergeReceipt{}, err
	}
	if !allMergeChecksPassed(checks, request.RequiredChecks) {
		return PullRequestMergeReceipt{}, errors.New("merge GitHub pull request: required checks are not currently passing")
	}
	if err := adapter.verifyBranchProtection(ctx, readCredential.Secret, request.RequiredChecks); err != nil {
		return PullRequestMergeReceipt{}, err
	}
	mergeCredential, err := adapter.config.MergeCredentials.Resolve(ctx)
	if err != nil || !validMergeCredential(mergeCredential) {
		return PullRequestMergeReceipt{}, errors.New("merge GitHub pull request: merge credential is unavailable")
	}
	if mergeCredential.Secret == readCredential.Secret {
		return PullRequestMergeReceipt{}, errors.New("merge GitHub pull request: read and merge identities must differ")
	}
	mutationAt := adapter.config.Clock()
	if mutationAt.IsZero() || mutationAt.Location() != time.UTC ||
		!mutationAt.Before(request.AuthorityExpiresAt) {
		return PullRequestMergeReceipt{}, errors.New("merge GitHub pull request: merge authority expired before mutation")
	}
	body := struct {
		SHA         string      `json:"sha"`
		MergeMethod MergeMethod `json:"merge_method"`
	}{SHA: request.HeadRevision, MergeMethod: request.Method}
	var response githubMergeResponse
	mutationErr := adapter.requestJSON(
		ctx, mergeCredential.Secret, http.MethodPut,
		adapter.repositoryPath("pulls", strconv.Itoa(number), "merge"), nil, body, &response,
	)
	postMerge, readErr := adapter.readPullRequest(ctx, readCredential.Secret, number)
	if readErr == nil {
		if mergeRevision, merged := adapter.exactMergedRevision(request, postMerge); merged {
			if mutationErr != nil || !response.Merged || response.SHA != mergeRevision {
				return PullRequestMergeReceipt{}, fmt.Errorf(
					"merge GitHub pull request: acknowledged method is not proved by forge truth: %w",
					ErrPullRequestMergeOutcomeUnknown,
				)
			}
			return PullRequestMergeReceipt{
				RepositoryID: adapter.config.RepositoryIdentity, PullRequestID: request.PullRequestID,
				HeadRevision: request.HeadRevision, MergeCommitRevision: mergeRevision, Method: request.Method,
			}, nil
		}
	}
	if mutationErr != nil {
		return PullRequestMergeReceipt{}, fmt.Errorf("merge GitHub pull request: mutation could not be reconciled: %w", ErrPullRequestMergeOutcomeUnknown)
	}
	return PullRequestMergeReceipt{}, fmt.Errorf("merge GitHub pull request: post-merge truth is unavailable: %w", ErrPullRequestMergeOutcomeUnknown)
}

func pullRequestNumber(pullRequestID string) (int, error) {
	number, err := strconv.Atoi(strings.TrimPrefix(pullRequestID, "github-pr-"))
	if err != nil || number < 1 || "github-pr-"+strconv.Itoa(number) != pullRequestID {
		return 0, errors.New("merge GitHub pull request: pull-request identity is invalid")
	}
	return number, nil
}

func (adapter *GitHubAdapter) exactMergedRevision(
	request PullRequestMergeRequest,
	pull githubPull,
) (string, bool) {
	if pull.State != "closed" || !pull.Merged || pull.Head.SHA != request.HeadRevision ||
		pull.Head.Ref != request.Branch || pull.Base.Ref != adapter.config.BaseBranch ||
		pull.MergeCommitSHA == nil || !revisionPattern.MatchString(*pull.MergeCommitSHA) {
		return "", false
	}
	return *pull.MergeCommitSHA, true
}

func allMergeChecksPassed(checks []domain.ForgeCheckEvidence, required []string) bool {
	if len(checks) != len(required) {
		return false
	}
	for index, check := range checks {
		if check.Name != required[index] || check.Conclusion != domain.CheckPassed {
			return false
		}
	}
	return true
}

func (adapter *GitHubAdapter) verifyBranchProtection(
	ctx context.Context,
	secret string,
	requiredChecks []string,
) error {
	var protection githubBranchProtection
	if err := adapter.requestJSON(
		ctx, secret, http.MethodGet, adapter.repositoryPath("branches", adapter.config.BaseBranch, "protection"),
		nil, nil, &protection,
	); err != nil {
		return fmt.Errorf("merge GitHub pull request: branch protection is unavailable: %w", err)
	}
	if protection.RequiredStatusChecks == nil || !protection.RequiredStatusChecks.Strict ||
		protection.EnforceAdmins == nil || !protection.EnforceAdmins.Enabled ||
		!sameCheckSet(protection.RequiredStatusChecks.Contexts, requiredChecks) {
		return errors.New("merge GitHub pull request: branch protection does not match required checks")
	}
	return nil
}

func sameCheckSet(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	counts := make(map[string]int, len(left))
	for _, value := range left {
		counts[value]++
	}
	for _, value := range right {
		if counts[value] != 1 {
			return false
		}
	}
	return true
}
