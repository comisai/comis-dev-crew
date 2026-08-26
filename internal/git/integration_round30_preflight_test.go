package git_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/comisai/comis-dev-crew/internal/application"
)

func TestRegistry_IntegrationRejectsTrackedRewriteBeforeTargetCAS(t *testing.T) {
	fixture := newIntegrationFixture(t)
	candidateHead := commitIntegrationFile(
		t, fixture, fixture.candidate.CanonicalPath, "fixture.txt", "candidate rewrite\n",
	)
	targetHead := integrationGitOutput(t, fixture, fixture.target.CanonicalPath, "rev-parse", "HEAD")
	request := fixture.request("integration-existing-rewrite", application.IntegrationMerge, candidateHead, targetHead)

	if _, err := fixture.registry.ApplyIntegrationCandidate(context.Background(), request); err == nil ||
		!errors.Is(err, application.ErrIntegrationMutationNotStarted) {
		t.Fatalf("ApplyIntegrationCandidate(existing rewrite) error = %v", err)
	}
	if head := integrationGitOutput(t, fixture, fixture.target.CanonicalPath, "rev-parse", "HEAD"); head != targetHead {
		t.Fatalf("target head = %q, want %q", head, targetHead)
	}
	contents, err := os.ReadFile(filepath.Join(fixture.target.CanonicalPath, "fixture.txt"))
	if err != nil || string(contents) != "fixture\n" {
		t.Fatalf("target contents = %q, %v", contents, err)
	}
}
