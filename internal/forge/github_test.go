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
			_, _ = response.Write([]byte(`{"check_runs":[{"id":17,"name":"ci/unit","status":"completed","conclusion":"success","started_at":"2026-08-14T19:00:00Z"}]}`))
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

func TestGitHubAdapter_VerifiesRecordedPullRequestWithReadAuthorityOnly(t *testing.T) {
	head := strings.Repeat("d", 40)
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.Header.Get("Authorization") != "Bearer read-token" {
			t.Errorf("authorization header = %q", request.Header.Get("Authorization"))
		}
		response.Header().Set("Content-Type", "application/json")
		switch request.URL.Path {
		case "/repos/comisai/fixture/pulls/23":
			_, _ = response.Write([]byte(`{"number":23,"state":"open","html_url":"https://example.com/comisai/fixture/pull/23","head":{"sha":"` + head + `","ref":"devcrew/task-recorded"},"base":{"ref":"main"}}`))
		case "/repos/comisai/fixture/commits/" + head + "/check-runs":
			_, _ = response.Write([]byte(`{"check_runs":[{"id":23,"name":"ci/unit","status":"completed","conclusion":"success","started_at":"2026-08-14T19:00:00Z"}]}`))
		default:
			http.NotFound(response, request)
		}
	}))
	defer server.Close()
	pusher := &recordingBranchPusher{}
	configuration := validGitHubConfig(server)
	configuration.Pusher = pusher
	configuration.PushCredentials = failingCredentialSource{}
	adapter, err := NewGitHubAdapter(configuration)
	if err != nil {
		t.Fatalf("NewGitHubAdapter() error = %v", err)
	}
	truth, err := adapter.VerifyPullRequest(context.Background(), PullRequestVerificationRequest{
		Branch: "devcrew/task-recorded", HeadRevision: head,
		PullRequestID: "github-pr-23", RequiredChecks: []string{"ci/unit"},
	})
	if err != nil {
		t.Fatalf("VerifyPullRequest() error = %v", err)
	}
	if truth.URL != "https://example.com/comisai/fixture/pull/23" ||
		truth.Evidence.PullRequestID != "github-pr-23" || truth.Evidence.HeadRevision != head ||
		len(truth.Evidence.CheckConclusions) != 1 || truth.Evidence.CheckConclusions[0].Conclusion != domain.CheckPassed {
		t.Fatalf("VerifyPullRequest() = %#v", truth)
	}
	if pusher.calls != 0 {
		t.Fatalf("read-only verification push calls = %d", pusher.calls)
	}
	var throughPort application.PullRequestDeliveryVerifier = adapter
	ported, err := throughPort.VerifyPullRequestDelivery(context.Background(), application.PullRequestDeliveryVerification{
		RepositoryID: "fixture-repository", PullRequestID: "github-pr-23",
		Branch: "devcrew/task-recorded", HeadRevision: head, RequiredChecks: []string{"ci/unit"},
	})
	if err != nil || ported.RepositoryID != "fixture-repository" || ported.PullRequestID != "github-pr-23" ||
		len(ported.Checks) != 1 || ported.Checks[0].Conclusion != domain.CheckPassed {
		t.Fatalf("VerifyPullRequestDelivery() = %#v, %v", ported, err)
	}
	if _, err := throughPort.VerifyPullRequestDelivery(context.Background(), application.PullRequestDeliveryVerification{
		RepositoryID: "different-repository", PullRequestID: "github-pr-23",
		Branch: "devcrew/task-recorded", HeadRevision: head, RequiredChecks: []string{"ci/unit"},
	}); err == nil {
		t.Fatal("VerifyPullRequestDelivery(different repository) error = nil")
	}
}

func TestGitHubAdapter_ClassifiesClosedPullRequestAsStaleDeliveryTruth(t *testing.T) {
	head := strings.Repeat("e", 40)
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("Content-Type", "application/json")
		if request.URL.Path != "/repos/comisai/fixture/pulls/23" {
			http.NotFound(response, request)
			return
		}
		_, _ = response.Write([]byte(`{"number":23,"state":"closed","html_url":"https://example.com/comisai/fixture/pull/23","head":{"sha":"` + head + `","ref":"devcrew/task-recorded"},"base":{"ref":"main"}}`))
	}))
	defer server.Close()
	configuration := validGitHubConfig(server)
	adapter, err := NewGitHubAdapter(configuration)
	if err != nil {
		t.Fatalf("NewGitHubAdapter() error = %v", err)
	}
	_, err = adapter.VerifyPullRequestDelivery(context.Background(), application.PullRequestDeliveryVerification{
		RepositoryID: "fixture-repository", PullRequestID: "github-pr-23",
		Branch: "devcrew/task-recorded", HeadRevision: head, RequiredChecks: []string{"ci/unit"},
	})
	if !errors.Is(err, application.ErrCleanupStaleForgeTruth) {
		t.Fatalf("VerifyPullRequestDelivery(closed pull request) error = %v, want stale forge truth", err)
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
	if _, err := adapter.DeliverPullRequest(context.Background(), request); err == nil || errors.Is(err, ErrPullRequestTruthUnavailable) {
		t.Fatalf("DeliverPullRequest(shared credentials) error = %v, want permanent failure", err)
	}
	configuration.PushCredentials = staticCredentialSource{credential: Credential{
		Kind: CredentialPush, Secret: "push-token", Scopes: []CredentialScope{ScopeContentsWrite},
	}}
	adapter, err = NewGitHubAdapter(configuration)
	if err != nil {
		t.Fatalf("NewGitHubAdapter(changed head) error = %v", err)
	}
	if _, err := adapter.DeliverPullRequest(context.Background(), request); err == nil || errors.Is(err, ErrPullRequestTruthUnavailable) {
		t.Fatalf("DeliverPullRequest(changed head) error = %v, want permanent failure", err)
	}
	var oversized map[string]any
	if err := adapter.requestJSON(context.Background(), "read-token", http.MethodGet, adapter.repositoryPath("oversized"), nil, nil, &oversized); err == nil {
		t.Fatal("requestJSON(oversized) error = nil")
	}
}

func TestGitHubAdapter_ClassifiesOnlyRetryableRemoteFailuresAsUnavailableTruth(t *testing.T) {
	statuses := map[string]int{
		"/timeout": http.StatusRequestTimeout, "/too-early": http.StatusTooEarly,
		"/rate-limit": http.StatusTooManyRequests, "/server": http.StatusServiceUnavailable,
		"/rate-limit-forbidden": http.StatusForbidden, "/forbidden": http.StatusForbidden,
		"/not-found": http.StatusNotFound, "/invalid": http.StatusUnprocessableEntity,
	}
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/rate-limit-forbidden" {
			response.Header().Set("X-RateLimit-Remaining", "0")
		}
		response.WriteHeader(statuses[request.URL.Path])
	}))
	t.Cleanup(server.Close)
	configuration := validGitHubConfig(server)
	adapter, err := NewGitHubAdapter(configuration)
	if err != nil {
		t.Fatalf("NewGitHubAdapter() error = %v", err)
	}
	for _, path := range []string{"/timeout", "/too-early", "/rate-limit", "/server", "/rate-limit-forbidden"} {
		if err := adapter.requestJSON(context.Background(), "read-token", http.MethodGet, path, nil, nil, nil); !errors.Is(err, ErrPullRequestTruthUnavailable) {
			t.Fatalf("requestJSON(%s) error = %v, want unavailable truth", path, err)
		}
	}
	for _, path := range []string{"/forbidden", "/not-found", "/invalid"} {
		if err := adapter.requestJSON(context.Background(), "read-token", http.MethodGet, path, nil, nil, nil); err == nil || errors.Is(err, ErrPullRequestTruthUnavailable) {
			t.Fatalf("requestJSON(%s) error = %v, want permanent failure", path, err)
		}
	}
	adapter.config.HTTPClient = &http.Client{Transport: githubErrorTransport{err: githubTimeoutError{}}}
	if err := adapter.requestJSON(context.Background(), "read-token", http.MethodGet, "/transport", nil, nil, nil); !errors.Is(err, ErrPullRequestTruthUnavailable) {
		t.Fatalf("requestJSON(transport) error = %v, want unavailable truth", err)
	}
	adapter.config.HTTPClient = &http.Client{Transport: githubErrorTransport{err: errors.New("permanent transport failure")}}
	if err := adapter.requestJSON(context.Background(), "read-token", http.MethodGet, "/transport", nil, nil, nil); err == nil || errors.Is(err, ErrPullRequestTruthUnavailable) {
		t.Fatalf("requestJSON(permanent transport) error = %v, want permanent failure", err)
	}
}

func TestGitHubAdapter_RejectsInvalidConfigurationRequestsAndDependencies(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("Content-Type", "application/json")
		_, _ = response.Write([]byte(`[]`))
	}))
	defer server.Close()
	for _, mutate := range []func(*GitHubConfig){
		func(config *GitHubConfig) { config.APIBaseURL = "http://example.com" },
		func(config *GitHubConfig) { config.Owner = "bad owner" },
		func(config *GitHubConfig) { config.RepositoryIdentity = "x" },
		func(config *GitHubConfig) { config.Pusher = nil },
		func(config *GitHubConfig) { config.ReadCredentials = nil },
	} {
		configuration := validGitHubConfig(server)
		mutate(&configuration)
		if _, err := NewGitHubAdapter(configuration); err == nil {
			t.Fatal("NewGitHubAdapter(invalid) error = nil")
		}
	}
	configuration := validGitHubConfig(server)
	adapter, err := NewGitHubAdapter(configuration)
	if err != nil {
		t.Fatalf("NewGitHubAdapter() error = %v", err)
	}
	valid := PullRequestRequest{
		OperationID: "deliver-fixture", WorktreePath: "/approved/worktrees/task-fixture",
		Branch: "devcrew/task-fixture", HeadRevision: strings.Repeat("b", 40), Title: "Task fixture",
		RequiredChecks: []string{"ci/unit"},
	}
	for _, mutate := range []func(*PullRequestRequest){
		func(request *PullRequestRequest) { request.OperationID = "bad operation" },
		func(request *PullRequestRequest) { request.WorktreePath = "relative" },
		func(request *PullRequestRequest) { request.Branch = "main" },
		func(request *PullRequestRequest) { request.HeadRevision = "invalid" },
		func(request *PullRequestRequest) { request.Title = " bad" },
		func(request *PullRequestRequest) { request.RequiredChecks = nil },
		func(request *PullRequestRequest) { request.RequiredChecks = []string{"ci/unit", "ci/unit"} },
	} {
		request := valid
		mutate(&request)
		if _, err := adapter.DeliverPullRequest(context.Background(), request); err == nil {
			t.Fatal("DeliverPullRequest(invalid request) error = nil")
		}
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := adapter.DeliverPullRequest(cancelled, valid); !errors.Is(err, context.Canceled) {
		t.Fatalf("DeliverPullRequest(cancelled) error = %v", err)
	}
	if _, err := (*GitHubAdapter)(nil).DeliverPullRequest(context.Background(), valid); err == nil {
		t.Fatal("DeliverPullRequest(nil adapter) error = nil")
	}
	configuration.ReadCredentials = failingCredentialSource{}
	adapter, _ = NewGitHubAdapter(configuration)
	if _, err := adapter.DeliverPullRequest(context.Background(), valid); err == nil {
		t.Fatal("DeliverPullRequest(read credential failure) error = nil")
	}
	configuration = validGitHubConfig(server)
	configuration.PushCredentials = failingCredentialSource{}
	adapter, _ = NewGitHubAdapter(configuration)
	if _, err := adapter.DeliverPullRequest(context.Background(), valid); err == nil {
		t.Fatal("DeliverPullRequest(push credential failure) error = nil")
	}
	configuration = validGitHubConfig(server)
	configuration.Pusher = failingBranchPusher{}
	adapter, _ = NewGitHubAdapter(configuration)
	if _, err := adapter.DeliverPullRequest(context.Background(), valid); err == nil || errors.Is(err, ErrPullRequestTruthUnavailable) {
		t.Fatalf("DeliverPullRequest(push failure) error = %v, want permanent failure", err)
	}
}

func TestGitHubCheckConclusion_MapsEveryExternalPostureFailClosed(t *testing.T) {
	value := func(value string) *string { return &value }
	for _, test := range []struct {
		status     string
		conclusion *string
		want       domain.CheckConclusion
	}{
		{status: "queued", want: domain.CheckPending},
		{status: "in_progress", want: domain.CheckPending},
		{status: "invented", want: domain.CheckUnknown},
		{status: "completed", want: domain.CheckUnknown},
		{status: "completed", conclusion: value("success"), want: domain.CheckPassed},
		{status: "completed", conclusion: value("neutral"), want: domain.CheckPassed},
		{status: "completed", conclusion: value("failure"), want: domain.CheckFailed},
		{status: "completed", conclusion: value("cancelled"), want: domain.CheckFailed},
		{status: "completed", conclusion: value("invented"), want: domain.CheckUnknown},
	} {
		if got := githubCheckConclusion(test.status, test.conclusion); got != test.want {
			t.Fatalf("githubCheckConclusion(%q, %v) = %q, want %q", test.status, test.conclusion, got, test.want)
		}
	}
}

func TestGitHubAdapter_SelectsNewestCheckRunForRepeatedName(t *testing.T) {
	head := strings.Repeat("f", 40)
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("Content-Type", "application/json")
		if request.URL.Path != "/repos/comisai/fixture/commits/"+head+"/check-runs" {
			http.NotFound(response, request)
			return
		}
		_, _ = response.Write([]byte(`{"check_runs":[` +
			`{"id":12,"name":"ci/unit","status":"in_progress","conclusion":null,"started_at":"2026-08-14T20:05:00Z"},` +
			`{"id":11,"name":"ci/unit","status":"completed","conclusion":"success","started_at":"2026-08-14T20:00:00Z"}` +
			`]}`))
	}))
	defer server.Close()
	configuration := validGitHubConfig(server)
	adapter, err := NewGitHubAdapter(configuration)
	if err != nil {
		t.Fatalf("NewGitHubAdapter() error = %v", err)
	}
	checks, err := adapter.readChecks(context.Background(), "read-token", head, []string{"ci/unit"})
	if err != nil {
		t.Fatalf("readChecks(repeated name) error = %v", err)
	}
	want := []domain.ForgeCheckEvidence{{Name: "ci/unit", Conclusion: domain.CheckPending}}
	if !reflect.DeepEqual(checks, want) {
		t.Fatalf("readChecks(repeated name) = %#v, want %#v", checks, want)
	}
}

func TestGitHubAdapter_TreatsMissingOrMalformedCheckRecencyConservatively(t *testing.T) {
	head := strings.Repeat("e", 40)
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("Content-Type", "application/json")
		if request.URL.Path != "/repos/comisai/fixture/commits/"+head+"/check-runs" {
			http.NotFound(response, request)
			return
		}
		_, _ = response.Write([]byte(`{"check_runs":[` +
			`{"id":13,"name":"ci/unit","status":"queued","conclusion":null,"started_at":null},` +
			`{"id":12,"name":"ci/unit","status":"completed","conclusion":"success","started_at":"2026-08-14T20:00:00Z"},` +
			`{"id":14,"name":"ci/lint","status":"completed","conclusion":"success","started_at":null},` +
			`{"id":15,"name":"ci/security","status":"completed","conclusion":"success","started_at":"invalid"},` +
			`{"id":16,"name":"ci/number","status":"completed","conclusion":"success","started_at":42},` +
			`{"id":17,"name":"ci/boolean","status":"completed","conclusion":"success","started_at":false},` +
			`{"id":18,"name":"ci/object","status":"completed","conclusion":"success","started_at":{}},` +
			`{"id":19,"name":"ci/array","status":"completed","conclusion":"success","started_at":[]}` +
			`]}`))
	}))
	defer server.Close()
	adapter, err := NewGitHubAdapter(validGitHubConfig(server))
	if err != nil {
		t.Fatalf("NewGitHubAdapter() error = %v", err)
	}
	checks, err := adapter.readChecks(context.Background(), "read-token", head, []string{
		"ci/unit", "ci/lint", "ci/security", "ci/number", "ci/boolean", "ci/object", "ci/array",
	})
	if err != nil {
		t.Fatalf("readChecks(nullable recency) error = %v", err)
	}
	want := []domain.ForgeCheckEvidence{
		{Name: "ci/unit", Conclusion: domain.CheckUnknown},
		{Name: "ci/lint", Conclusion: domain.CheckUnknown},
		{Name: "ci/security", Conclusion: domain.CheckUnknown},
		{Name: "ci/number", Conclusion: domain.CheckUnknown},
		{Name: "ci/boolean", Conclusion: domain.CheckUnknown},
		{Name: "ci/object", Conclusion: domain.CheckUnknown},
		{Name: "ci/array", Conclusion: domain.CheckUnknown},
	}
	if !reflect.DeepEqual(checks, want) {
		t.Fatalf("readChecks(nullable recency) = %#v, want %#v", checks, want)
	}
}

type staticCredentialSource struct{ credential Credential }

func (source staticCredentialSource) Resolve(context.Context) (Credential, error) {
	return source.credential, nil
}

type failingCredentialSource struct{}

func (failingCredentialSource) Resolve(context.Context) (Credential, error) {
	return Credential{}, errors.New("credential unavailable")
}

type recordingBranchPusher struct {
	calls      int
	credential Credential
	request    BranchPushRequest
}

type failingBranchPusher struct{}

func (failingBranchPusher) Push(context.Context, Credential, BranchPushRequest) error {
	return errors.New("push failed")
}

type githubErrorTransport struct{ err error }

func (transport githubErrorTransport) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, transport.err
}

type githubTimeoutError struct{}

func (githubTimeoutError) Error() string   { return "request timed out" }
func (githubTimeoutError) Timeout() bool   { return true }
func (githubTimeoutError) Temporary() bool { return true }

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
