package forge

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"

	"github.com/comisai/comis-dev-crew/internal/domain"
)

const (
	githubCheckRunsPerPage  = 100
	maximumGitHubCheckPages = 10
)

func (adapter *GitHubAdapter) readAllCheckRuns(
	ctx context.Context,
	secret, head string,
) (githubChecks, bool, error) {
	var all githubChecks
	expectedTotal := -1
	for page := 1; page <= maximumGitHubCheckPages; page++ {
		query := url.Values{
			"filter": {"all"}, "page": {strconv.Itoa(page)},
			"per_page": {strconv.Itoa(githubCheckRunsPerPage)},
		}
		var response githubChecks
		if err := adapter.requestJSON(
			ctx, secret, http.MethodGet, adapter.repositoryPath("commits", head, "check-runs"), query, nil, &response,
		); err != nil {
			return githubChecks{}, false, err
		}
		total, valid := parseGitHubCheckTotal(response.TotalCount)
		if !valid || total > githubCheckRunsPerPage*maximumGitHubCheckPages || len(response.Runs) > githubCheckRunsPerPage {
			return githubChecks{}, false, nil
		}
		if expectedTotal < 0 {
			expectedTotal = total
		} else if total != expectedTotal {
			return githubChecks{}, false, nil
		}
		all.Runs = append(all.Runs, response.Runs...)
		if len(all.Runs) == expectedTotal {
			return all, true, nil
		}
		if len(all.Runs) > expectedTotal || len(response.Runs) != githubCheckRunsPerPage {
			return githubChecks{}, false, nil
		}
	}
	return githubChecks{}, false, nil
}

func parseGitHubCheckTotal(raw json.RawMessage) (int, bool) {
	var total int
	if len(raw) == 0 || json.Unmarshal(raw, &total) != nil || total < 0 {
		return 0, false
	}
	return total, true
}

func unknownRequiredChecks(required []string) []domain.ForgeCheckEvidence {
	evidence := make([]domain.ForgeCheckEvidence, 0, len(required))
	for _, name := range required {
		evidence = append(evidence, domain.ForgeCheckEvidence{Name: name, Conclusion: domain.CheckUnknown})
	}
	return evidence
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
