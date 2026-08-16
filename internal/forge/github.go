package forge

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/comisai/comis-dev-crew/internal/domain"
)

const maximumForgeResponseBytes = 1 << 20

var (
	githubNamePattern          = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,99}$`)
	forgeIDPattern             = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{2,63}$`)
	operationIDPattern         = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{2,127}$`)
	branchPattern              = regexp.MustCompile(`^devcrew/[A-Za-z0-9][A-Za-z0-9._/-]{0,199}$`)
	revisionPattern            = regexp.MustCompile(`^(?:[0-9a-f]{40}|[0-9a-f]{64})$`)
	pullRequestPattern         = regexp.MustCompile(`^github-pr-[1-9][0-9]{0,9}$`)
	errPullRequestTruthDiffers = errors.New("pull-request truth differs")
)

// GitHubConfig supplies one fixed repository and separate non-merge identities.
type GitHubConfig struct {
	APIBaseURL         string
	Owner              string
	Repository         string
	RepositoryIdentity string
	BaseBranch         string
	HTTPClient         *http.Client
	Pusher             BranchPusher
	ReadCredentials    CredentialSource
	PushCredentials    CredentialSource
}

// GitHubAdapter owns the bounded idempotent push, pull-request, and check flow.
type GitHubAdapter struct {
	config GitHubConfig
	base   *url.URL
}

// NewGitHubAdapter validates immutable operator-owned repository wiring.
func NewGitHubAdapter(config GitHubConfig) (*GitHubAdapter, error) {
	base, err := url.Parse(config.APIBaseURL)
	if err != nil || base.User != nil || base.RawQuery != "" || base.Fragment != "" || base.Host == "" ||
		(base.Scheme != "https" && !(base.Scheme == "http" && loopbackHost(base.Hostname()))) {
		return nil, errors.New("create GitHub adapter: API base URL is invalid")
	}
	if !githubNamePattern.MatchString(config.Owner) || !githubNamePattern.MatchString(config.Repository) ||
		!forgeIDPattern.MatchString(config.RepositoryIdentity) || !githubNamePattern.MatchString(config.BaseBranch) ||
		config.HTTPClient == nil || config.Pusher == nil || config.ReadCredentials == nil || config.PushCredentials == nil {
		return nil, errors.New("create GitHub adapter: repository and dependencies are required")
	}
	return &GitHubAdapter{config: config, base: base}, nil
}

// DeliverPullRequest pushes once, creates or resolves a PR idempotently, then
// re-reads the exact head and required checks from forge truth.
func (adapter *GitHubAdapter) DeliverPullRequest(ctx context.Context, request PullRequestRequest) (PullRequestTruth, error) {
	if adapter == nil || ctx == nil {
		return PullRequestTruth{}, errors.New("deliver GitHub pull request: adapter and context are required")
	}
	if err := ctx.Err(); err != nil {
		return PullRequestTruth{}, err
	}
	if err := validatePullRequestRequest(request); err != nil {
		return PullRequestTruth{}, err
	}
	readCredential, err := adapter.config.ReadCredentials.Resolve(ctx)
	if err != nil || !validReadCredential(readCredential) {
		return PullRequestTruth{}, errors.New("deliver GitHub pull request: read credential is unavailable")
	}
	pushCredential, err := adapter.config.PushCredentials.Resolve(ctx)
	if err != nil || !validPushCredential(pushCredential) {
		return PullRequestTruth{}, errors.New("deliver GitHub pull request: push credential is unavailable")
	}
	if readCredential.Secret == pushCredential.Secret {
		return PullRequestTruth{}, errors.New("deliver GitHub pull request: read and push identities must differ")
	}
	if err := adapter.config.Pusher.Push(ctx, pushCredential, BranchPushRequest{
		OperationID: request.OperationID, WorktreePath: request.WorktreePath,
		Branch: request.Branch, HeadRevision: request.HeadRevision,
	}); err != nil {
		return PullRequestTruth{}, errors.New("deliver GitHub pull request: exact branch push failed")
	}
	number, err := adapter.resolvePullRequest(ctx, readCredential.Secret, request)
	if err != nil {
		return PullRequestTruth{}, err
	}
	pull, err := adapter.readPullRequest(ctx, readCredential.Secret, number)
	if err != nil {
		return PullRequestTruth{}, err
	}
	if pull.State != "open" || pull.Head.SHA != request.HeadRevision || pull.Head.Ref != request.Branch || pull.Base.Ref != adapter.config.BaseBranch {
		return PullRequestTruth{}, errors.New("deliver GitHub pull request: re-read identity differs")
	}
	if err := validatePullRequestURL(pull.HTMLURL); err != nil {
		return PullRequestTruth{}, err
	}
	checks, err := adapter.readChecks(ctx, readCredential.Secret, request.HeadRevision, request.RequiredChecks)
	if err != nil {
		return PullRequestTruth{}, err
	}
	return PullRequestTruth{
		URL: pull.HTMLURL,
		Evidence: domain.ForgeEvidence{
			Repository:    adapter.config.RepositoryIdentity,
			PullRequestID: "github-pr-" + strconv.Itoa(number), HeadRevision: request.HeadRevision,
			CheckConclusions: checks,
		},
	}, nil
}

// VerifyPullRequest re-reads an exact recorded pull request and its required
// checks using only the separately configured read authority.
func (adapter *GitHubAdapter) VerifyPullRequest(
	ctx context.Context,
	request PullRequestVerificationRequest,
) (PullRequestTruth, error) {
	if adapter == nil || ctx == nil {
		return PullRequestTruth{}, errors.New("verify GitHub pull request: adapter and context are required")
	}
	if err := ctx.Err(); err != nil {
		return PullRequestTruth{}, err
	}
	if !branchPattern.MatchString(request.Branch) || strings.Contains(request.Branch, "..") ||
		!revisionPattern.MatchString(request.HeadRevision) || !pullRequestPattern.MatchString(request.PullRequestID) ||
		validateRequiredChecks(request.RequiredChecks) != nil {
		return PullRequestTruth{}, errors.New("verify GitHub pull request: request is invalid")
	}
	number, err := strconv.Atoi(strings.TrimPrefix(request.PullRequestID, "github-pr-"))
	if err != nil || number < 1 || "github-pr-"+strconv.Itoa(number) != request.PullRequestID {
		return PullRequestTruth{}, errors.New("verify GitHub pull request: pull-request identity is invalid")
	}
	readCredential, err := adapter.config.ReadCredentials.Resolve(ctx)
	if err != nil || !validReadCredential(readCredential) {
		return PullRequestTruth{}, errors.New("verify GitHub pull request: read credential is unavailable")
	}
	pull, err := adapter.readPullRequest(ctx, readCredential.Secret, number)
	if err != nil {
		return PullRequestTruth{}, err
	}
	if pull.State != "open" || pull.Head.SHA != request.HeadRevision || pull.Head.Ref != request.Branch ||
		pull.Base.Ref != adapter.config.BaseBranch {
		return PullRequestTruth{}, fmt.Errorf("verify GitHub pull request: %w", errPullRequestTruthDiffers)
	}
	if err := validatePullRequestURL(pull.HTMLURL); err != nil {
		return PullRequestTruth{}, err
	}
	checks, err := adapter.readChecks(ctx, readCredential.Secret, request.HeadRevision, request.RequiredChecks)
	if err != nil {
		return PullRequestTruth{}, err
	}
	return PullRequestTruth{
		URL: pull.HTMLURL,
		Evidence: domain.ForgeEvidence{
			Repository: adapter.config.RepositoryIdentity, PullRequestID: request.PullRequestID,
			HeadRevision: request.HeadRevision, CheckConclusions: checks,
		},
	}, nil
}

type githubPullSummary struct {
	Number int `json:"number"`
}

type githubPull struct {
	Number  int    `json:"number"`
	State   string `json:"state"`
	HTMLURL string `json:"html_url"`
	Head    struct {
		SHA string `json:"sha"`
		Ref string `json:"ref"`
	} `json:"head"`
	Base struct {
		Ref string `json:"ref"`
	} `json:"base"`
}

type githubChecks struct {
	Runs []struct {
		ID         int64   `json:"id"`
		Name       string  `json:"name"`
		Status     string  `json:"status"`
		Conclusion *string `json:"conclusion"`
		StartedAt  string  `json:"started_at"`
	} `json:"check_runs"`
}

func (adapter *GitHubAdapter) resolvePullRequest(ctx context.Context, secret string, request PullRequestRequest) (int, error) {
	query := url.Values{"head": {adapter.config.Owner + ":" + request.Branch}, "state": {"open"}}
	var pulls []githubPullSummary
	if err := adapter.requestJSON(ctx, secret, http.MethodGet, adapter.repositoryPath("pulls"), query, nil, &pulls); err != nil {
		return 0, err
	}
	if len(pulls) > 1 || len(pulls) == 1 && pulls[0].Number < 1 {
		return 0, errors.New("deliver GitHub pull request: pull-request lookup is ambiguous")
	}
	if len(pulls) == 1 {
		return pulls[0].Number, nil
	}
	body := struct {
		Title string `json:"title"`
		Head  string `json:"head"`
		Base  string `json:"base"`
	}{Title: request.Title, Head: request.Branch, Base: adapter.config.BaseBranch}
	var created githubPullSummary
	if err := adapter.requestJSON(ctx, secret, http.MethodPost, adapter.repositoryPath("pulls"), nil, body, &created); err != nil {
		return 0, err
	}
	if created.Number < 1 {
		return 0, errors.New("deliver GitHub pull request: created identity is invalid")
	}
	return created.Number, nil
}

func (adapter *GitHubAdapter) readPullRequest(ctx context.Context, secret string, number int) (githubPull, error) {
	var pull githubPull
	if err := adapter.requestJSON(ctx, secret, http.MethodGet, adapter.repositoryPath("pulls", strconv.Itoa(number)), nil, nil, &pull); err != nil {
		return githubPull{}, err
	}
	if pull.Number != number {
		return githubPull{}, errors.New("deliver GitHub pull request: pull-request identity differs")
	}
	return pull, nil
}

func (adapter *GitHubAdapter) readChecks(
	ctx context.Context,
	secret, head string,
	required []string,
) ([]domain.ForgeCheckEvidence, error) {
	var response githubChecks
	if err := adapter.requestJSON(ctx, secret, http.MethodGet, adapter.repositoryPath("commits", head, "check-runs"), nil, nil, &response); err != nil {
		return nil, err
	}
	type observedCheck struct {
		id           int64
		startedAt    time.Time
		recencyKnown bool
		conclusion   domain.CheckConclusion
	}
	observed := make(map[string]observedCheck, len(response.Runs))
	seenIDs := make(map[int64]struct{}, len(response.Runs))
	for _, run := range response.Runs {
		if run.Name == "" || len(run.Name) > 128 || strings.TrimSpace(run.Name) != run.Name {
			return nil, errors.New("deliver GitHub pull request: check identity is invalid")
		}
		if run.ID < 1 {
			return nil, errors.New("deliver GitHub pull request: check identity is invalid")
		}
		if _, duplicate := seenIDs[run.ID]; duplicate {
			return nil, errors.New("deliver GitHub pull request: check identity is duplicated")
		}
		seenIDs[run.ID] = struct{}{}
		startedAt := time.Time{}
		recencyKnown := run.StartedAt != ""
		if recencyKnown {
			var err error
			startedAt, err = time.Parse(time.RFC3339, run.StartedAt)
			if err != nil || startedAt.IsZero() {
				return nil, errors.New("deliver GitHub pull request: check recency is invalid")
			}
		}
		current, exists := observed[run.Name]
		if exists && (!current.recencyKnown || !recencyKnown) {
			current.recencyKnown = false
			current.conclusion = domain.CheckUnknown
			observed[run.Name] = current
			continue
		}
		if exists && (startedAt.Before(current.startedAt) || startedAt.Equal(current.startedAt) && run.ID < current.id) {
			continue
		}
		observed[run.Name] = observedCheck{
			id: run.ID, startedAt: startedAt, recencyKnown: recencyKnown,
			conclusion: githubCheckConclusion(run.Status, run.Conclusion),
		}
	}
	evidence := make([]domain.ForgeCheckEvidence, 0, len(required))
	for _, name := range required {
		check, exists := observed[name]
		conclusion := check.conclusion
		if !exists {
			conclusion = domain.CheckUnknown
		}
		evidence = append(evidence, domain.ForgeCheckEvidence{Name: name, Conclusion: conclusion})
	}
	return evidence, nil
}

func githubCheckConclusion(status string, conclusion *string) domain.CheckConclusion {
	if status != "completed" {
		if status == "queued" || status == "in_progress" || status == "pending" {
			return domain.CheckPending
		}
		return domain.CheckUnknown
	}
	if conclusion == nil {
		return domain.CheckUnknown
	}
	switch *conclusion {
	case "success", "neutral", "skipped":
		return domain.CheckPassed
	case "failure", "cancelled", "timed_out", "action_required", "startup_failure":
		return domain.CheckFailed
	default:
		return domain.CheckUnknown
	}
}

func (adapter *GitHubAdapter) requestJSON(
	ctx context.Context,
	secret, method, path string,
	query url.Values,
	body any,
	destination any,
) error {
	endpoint := *adapter.base
	endpoint.Path = strings.TrimSuffix(adapter.base.Path, "/") + path
	endpoint.RawQuery = query.Encode()
	var encoded io.Reader
	if body != nil {
		contents, err := json.Marshal(body)
		if err != nil {
			return errors.New("GitHub request body could not be encoded")
		}
		encoded = bytes.NewReader(contents)
	}
	request, err := http.NewRequestWithContext(ctx, method, endpoint.String(), encoded)
	if err != nil {
		return errors.New("GitHub request could not be created")
	}
	request.Header.Set("Accept", "application/vnd.github+json")
	request.Header.Set("Authorization", "Bearer "+secret)
	request.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := adapter.config.HTTPClient.Do(request)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if retryableGitHubNetworkError(err) {
			return fmt.Errorf("GitHub request failed: %w", ErrPullRequestTruthUnavailable)
		}
		return errors.New("GitHub request failed")
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode < 200 || response.StatusCode > 299 {
		if retryableGitHubResponse(response) {
			return fmt.Errorf("GitHub request returned a temporary status: %w", ErrPullRequestTruthUnavailable)
		}
		return errors.New("GitHub request returned a non-success status")
	}
	limited := io.LimitReader(response.Body, maximumForgeResponseBytes+1)
	contents, err := io.ReadAll(limited)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if retryableGitHubNetworkError(err) {
			return fmt.Errorf("GitHub response could not be read: %w", ErrPullRequestTruthUnavailable)
		}
		return errors.New("GitHub response could not be read")
	}
	if len(contents) == 0 || len(contents) > maximumForgeResponseBytes {
		return errors.New("GitHub response is invalid or exceeds its bound")
	}
	if destination == nil {
		return nil
	}
	decoder := json.NewDecoder(bytes.NewReader(contents))
	if err := decoder.Decode(destination); err != nil {
		return errors.New("GitHub response is malformed")
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("GitHub response has trailing content")
	}
	return nil
}

func retryableGitHubNetworkError(err error) bool {
	var networkError net.Error
	//lint:ignore SA1019 Preserve explicit temporary signals from legacy network errors as retryable forge truth failures.
	return errors.As(err, &networkError) && (networkError.Timeout() || networkError.Temporary())
}

func retryableGitHubResponse(response *http.Response) bool {
	if response == nil {
		return false
	}
	switch response.StatusCode {
	case http.StatusRequestTimeout, http.StatusTooEarly, http.StatusTooManyRequests:
		return true
	case http.StatusForbidden:
		return strings.TrimSpace(response.Header.Get("Retry-After")) != "" ||
			strings.TrimSpace(response.Header.Get("X-RateLimit-Remaining")) == "0"
	default:
		return response.StatusCode >= 500 && response.StatusCode <= 599
	}
}

func (adapter *GitHubAdapter) repositoryPath(parts ...string) string {
	all := append([]string{"", "repos", adapter.config.Owner, adapter.config.Repository}, parts...)
	return strings.Join(all, "/")
}

func validatePullRequestRequest(request PullRequestRequest) error {
	if !operationIDPattern.MatchString(request.OperationID) || !filepath.IsAbs(request.WorktreePath) ||
		filepath.Clean(request.WorktreePath) != request.WorktreePath || !branchPattern.MatchString(request.Branch) ||
		strings.Contains(request.Branch, "..") || !revisionPattern.MatchString(request.HeadRevision) ||
		request.Title == "" || len(request.Title) > 256 || strings.TrimSpace(request.Title) != request.Title ||
		strings.ContainsAny(request.Title, "\x00\r\n") || validateRequiredChecks(request.RequiredChecks) != nil {
		return errors.New("deliver GitHub pull request: request is invalid")
	}
	return nil
}

func validateRequiredChecks(required []string) error {
	if len(required) == 0 || len(required) > 64 {
		return errors.New("required checks are invalid")
	}
	seen := make(map[string]struct{}, len(required))
	for _, check := range required {
		if check == "" || len(check) > 128 || strings.TrimSpace(check) != check || strings.ContainsAny(check, "\x00\r\n") {
			return errors.New("required check is invalid")
		}
		if _, exists := seen[check]; exists {
			return errors.New("required check is duplicated")
		}
		seen[check] = struct{}{}
	}
	return nil
}

func validReadCredential(credential Credential) bool {
	return credential.Kind == CredentialRead && validSecret(credential.Secret) &&
		equalScopes(credential.Scopes, []CredentialScope{ScopeContentsRead, ScopePullRequestsRead, ScopeChecksRead})
}

func validPushCredential(credential Credential) bool {
	return credential.Kind == CredentialPush && validSecret(credential.Secret) &&
		equalScopes(credential.Scopes, []CredentialScope{ScopeContentsWrite})
}

func validSecret(secret string) bool {
	return secret != "" && len(secret) <= 4096 && !strings.ContainsAny(secret, "\x00\r\n\t ")
}

func equalScopes(got, want []CredentialScope) bool {
	if len(got) != len(want) {
		return false
	}
	counts := make(map[CredentialScope]int, len(got))
	for _, scope := range got {
		counts[scope]++
	}
	for _, scope := range want {
		if counts[scope] != 1 {
			return false
		}
	}
	return true
}

func validatePullRequestURL(value string) error {
	parsed, err := url.Parse(value)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil || parsed.Fragment != "" {
		return errors.New("deliver GitHub pull request: pull-request URL is invalid")
	}
	return nil
}

func loopbackHost(host string) bool {
	if host == "localhost" {
		return true
	}
	address := net.ParseIP(host)
	return address != nil && address.IsLoopback()
}
