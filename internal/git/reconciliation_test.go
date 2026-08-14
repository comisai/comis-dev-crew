package git_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/comisai/comis-dev-crew/internal/application"
	devgit "github.com/comisai/comis-dev-crew/internal/git"
)

func TestRegistry_InspectsOnlyExactOperationBoundCleanCandidate(t *testing.T) {
	fixture := newRepositoryFixture(t, "product-api")
	registry := newLifecycleRegistry(t, fixture)
	request := lifecycleRequest(t, fixture, "prepare-reconcile-candidate", "task-reconcile-candidate")
	prepared, err := registry.PrepareWorktree(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(prepared.CanonicalPath, "candidate.txt"), []byte("candidate\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runGit(t, fixture.gitExecutable, "--no-optional-locks", "-C", prepared.CanonicalPath, "add", "candidate.txt")
	runGit(t, fixture.gitExecutable, "--no-optional-locks", "-C", prepared.CanonicalPath,
		"-c", "user.name=DevCrew Fixture", "-c", "user.email=fixture@example.invalid",
		"commit", "-m", "candidate")

	var inspector application.ReconciliationWorkspaceInspector = registry
	reconciliationRequest := application.ReconciliationWorkspaceRequest{
		PreparationOperationID: request.OperationID, TaskHandle: request.TaskHandle,
		RepositoryID: request.RepositoryID, WorktreePath: prepared.CanonicalPath,
		BaseRevision: request.BaseRevision,
	}
	snapshot, err := inspector.InspectReconciliationCandidate(context.Background(), reconciliationRequest)
	if err != nil {
		t.Fatalf("InspectReconciliationCandidate() error = %v", err)
	}
	if snapshot.TaskHandle != request.TaskHandle || snapshot.RepositoryID != request.RepositoryID ||
		snapshot.WorktreePath != prepared.CanonicalPath || snapshot.Branch != prepared.Branch ||
		snapshot.HeadRevision == request.BaseRevision || snapshot.Cleanliness != application.WorkspaceClean {
		t.Fatalf("InspectReconciliationCandidate() = %#v", snapshot)
	}

	t.Run("dirty worktree", func(t *testing.T) {
		if err := os.WriteFile(filepath.Join(prepared.CanonicalPath, "dirty.txt"), []byte("dirty\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := inspector.InspectReconciliationCandidate(context.Background(), reconciliationRequest); err == nil {
			t.Fatal("InspectReconciliationCandidate(dirty) error = nil")
		}
		if err := os.Remove(filepath.Join(prepared.CanonicalPath, "dirty.txt")); err != nil {
			t.Fatal(err)
		}
	})

	for _, test := range []struct {
		name   string
		mutate func(*application.ReconciliationWorkspaceRequest)
	}{
		{name: "preparation operation differs", mutate: func(value *application.ReconciliationWorkspaceRequest) {
			value.PreparationOperationID = "prepare-reconcile-other"
		}},
		{name: "task differs", mutate: func(value *application.ReconciliationWorkspaceRequest) {
			value.TaskHandle = "task-reconcile-other"
		}},
		{name: "repository differs", mutate: func(value *application.ReconciliationWorkspaceRequest) {
			value.RepositoryID = "other-product"
		}},
		{name: "worktree differs", mutate: func(value *application.ReconciliationWorkspaceRequest) {
			value.WorktreePath += "-other"
		}},
		{name: "base differs", mutate: func(value *application.ReconciliationWorkspaceRequest) {
			value.BaseRevision = snapshot.HeadRevision
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			altered := reconciliationRequest
			test.mutate(&altered)
			if _, err := inspector.InspectReconciliationCandidate(context.Background(), altered); err == nil {
				t.Fatal("InspectReconciliationCandidate(altered authority) error = nil")
			}
		})
	}

	baseRequest := lifecycleRequest(t, fixture, "prepare-reconcile-base", "task-reconcile-base")
	basePrepared, err := registry.PrepareWorktree(context.Background(), baseRequest)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := inspector.InspectReconciliationCandidate(context.Background(), application.ReconciliationWorkspaceRequest{
		PreparationOperationID: baseRequest.OperationID, TaskHandle: baseRequest.TaskHandle,
		RepositoryID: baseRequest.RepositoryID, WorktreePath: basePrepared.CanonicalPath,
		BaseRevision: baseRequest.BaseRevision,
	}); err == nil {
		t.Fatal("InspectReconciliationCandidate(base head) error = nil")
	}
}

func TestRegistry_RefusesUnavailableOrDivergentReconciliationCandidate(t *testing.T) {
	fixture := newRepositoryFixture(t, "product-reconciliation-refusal")
	registry := newLifecycleRegistry(t, fixture)
	request := lifecycleRequest(t, fixture, "prepare-reconciliation-refusal", "task-reconciliation-refusal")
	prepared, err := registry.PrepareWorktree(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(prepared.CanonicalPath, "candidate.txt"), []byte("candidate\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runGit(t, fixture.gitExecutable, "--no-optional-locks", "-C", prepared.CanonicalPath, "add", "candidate.txt")
	runGit(t, fixture.gitExecutable, "--no-optional-locks", "-C", prepared.CanonicalPath,
		"-c", "user.name=DevCrew Fixture", "-c", "user.email=fixture@example.invalid",
		"commit", "-m", "candidate")
	base := application.ReconciliationWorkspaceRequest{
		PreparationOperationID: request.OperationID, TaskHandle: request.TaskHandle,
		RepositoryID: request.RepositoryID, WorktreePath: prepared.CanonicalPath,
		BaseRevision: request.BaseRevision,
	}
	tests := []struct {
		name   string
		mutate func(*application.ReconciliationWorkspaceRequest)
	}{
		{name: "repository is unavailable", mutate: func(value *application.ReconciliationWorkspaceRequest) {
			value.RepositoryID = "product-missing"
		}},
		{name: "request identity is invalid", mutate: func(value *application.ReconciliationWorkspaceRequest) {
			value.BaseRevision = "not-a-revision"
		}},
		{name: "worktree is unavailable", mutate: func(value *application.ReconciliationWorkspaceRequest) {
			value.WorktreePath = filepath.Join(filepath.Dir(prepared.CanonicalPath), "missing-worktree")
		}},
		{name: "pinned base is unavailable", mutate: func(value *application.ReconciliationWorkspaceRequest) {
			value.BaseRevision = strings.Repeat("f", 40)
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			altered := base
			test.mutate(&altered)
			if _, err := registry.InspectReconciliationCandidate(context.Background(), altered); err == nil {
				t.Fatal("InspectReconciliationCandidate() error = nil")
			}
		})
	}
	if _, err := (*devgit.Registry)(nil).InspectReconciliationCandidate(context.Background(), base); err == nil {
		t.Fatal("InspectReconciliationCandidate(nil registry) error = nil")
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := registry.InspectReconciliationCandidate(cancelled, base); err == nil {
		t.Fatal("InspectReconciliationCandidate(cancelled) error = nil")
	}
}
