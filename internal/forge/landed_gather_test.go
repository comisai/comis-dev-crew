package forge

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/comisai/comis-dev-crew/internal/application"
)

func landedAdapter(t *testing.T, handler http.HandlerFunc) (*GitHubAdapter, func()) {
	t.Helper()
	server := httptest.NewServer(handler)
	adapter, err := NewGitHubAdapter(GitHubConfig{
		APIBaseURL: server.URL, Owner: "comisai", Repository: "fixture",
		RepositoryIdentity: "fixture-repository", BaseBranch: "main",
		HTTPClient: server.Client(), Pusher: &recordingBranchPusher{},
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
	return adapter, server.Close
}

func TestGatherLandedEvidenceFindsAMergedPullRequestByHeadBranch(t *testing.T) {
	head := strings.Repeat("b", 40)
	merge := strings.Repeat("c", 40)
	adapter, closeServer := landedAdapter(t, func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("Content-Type", "application/json")
		switch request.Method + " " + request.URL.Path {
		case "GET /repos/comisai/fixture/pulls":
			// No recorded number was supplied; the lookup is by head branch.
			_, _ = response.Write([]byte(`[{"number":21}]`))
		case "GET /repos/comisai/fixture/pulls/21":
			_, _ = response.Write([]byte(`{"number":21,"state":"closed","merged":true,"merge_commit_sha":"` + merge + `","head":{"sha":"` + head + `","ref":"devcrew/task-fixture"},"base":{"ref":"main"}}`))
		case "GET /repos/comisai/fixture/compare/" + merge + "..." + head:
			_, _ = response.Write([]byte(`{"status":"behind"}`))
		default:
			http.NotFound(response, request)
		}
	})
	defer closeServer()

	truth, err := adapter.GatherLandedEvidence(context.Background(), application.LandedEvidenceRequest{
		RepositoryID: "fixture-repository", Branch: "devcrew/task-fixture", HeadRevision: head,
	})
	if err != nil {
		t.Fatalf("GatherLandedEvidence() error = %v", err)
	}
	if !truth.Available || truth.MergedPullRequest == nil {
		t.Fatalf("truth = %+v", truth)
	}
	if !truth.MergedPullRequest.Merged || !truth.MergedPullRequest.MergeCommitContainsHead {
		t.Fatalf("merged pull request = %+v", truth.MergedPullRequest)
	}
	if proof := ProveLandedFromForge(truth); !proof.Landed {
		t.Fatalf("proof = %+v", proof)
	}
}

func TestGatherLandedEvidenceReportsContainmentInTheDefaultBranch(t *testing.T) {
	head := strings.Repeat("b", 40)
	adapter, closeServer := landedAdapter(t, func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("Content-Type", "application/json")
		switch request.Method + " " + request.URL.Path {
		case "GET /repos/comisai/fixture/pulls":
			_, _ = response.Write([]byte(`[]`))
		case "GET /repos/comisai/fixture/compare/main..." + head:
			// identical or behind both mean the default branch already contains it.
			_, _ = response.Write([]byte(`{"status":"behind"}`))
		default:
			http.NotFound(response, request)
		}
	})
	defer closeServer()

	truth, err := adapter.GatherLandedEvidence(context.Background(), application.LandedEvidenceRequest{
		RepositoryID: "fixture-repository", Branch: "devcrew/task-fixture", HeadRevision: head,
	})
	if err != nil {
		t.Fatalf("GatherLandedEvidence() error = %v", err)
	}
	if !truth.DefaultBranchContainsContent || !truth.DefaultBranchUpToDate {
		t.Fatalf("truth = %+v", truth)
	}
	if proof := ProveLandedFromForge(truth); !proof.Landed {
		t.Fatalf("proof = %+v", proof)
	}
}

func TestGatherLandedEvidenceReportsUnavailableRatherThanNotLanded(t *testing.T) {
	// The forge refused the read. That must arrive as "no answer", never as an
	// answer of no — otherwise a transient outage would look like proof that
	// work was never delivered, and cleanup would remove it.
	adapter, closeServer := landedAdapter(t, func(response http.ResponseWriter, request *http.Request) {
		http.Error(response, "upstream unavailable", http.StatusBadGateway)
	})
	defer closeServer()

	truth, err := adapter.GatherLandedEvidence(context.Background(), application.LandedEvidenceRequest{
		RepositoryID: "fixture-repository", Branch: "devcrew/task-fixture",
		HeadRevision: strings.Repeat("b", 40),
	})
	if err != nil {
		t.Fatalf("GatherLandedEvidence() should not surface a transient as an error: %v", err)
	}
	if truth.Available {
		t.Fatalf("unavailable forge reported as available: %+v", truth)
	}
	if proof := ProveLandedFromForge(truth); proof.Landed {
		t.Fatalf("proof = %+v", proof)
	}
}

func TestGatherLandedEvidenceRefusesAForeignRepository(t *testing.T) {
	adapter, closeServer := landedAdapter(t, func(response http.ResponseWriter, request *http.Request) {
		t.Error("a foreign repository must not reach the network")
	})
	defer closeServer()
	if _, err := adapter.GatherLandedEvidence(context.Background(), application.LandedEvidenceRequest{
		RepositoryID: "other-repository", Branch: "devcrew/task-fixture",
		HeadRevision: strings.Repeat("b", 40),
	}); err == nil {
		t.Fatal("foreign repository accepted")
	}
}

func TestGatherLandedEvidenceIgnoresAnUnmergedPullRequest(t *testing.T) {
	head := strings.Repeat("b", 40)
	adapter, closeServer := landedAdapter(t, func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("Content-Type", "application/json")
		switch request.Method + " " + request.URL.Path {
		case "GET /repos/comisai/fixture/pulls":
			_, _ = response.Write([]byte(`[{"number":21}]`))
		case "GET /repos/comisai/fixture/pulls/21":
			_, _ = response.Write([]byte(`{"number":21,"state":"open","merged":false,"head":{"sha":"` + head + `","ref":"devcrew/task-fixture"},"base":{"ref":"main"}}`))
		case "GET /repos/comisai/fixture/compare/main..." + head:
			_, _ = response.Write([]byte(`{"status":"diverged"}`))
		default:
			http.NotFound(response, request)
		}
	})
	defer closeServer()

	truth, err := adapter.GatherLandedEvidence(context.Background(), application.LandedEvidenceRequest{
		RepositoryID: "fixture-repository", Branch: "devcrew/task-fixture", HeadRevision: head,
	})
	if err != nil {
		t.Fatalf("GatherLandedEvidence() error = %v", err)
	}
	if proof := ProveLandedFromForge(truth); proof.Landed {
		t.Fatalf("open pull request proved landed: %+v", proof)
	}
}
