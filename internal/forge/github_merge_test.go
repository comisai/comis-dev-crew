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
		PullRequestID: "github-pr-31", RequiredChecks: []string{"ci/unit"},
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
				PullRequestID: "github-pr-31", RequiredChecks: []string{"ci/unit"},
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

func TestGitHubAdapter_ReconcilesAnAlreadyMergedExactHeadWithoutAnotherMutation(t *testing.T) {
	head := strings.Repeat("e", 40)
	mergeCommit := strings.Repeat("f", 40)
	mergeCalls := 0
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("Content-Type", "application/json")
		if request.URL.Path == "/repos/comisai/fixture/pulls/31" {
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
		PullRequestID: "github-pr-31", RequiredChecks: []string{"ci/unit"},
	})
	if err != nil || receipt.MergeCommitRevision != mergeCommit || receipt.Method != MergeRebase || mergeCalls != 0 {
		t.Fatalf("MergePullRequest(replay) = %#v, calls=%d, error=%v", receipt, mergeCalls, err)
	}
}

func TestGitHubAdapter_MapsExactMergedTruthOntoApplicationPort(t *testing.T) {
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
	receipt, err := port.MergeApprovedPullRequest(context.Background(), application.PullRequestMergeRequest{
		OperationID: "merge-task-0001", RepositoryID: "fixture-repository", PullRequestID: "github-pr-31",
		Branch: "devcrew/task-merge", HeadRevision: head, RequiredChecks: []string{"ci/unit"},
	})
	if err != nil || receipt.Method != application.PullRequestMergeCommit ||
		receipt.MergeCommitRevision != mergeCommit {
		t.Fatalf("MergeApprovedPullRequest() = %#v, %v", receipt, err)
	}
	if _, err := port.MergeApprovedPullRequest(context.Background(), application.PullRequestMergeRequest{
		RepositoryID: "other-repository",
	}); err == nil {
		t.Fatal("MergeApprovedPullRequest(other repository) error = nil")
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
