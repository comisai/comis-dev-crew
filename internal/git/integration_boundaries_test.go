package git_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/comisai/comis-dev-crew/internal/application"
	devgit "github.com/comisai/comis-dev-crew/internal/git"
)

func TestRegistry_IntegrationBoundaryRejectsUnavailableCancelledAndInvalidRequests(t *testing.T) {
	fixture := newIntegrationFixture(t)
	candidateHead := commitIntegrationFile(t, fixture, fixture.candidate.CanonicalPath, "component.txt", "component\n")
	targetHead := integrationGitOutput(t, fixture, fixture.target.CanonicalPath, "rev-parse", "HEAD")
	valid := fixture.request("integration-boundary-0001", application.IntegrationMerge, candidateHead, targetHead)
	if _, err := (*devgit.Registry)(nil).ApplyIntegrationCandidate(context.Background(), valid); err == nil {
		t.Fatal("ApplyIntegrationCandidate(nil registry) error = nil")
	}
	//lint:ignore SA1012 The public boundary must reject nil before Git inspection.
	if _, err := fixture.registry.ApplyIntegrationCandidate(nil, valid); err == nil {
		t.Fatal("ApplyIntegrationCandidate(nil context) error = nil")
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := fixture.registry.ApplyIntegrationCandidate(cancelled, valid); !errors.Is(err, context.Canceled) {
		t.Fatalf("ApplyIntegrationCandidate(cancelled) error = %v", err)
	}
	invalid := valid
	invalid.Strategy = "shell_fragment"
	if _, err := fixture.registry.ApplyIntegrationCandidate(context.Background(), invalid); err == nil {
		t.Fatal("ApplyIntegrationCandidate(unreviewed strategy) error = nil")
	}
	invalid = valid
	invalid.Target.TaskHandle = invalid.Candidate.TaskHandle
	if _, err := fixture.registry.ApplyIntegrationCandidate(context.Background(), invalid); err == nil {
		t.Fatal("ApplyIntegrationCandidate(shared task) error = nil")
	}
	invalid = valid
	invalid.Target.RepositoryID = "missing-repository"
	invalid.Candidate.RepositoryID = "missing-repository"
	if _, err := fixture.registry.ApplyIntegrationCandidate(context.Background(), invalid); err == nil {
		t.Fatal("ApplyIntegrationCandidate(missing repository) error = nil")
	}
	invalid = valid
	invalid.Candidate.BaseRevision = strings.Repeat("f", 40)
	if _, err := fixture.registry.ApplyIntegrationCandidate(context.Background(), invalid); err == nil {
		t.Fatal("ApplyIntegrationCandidate(missing base) error = nil")
	}
}

func TestRegistry_IntegrationBoundaryRefusesDirtyTargetAndUnattributableFailure(t *testing.T) {
	fixture := newIntegrationFixture(t)
	candidateHead := commitIntegrationFile(t, fixture, fixture.candidate.CanonicalPath, "component.txt", "component\n")
	targetHead := integrationGitOutput(t, fixture, fixture.target.CanonicalPath, "rev-parse", "HEAD")
	request := fixture.request("integration-dirty-target", application.IntegrationCherryPick, candidateHead, targetHead)
	dirtyPath := filepath.Join(fixture.target.CanonicalPath, "dirty.txt")
	if err := os.WriteFile(dirtyPath, []byte("dirty\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.registry.ApplyIntegrationCandidate(context.Background(), request); err == nil {
		t.Fatal("ApplyIntegrationCandidate(dirty target) error = nil")
	}
	if err := os.Remove(dirtyPath); err != nil {
		t.Fatal(err)
	}
	first, err := fixture.registry.ApplyIntegrationCandidate(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	second := fixture.request("integration-empty-cherry-pick", application.IntegrationCherryPick, candidateHead, first.ResultingHead)
	if _, err := fixture.registry.ApplyIntegrationCandidate(context.Background(), second); err == nil {
		t.Fatal("ApplyIntegrationCandidate(empty cherry-pick) error = nil")
	}
}

func TestRegistry_IntegrationReceiptRefusesAChangedTarget(t *testing.T) {
	fixture := newIntegrationFixture(t)
	candidateHead := commitIntegrationFile(t, fixture, fixture.candidate.CanonicalPath, "component.txt", "component\n")
	targetHead := integrationGitOutput(t, fixture, fixture.target.CanonicalPath, "rev-parse", "HEAD")
	request := fixture.request("integration-receipt-target-change", application.IntegrationMerge, candidateHead, targetHead)
	if _, err := fixture.registry.ApplyIntegrationCandidate(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	commitIntegrationFile(t, fixture, fixture.target.CanonicalPath, "later.txt", "later\n")
	if _, err := fixture.registry.ApplyIntegrationCandidate(context.Background(), request); err == nil {
		t.Fatal("ApplyIntegrationCandidate(changed receipt target) error = nil")
	}
}
