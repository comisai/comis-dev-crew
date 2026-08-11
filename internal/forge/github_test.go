package forge

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/comisai/comis-dev-crew/internal/domain"
)

func TestGitHubAdapter_UsesSeparateAuthoritiesAndRereadsExactPullRequestTruth(t *testing.T) {
	head := strings.Repeat("b", 40)
	var mu sync.Mutex
	requests := make([]string, 0)
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.Header.Get("Authorization") != "Bearer read-token" {
			t.Errorf("authorization header = %q", request.Header.Get("Authorization"))
		}
		mu.Lock()
		requests = append(requests, request.Method+" "+request.URL.RequestURI())
		mu.Unlock()
		response.Header().Set("Content-Type", "application/json")
		switch request.Method + " " + request.URL.Path {
		case "GET /repos/comisai/fixture/pulls":
			_, _ = response.Write([]byte(`[]`))
		case "POST /repos/comisai/fixture/pulls":
			var body map[string]string
			if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
				t.Errorf("decode pull request body: %v", err)
			}
			if body["head"] != "devcrew/task-fixture" || body["base"] != "main" || body["title"] != "Task fixture" {
				t.Errorf("pull request body = %#v", body)
			}
			_, _ = response.Write([]byte(`{"number":17}`))
		case "GET /repos/comisai/fixture/pulls/17":
			_, _ = response.Write([]byte(`{"number":17,"state":"open","html_url":"https://example.com/comisai/fixture/pull/17","head":{"sha":"` + head + `","ref":"devcrew/task-fixture"},"base":{"ref":"main"}}`))
		case "GET /repos/comisai/fixture/commits/" + head + "/check-runs":
			_, _ = response.Write([]byte(`{"check_runs":[{"name":"ci/unit","status":"completed","conclusion":"success"}]}`))
		default:
			http.NotFound(response, request)
		}
	}))
	defer server.Close()
	pusher := &recordingBranchPusher{}
	adapter, err := NewGitHubAdapter(GitHubConfig{
		APIBaseURL: server.URL, Owner: "comisai", Repository: "fixture", RepositoryIdentity: "fixture-repository",
		BaseBranch: "main", HTTPClient: server.Client(), Pusher: pusher,
		ReadCredentials: staticCredentialSource{credential: Credential{
			Kind: CredentialRead, Secret: "read-token",
			Scopes: []CredentialScope{ScopeContentsRead, ScopePullRequestsRead, ScopeChecksRead},
		}},
		PushCredentials: staticCredentialSource{credential: Credential{
			Kind: CredentialPush, Secret: "push-token", Scopes: []CredentialScope{ScopeContentsWrite},
		}},
	})
	if err != nil {
		t.Fatalf("NewGitHubAdapter() error = %v", err)
	}
	truth, err := adapter.DeliverPullRequest(context.Background(), PullRequestRequest{
		OperationID: "deliver-fixture", WorktreePath: "/approved/worktrees/task-fixture",
		Branch: "devcrew/task-fixture", HeadRevision: head, Title: "Task fixture",
		RequiredChecks: []string{"ci/unit"},
	})
	if err != nil {
		t.Fatalf("DeliverPullRequest() error = %v", err)
	}
	wantEvidence := domain.ForgeEvidence{
		Repository: "fixture-repository", PullRequestID: "github-pr-17", HeadRevision: head,
		CheckConclusions: []domain.ForgeCheckEvidence{{Name: "ci/unit", Conclusion: domain.CheckPassed}},
	}
	if truth.URL != "https://example.com/comisai/fixture/pull/17" || !reflect.DeepEqual(truth.Evidence, wantEvidence) {
		t.Fatalf("DeliverPullRequest() = %#v", truth)
	}
	if pusher.calls != 1 || pusher.credential.Secret != "push-token" || pusher.request.HeadRevision != head {
		t.Fatalf("branch push = %#v, %#v, calls=%d", pusher.credential, pusher.request, pusher.calls)
	}
	wantRequests := []string{
		"GET /repos/comisai/fixture/pulls?head=comisai%3Adevcrew%2Ftask-fixture&state=open",
		"POST /repos/comisai/fixture/pulls",
		"GET /repos/comisai/fixture/pulls/17",
		"GET /repos/comisai/fixture/commits/" + head + "/check-runs",
	}
	mu.Lock()
	defer mu.Unlock()
	if !reflect.DeepEqual(requests, wantRequests) {
		t.Fatalf("GitHub requests = %#v, want %#v", requests, wantRequests)
	}
}

func TestGitHubAdapter_RefusesSharedCredentialsChangedHeadAndUnboundedResponses(t *testing.T) {
	head := strings.Repeat("b", 40)
	changedHead := strings.Repeat("c", 40)
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("Content-Type", "application/json")
		switch {
		case request.URL.Path == "/repos/comisai/fixture/pulls":
			_, _ = response.Write([]byte(`[{"number":17}]`))
		case request.URL.Path == "/repos/comisai/fixture/pulls/17":
			_, _ = response.Write([]byte(`{"number":17,"state":"open","html_url":"https://example.com/pull/17","head":{"sha":"` + changedHead + `","ref":"devcrew/task-fixture"},"base":{"ref":"main"}}`))
		default:
			_, _ = response.Write([]byte(strings.Repeat("x", maximumForgeResponseBytes+1)))
		}
	}))
	defer server.Close()
	configuration := validGitHubConfig(server)
	configuration.ReadCredentials = staticCredentialSource{credential: Credential{
		Kind: CredentialRead, Secret: "shared-token",
		Scopes: []CredentialScope{ScopeContentsRead, ScopePullRequestsRead, ScopeChecksRead},
	}}
	configuration.PushCredentials = staticCredentialSource{credential: Credential{
		Kind: CredentialPush, Secret: "shared-token", Scopes: []CredentialScope{ScopeContentsWrite},
	}}
	adapter, err := NewGitHubAdapter(configuration)
	if err != nil {
		t.Fatalf("NewGitHubAdapter() error = %v", err)
	}
	request := PullRequestRequest{
		OperationID: "deliver-fixture", WorktreePath: "/approved/worktrees/task-fixture",
		Branch: "devcrew/task-fixture", HeadRevision: head, Title: "Task fixture",
		RequiredChecks: []string{"ci/unit"},
	}
	if _, err := adapter.DeliverPullRequest(context.Background(), request); err == nil {
		t.Fatal("DeliverPullRequest(shared credentials) error = nil")
	}
	configuration.PushCredentials = staticCredentialSource{credential: Credential{
		Kind: CredentialPush, Secret: "push-token", Scopes: []CredentialScope{ScopeContentsWrite},
	}}
	adapter, err = NewGitHubAdapter(configuration)
	if err != nil {
		t.Fatalf("NewGitHubAdapter(changed head) error = %v", err)
	}
	if _, err := adapter.DeliverPullRequest(context.Background(), request); err == nil {
		t.Fatal("DeliverPullRequest(changed head) error = nil")
	}
}

type staticCredentialSource struct{ credential Credential }

func (source staticCredentialSource) Resolve(context.Context) (Credential, error) {
	return source.credential, nil
}

type recordingBranchPusher struct {
	calls      int
	credential Credential
	request    BranchPushRequest
}

func (pusher *recordingBranchPusher) Push(_ context.Context, credential Credential, request BranchPushRequest) error {
	pusher.calls++
	pusher.credential = credential
	pusher.request = request
	return nil
}

func validGitHubConfig(server *httptest.Server) GitHubConfig {
	return GitHubConfig{
		APIBaseURL: server.URL, Owner: "comisai", Repository: "fixture", RepositoryIdentity: "fixture-repository",
		BaseBranch: "main", HTTPClient: server.Client(), Pusher: &recordingBranchPusher{},
		ReadCredentials: staticCredentialSource{credential: Credential{
			Kind: CredentialRead, Secret: "read-token",
			Scopes: []CredentialScope{ScopeContentsRead, ScopePullRequestsRead, ScopeChecksRead},
		}},
		PushCredentials: staticCredentialSource{credential: Credential{
			Kind: CredentialPush, Secret: "push-token", Scopes: []CredentialScope{ScopeContentsWrite},
		}},
	}
}
