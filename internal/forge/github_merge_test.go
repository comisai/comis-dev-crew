package forge

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/comisai/comis-dev-crew/internal/application"
)

func TestGitHubAdapter_MergesOnlyAfterFreshProtectedTruth(t *testing.T) {
	head := strings.Repeat("a", 40)
	mergeCommit := strings.Repeat("b", 40)
	var mu sync.Mutex
	requests := make([]string, 0, 8)
	merged := false
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		mu.Lock()
		requests = append(requests, request.Method+" "+request.URL.RequestURI()+" "+request.Header.Get("Authorization"))
		mu.Unlock()
		response.Header().Set("Content-Type", "application/json")
		switch request.Method + " " + request.URL.Path {
		case "GET /repos/comisai/fixture/pulls/31":
			state, mergedJSON, commit := "open", "false", "null"
			if merged {
				state, mergedJSON, commit = "closed", "true", `"`+mergeCommit+`"`
			}
			_, _ = response.Write([]byte(`{"number":31,"state":"` + state + `","merged":` + mergedJSON +
				`,"merge_commit_sha":` + commit + `,"html_url":"https://example.com/comisai/fixture/pull/31",` +
				`"head":{"sha":"` + head + `","ref":"devcrew/task-merge"},"base":{"ref":"main"}}`))
		case "GET /repos/comisai/fixture/commits/" + head + "/check-runs":
			_, _ = response.Write([]byte(`{"total_count":1,"check_runs":[{"id":31,"name":"ci/unit","status":"completed","conclusion":"success","started_at":"2026-08-20T10:00:00Z"}]}`))
		case "GET /repos/comisai/fixture/branches/main/protection":
			_, _ = response.Write([]byte(`{"required_status_checks":{"strict":true,"contexts":["ci/unit"]},"enforce_admins":{"enabled":true}}`))
		case "PUT /repos/comisai/fixture/pulls/31/merge":
			if request.Header.Get("Authorization") != "Bearer merge-token" {
				t.Errorf("merge authorization = %q", request.Header.Get("Authorization"))
			}
			var body map[string]string
			if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
				t.Errorf("decode merge body: %v", err)
			}
			if body["sha"] != head || body["merge_method"] != "squash" {
				t.Errorf("merge body = %#v", body)
			}
			merged = true
			_, _ = response.Write([]byte(`{"sha":"` + mergeCommit + `","merged":true,"message":"Pull Request successfully merged"}`))
		default:
			http.NotFound(response, request)
		}
	}))
	t.Cleanup(server.Close)
	events := make([]string, 0, 1)
	configuration := validGitHubConfig(server)
	configuration.MergeCredentials = recordingCredentialSource{
		events:     &events,
		credential: Credential{Kind: CredentialMerge, Secret: "merge-token", Scopes: []CredentialScope{ScopePullRequestsWrite}},
	}
	configuration.MergeMethod = MergeSquash
	adapter, err := NewGitHubAdapter(configuration)
	if err != nil {
		t.Fatalf("NewGitHubAdapter() error = %v", err)
	}
	receipt, err := adapter.MergePullRequest(context.Background(), PullRequestMergeRequest{
		OperationID: "merge-task-0001", Branch: "devcrew/task-merge", HeadRevision: head,
		PullRequestID: "github-pr-31", Method: MergeSquash, RequiredChecks: []string{"ci/unit"},
		AuthorityExpiresAt: time.Date(2026, time.August, 20, 12, 5, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("MergePullRequest() error = %v", err)
	}
	wantReceipt := PullRequestMergeReceipt{
		RepositoryID: "fixture-repository", PullRequestID: "github-pr-31", HeadRevision: head,
		MergeCommitRevision: mergeCommit, Method: MergeSquash,
	}
	if !reflect.DeepEqual(receipt, wantReceipt) {
		t.Fatalf("MergePullRequest() = %#v, want %#v", receipt, wantReceipt)
	}
	if !reflect.DeepEqual(events, []string{"merge-credential-resolved"}) {
		t.Fatalf("credential events = %#v", events)
	}
	mu.Lock()
	defer mu.Unlock()
	wantRequests := []string{
		"GET /repos/comisai/fixture/pulls/31 Bearer read-token",
		"GET /repos/comisai/fixture/commits/" + head + "/check-runs?filter=all&page=1&per_page=100 Bearer read-token",
		"GET /repos/comisai/fixture/commits/" + head + "/check-runs?filter=all&page=1&per_page=100 Bearer read-token",
		"GET /repos/comisai/fixture/branches/main/protection Bearer read-token",
		"PUT /repos/comisai/fixture/pulls/31/merge Bearer merge-token",
		"GET /repos/comisai/fixture/pulls/31 Bearer read-token",
	}
	if !reflect.DeepEqual(requests, wantRequests) {
		t.Fatalf("GitHub requests = %#v, want %#v", requests, wantRequests)
	}
}

func TestGitHubAdapter_RefusesMutationAfterAuthorityExpiresDuringPreflight(t *testing.T) {
	head := strings.Repeat("3", 40)
	now := time.Date(2026, time.August, 20, 12, 0, 0, 0, time.UTC)
	expiresAt := now.Add(time.Minute)
	var mu sync.Mutex
	expired, mergeCalls := false, 0
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("Content-Type", "application/json")
		switch request.Method + " " + request.URL.Path {
		case "GET /repos/comisai/fixture/pulls/31":
			_, _ = response.Write([]byte(`{"number":31,"state":"open","merged":false,"merge_commit_sha":null,"html_url":"https://example.com/pull/31","head":{"sha":"` + head + `","ref":"devcrew/task-merge"},"base":{"ref":"main"}}`))
		case "GET /repos/comisai/fixture/commits/" + head + "/check-runs":
			_, _ = response.Write([]byte(`{"total_count":1,"check_runs":[{"id":31,"name":"ci/unit","status":"completed","conclusion":"success","started_at":"2026-08-20T10:00:00Z"}]}`))
		case "GET /repos/comisai/fixture/branches/main/protection":
			mu.Lock()
			expired = true
			mu.Unlock()
			_, _ = response.Write([]byte(`{"required_status_checks":{"strict":true,"contexts":["ci/unit"]},"enforce_admins":{"enabled":true}}`))
		case "PUT /repos/comisai/fixture/pulls/31/merge":
			mu.Lock()
			mergeCalls++
			mu.Unlock()
			http.Error(response, "unexpected mutation", http.StatusInternalServerError)
		default:
			http.NotFound(response, request)
		}
	}))
	t.Cleanup(server.Close)
	configuration := validGitHubConfig(server)
	configuration.MergeCredentials = staticCredentialSource{credential: Credential{
		Kind: CredentialMerge, Secret: "merge-token", Scopes: []CredentialScope{ScopePullRequestsWrite},
	}}
	configuration.MergeMethod = MergeSquash
	configuration.Clock = func() time.Time {
		mu.Lock()
		defer mu.Unlock()
		if expired {
			return expiresAt
		}
		return now
	}
	adapter, err := NewGitHubAdapter(configuration)
	if err != nil {
		t.Fatal(err)
	}
	_, err = adapter.MergePullRequest(context.Background(), PullRequestMergeRequest{
		OperationID: "merge-task-0001", Branch: "devcrew/task-merge", HeadRevision: head,
		PullRequestID: "github-pr-31", Method: MergeSquash, RequiredChecks: []string{"ci/unit"},
		AuthorityExpiresAt: expiresAt,
	})
	mu.Lock()
	defer mu.Unlock()
	if err == nil || mergeCalls != 0 {
		t.Fatalf("MergePullRequest(expired during preflight) calls=%d, error=%v", mergeCalls, err)
	}
}

func TestGitHubAdapter_RejectsDuplicateMergeAuthorityResponses(t *testing.T) {
	head := strings.Repeat("4", 40)
	mergeCommit := strings.Repeat("5", 40)
	validPull := `{"number":31,"state":"open","merged":false,"merge_commit_sha":null,"html_url":"https://example.com/pull/31","head":{"sha":"` + head + `","ref":"devcrew/task-merge"},"base":{"ref":"main"}}`
	validProtection := `{"required_status_checks":{"strict":true,"contexts":["ci/unit"]},"enforce_admins":{"enabled":true}}`
	validResponse := `{"sha":"` + mergeCommit + `","merged":true,"message":"merged"}`
	for _, test := range []struct {
		name, pull, protection, response string
		wantMergeCalls                   int
	}{
		{name: "pull", pull: strings.Replace(validPull, `"merged":false`, `"merged":true,"merged":false`, 1), protection: validProtection, response: validResponse},
		{name: "protection", pull: validPull, protection: strings.Replace(validProtection, `"strict":true`, `"strict":false,"strict":true`, 1), response: validResponse},
		{name: "merge response", pull: validPull, protection: validProtection, response: `{"sha":"` + head + `","sha":"` + mergeCommit + `","merged":false,"merged":true,"message":"merged"}`, wantMergeCalls: 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			var mu sync.Mutex
			merged, mergeCalls := false, 0
			server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
				response.Header().Set("Content-Type", "application/json")
				switch request.Method + " " + request.URL.Path {
				case "GET /repos/comisai/fixture/pulls/31":
					mu.Lock()
					isMerged := merged
					mu.Unlock()
					if isMerged {
						_, _ = response.Write([]byte(`{"number":31,"state":"closed","merged":true,"merge_commit_sha":"` + mergeCommit + `","html_url":"https://example.com/pull/31","head":{"sha":"` + head + `","ref":"devcrew/task-merge"},"base":{"ref":"main"}}`))
					} else {
						_, _ = response.Write([]byte(test.pull))
					}
				case "GET /repos/comisai/fixture/commits/" + head + "/check-runs":
					_, _ = response.Write([]byte(`{"total_count":1,"check_runs":[{"id":31,"name":"ci/unit","status":"completed","conclusion":"success","started_at":"2026-08-20T10:00:00Z"}]}`))
				case "GET /repos/comisai/fixture/branches/main/protection":
					_, _ = response.Write([]byte(test.protection))
				case "PUT /repos/comisai/fixture/pulls/31/merge":
					mu.Lock()
					mergeCalls++
					merged = true
					mu.Unlock()
					_, _ = response.Write([]byte(test.response))
				default:
					http.NotFound(response, request)
				}
			}))
			t.Cleanup(server.Close)
			configuration := validGitHubConfig(server)
			configuration.MergeCredentials = staticCredentialSource{credential: Credential{
				Kind: CredentialMerge, Secret: "merge-token", Scopes: []CredentialScope{ScopePullRequestsWrite},
			}}
			configuration.MergeMethod = MergeSquash
			adapter, err := NewGitHubAdapter(configuration)
			if err != nil {
				t.Fatal(err)
			}
			receipt, err := adapter.MergePullRequest(context.Background(), PullRequestMergeRequest{
				OperationID: "merge-task-0001", Branch: "devcrew/task-merge", HeadRevision: head,
				PullRequestID: "github-pr-31", Method: MergeSquash, RequiredChecks: []string{"ci/unit"},
				AuthorityExpiresAt: time.Date(2026, time.August, 20, 12, 5, 0, 0, time.UTC),
			})
			mu.Lock()
			defer mu.Unlock()
			if err == nil || receipt != (PullRequestMergeReceipt{}) || mergeCalls != test.wantMergeCalls {
				t.Fatalf("MergePullRequest(duplicate %s) = %#v, calls=%d, error=%v", test.name, receipt, mergeCalls, err)
			}
		})
	}
}

func TestGitHubAdapter_RefusesChangedOrUnprotectedMergeBeforeCredentialResolution(t *testing.T) {
	approvedHead := strings.Repeat("c", 40)
	changedHead := strings.Repeat("d", 40)
	for _, test := range []struct {
		name       string
		pullHead   string
		protection string
	}{
		{name: "changed head", pullHead: changedHead, protection: `{"required_status_checks":{"strict":true,"contexts":["ci/unit"]},"enforce_admins":{"enabled":true}}`},
		{name: "unprotected branch", pullHead: approvedHead, protection: `{"required_status_checks":null,"enforce_admins":{"enabled":false}}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
				response.Header().Set("Content-Type", "application/json")
				switch request.URL.Path {
				case "/repos/comisai/fixture/pulls/31":
					_, _ = response.Write([]byte(`{"number":31,"state":"open","merged":false,"merge_commit_sha":null,"html_url":"https://example.com/pull/31","head":{"sha":"` + test.pullHead + `","ref":"devcrew/task-merge"},"base":{"ref":"main"}}`))
				case "/repos/comisai/fixture/commits/" + approvedHead + "/check-runs":
					_, _ = response.Write([]byte(`{"total_count":1,"check_runs":[{"id":31,"name":"ci/unit","status":"completed","conclusion":"success","started_at":"2026-08-20T10:00:00Z"}]}`))
				case "/repos/comisai/fixture/branches/main/protection":
					_, _ = response.Write([]byte(test.protection))
				default:
					http.NotFound(response, request)
				}
			}))
			t.Cleanup(server.Close)
			events := make([]string, 0, 1)
			configuration := validGitHubConfig(server)
			configuration.MergeCredentials = recordingCredentialSource{
				events:     &events,
				credential: Credential{Kind: CredentialMerge, Secret: "merge-token", Scopes: []CredentialScope{ScopePullRequestsWrite}},
			}
			configuration.MergeMethod = MergeSquash
			adapter, err := NewGitHubAdapter(configuration)
			if err != nil {
				t.Fatal(err)
			}
			_, err = adapter.MergePullRequest(context.Background(), PullRequestMergeRequest{
				OperationID: "merge-task-0001", Branch: "devcrew/task-merge", HeadRevision: approvedHead,
				PullRequestID: "github-pr-31", Method: MergeSquash, RequiredChecks: []string{"ci/unit"},
				AuthorityExpiresAt: time.Date(2026, time.August, 20, 12, 5, 0, 0, time.UTC),
			})
			if err == nil || errors.Is(err, ErrPullRequestTruthUnavailable) {
				t.Fatalf("MergePullRequest() error = %v, want permanent refusal", err)
			}
			if len(events) != 0 {
				t.Fatalf("merge credential resolved before fresh truth: %#v", events)
			}
		})
	}
}

func TestGitHubAdapter_PreservesAlreadyMergedMethodAsUnknownWithoutMutation(t *testing.T) {
	head := strings.Repeat("e", 40)
	mergeCommit := strings.Repeat("f", 40)
	mergeCalls := 0
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("Content-Type", "application/json")
		if request.Method == http.MethodGet && request.URL.Path == "/repos/comisai/fixture/pulls/31" {
			_, _ = response.Write([]byte(`{"number":31,"state":"closed","merged":true,"merge_commit_sha":"` + mergeCommit + `","html_url":"https://example.com/pull/31","head":{"sha":"` + head + `","ref":"devcrew/task-merge"},"base":{"ref":"main"}}`))
			return
		}
		if request.Method == http.MethodPut {
			mergeCalls++
		}
		http.NotFound(response, request)
	}))
	t.Cleanup(server.Close)
	configuration := validGitHubConfig(server)
	configuration.MergeCredentials = failingCredentialSource{}
	configuration.MergeMethod = MergeRebase
	adapter, err := NewGitHubAdapter(configuration)
	if err != nil {
		t.Fatal(err)
	}
	receipt, err := adapter.MergePullRequest(context.Background(), PullRequestMergeRequest{
		OperationID: "merge-task-0001", Branch: "devcrew/task-merge", HeadRevision: head,
		PullRequestID: "github-pr-31", Method: MergeRebase, RequiredChecks: []string{"ci/unit"},
		AuthorityExpiresAt: time.Date(2026, time.August, 20, 12, 5, 0, 0, time.UTC),
	})
	if !errors.Is(err, ErrPullRequestMergeOutcomeUnknown) || receipt != (PullRequestMergeReceipt{}) || mergeCalls != 0 {
		t.Fatalf("MergePullRequest(already merged) = %#v, calls=%d, error=%v", receipt, mergeCalls, err)
	}
}

func TestGitHubAdapter_PreservesUncertainMutationMethodAsUnknown(t *testing.T) {
	head := strings.Repeat("3", 40)
	mergeCommit := strings.Repeat("4", 40)
	merged := false
	mergeCalls := 0
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("Content-Type", "application/json")
		switch request.Method + " " + request.URL.Path {
		case "GET /repos/comisai/fixture/pulls/31":
			state, mergedJSON, commit := "open", "false", "null"
			if merged {
				state, mergedJSON, commit = "closed", "true", `"`+mergeCommit+`"`
			}
			_, _ = response.Write([]byte(`{"number":31,"state":"` + state + `","merged":` + mergedJSON +
				`,"merge_commit_sha":` + commit + `,"html_url":"https://example.com/pull/31","head":{"sha":"` + head +
				`","ref":"devcrew/task-merge"},"base":{"ref":"main"}}`))
		case "GET /repos/comisai/fixture/commits/" + head + "/check-runs":
			_, _ = response.Write([]byte(`{"total_count":1,"check_runs":[{"id":31,"name":"ci/unit","status":"completed","conclusion":"success","started_at":"2026-08-20T10:00:00Z"}]}`))
		case "GET /repos/comisai/fixture/branches/main/protection":
			_, _ = response.Write([]byte(`{"required_status_checks":{"strict":true,"contexts":["ci/unit"]},"enforce_admins":{"enabled":true}}`))
		case "PUT /repos/comisai/fixture/pulls/31/merge":
			mergeCalls++
			merged = true
			http.Error(response, `{"message":"Pull Request was already merged"}`, http.StatusConflict)
		default:
			http.NotFound(response, request)
		}
	}))
	t.Cleanup(server.Close)
	configuration := validGitHubConfig(server)
	events := make([]string, 0, 1)
	configuration.MergeCredentials = recordingCredentialSource{
		events: &events,
		credential: Credential{
			Kind: CredentialMerge, Secret: "merge-token", Scopes: []CredentialScope{ScopePullRequestsWrite},
		},
	}
	configuration.MergeMethod = MergeSquash
	adapter, err := NewGitHubAdapter(configuration)
	if err != nil {
		t.Fatal(err)
	}
	receipt, err := adapter.MergePullRequest(context.Background(), PullRequestMergeRequest{
		OperationID: "merge-task-0001", Branch: "devcrew/task-merge", HeadRevision: head,
		PullRequestID: "github-pr-31", Method: MergeSquash, RequiredChecks: []string{"ci/unit"},
		AuthorityExpiresAt: time.Date(2026, time.August, 20, 12, 5, 0, 0, time.UTC),
	})
	if !errors.Is(err, ErrPullRequestMergeOutcomeUnknown) || receipt != (PullRequestMergeReceipt{}) ||
		mergeCalls != 1 || !reflect.DeepEqual(events, []string{"merge-credential-resolved"}) {
		t.Fatalf("MergePullRequest(uncertain method) = %#v, calls=%d, error=%v", receipt, mergeCalls, err)
	}
}

func TestGitHubAdapter_PreservesUnknownMergedMethodAcrossApplicationPort(t *testing.T) {
	head := strings.Repeat("1", 40)
	mergeCommit := strings.Repeat("2", 40)
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("Content-Type", "application/json")
		if request.URL.Path != "/repos/comisai/fixture/pulls/31" {
			http.NotFound(response, request)
			return
		}
		_, _ = response.Write([]byte(`{"number":31,"state":"closed","merged":true,"merge_commit_sha":"` + mergeCommit + `","html_url":"https://example.com/pull/31","head":{"sha":"` + head + `","ref":"devcrew/task-merge"},"base":{"ref":"main"}}`))
	}))
	t.Cleanup(server.Close)
	configuration := validGitHubConfig(server)
	configuration.MergeCredentials = failingCredentialSource{}
	configuration.MergeMethod = MergeCommit
	adapter, err := NewGitHubAdapter(configuration)
	if err != nil {
		t.Fatal(err)
	}
	var port application.ApprovedPullRequestMerger = adapter
	request := application.PullRequestMergeRequest{
		OperationID: "merge-task-0001", RepositoryID: "fixture-repository", PullRequestID: "github-pr-31",
		Branch: "devcrew/task-merge", HeadRevision: head, Method: application.PullRequestMergeRebase,
		RequiredChecks:     []string{"ci/unit"},
		AuthorityExpiresAt: time.Date(2026, time.August, 20, 12, 5, 0, 0, time.UTC),
	}
	reconciled, found, err := port.ReconcileApprovedPullRequest(context.Background(), request)
	if !errors.Is(err, ErrPullRequestMergeOutcomeUnknown) || found ||
		reconciled != (application.PullRequestMergeReceipt{}) {
		t.Fatalf("ReconcileApprovedPullRequest() = %#v, %t, %v", reconciled, found, err)
	}
	receipt, err := port.MergeApprovedPullRequest(context.Background(), request)
	if !errors.Is(err, ErrPullRequestMergeOutcomeUnknown) || receipt != (application.PullRequestMergeReceipt{}) {
		t.Fatalf("MergeApprovedPullRequest() = %#v, %v", receipt, err)
	}
	if _, err := port.MergeApprovedPullRequest(context.Background(), application.PullRequestMergeRequest{
		RepositoryID: "other-repository",
	}); err == nil {
		t.Fatal("MergeApprovedPullRequest(other repository) error = nil")
	}
}

func TestGitHubAdapter_DoesNotInventConfiguredMethodDuringReconciliation(t *testing.T) {
	head := strings.Repeat("1", 40)
	mergeCommit := strings.Repeat("2", 40)
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("Content-Type", "application/json")
		if request.URL.Path != "/repos/comisai/fixture/pulls/31" {
			http.NotFound(response, request)
			return
		}
		_, _ = response.Write([]byte(`{"number":31,"state":"closed","merged":true,"merge_commit_sha":"` + mergeCommit + `","html_url":"https://example.com/pull/31","head":{"sha":"` + head + `","ref":"devcrew/task-merge"},"base":{"ref":"main"}}`))
	}))
	t.Cleanup(server.Close)
	configuration := validGitHubConfig(server)
	configuration.MergeCredentials = failingCredentialSource{}
	configuration.MergeMethod = MergeSquash
	adapter, err := NewGitHubAdapter(configuration)
	if err != nil {
		t.Fatal(err)
	}
	_, found, err := adapter.ReconcileApprovedPullRequest(context.Background(), application.PullRequestMergeRequest{
		OperationID: "merge-task-0001", RepositoryID: "fixture-repository", PullRequestID: "github-pr-31",
		Branch: "devcrew/task-merge", HeadRevision: head, RequiredChecks: []string{"ci/unit"},
		AuthorityExpiresAt: time.Date(2026, time.August, 20, 12, 5, 0, 0, time.UTC),
	})
	if err == nil || found {
		t.Fatalf("ReconcileApprovedPullRequest(without intended method) found=%t, error=%v", found, err)
	}
}

func TestGitHubAdapter_MapsOnlySupportedMergeMethodsOntoApplicationPort(t *testing.T) {
	for _, test := range []struct {
		name   string
		method MergeMethod
		want   application.PullRequestMergeMethod
	}{
		{name: "merge commit", method: MergeCommit, want: application.PullRequestMergeCommit},
		{name: "squash", method: MergeSquash, want: application.PullRequestMergeSquash},
		{name: "rebase", method: MergeRebase, want: application.PullRequestMergeRebase},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, err := applicationMergeMethod(test.method)
			if err != nil || got != test.want {
				t.Fatalf("applicationMergeMethod(%q) = %q, %v", test.method, got, err)
			}
		})
	}
	if _, err := applicationMergeMethod(MergeMethod("unsupported")); err == nil {
		t.Fatal("applicationMergeMethod(unsupported) error = nil")
	}
}

type recordingCredentialSource struct {
	events     *[]string
	credential Credential
}

func (source recordingCredentialSource) Resolve(context.Context) (Credential, error) {
	*source.events = append(*source.events, "merge-credential-resolved")
	return source.credential, nil
}
