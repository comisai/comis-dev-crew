package git

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/comisai/comis-dev-crew/internal/application"
)

func TestGitEntryPointsRejectMissingAuthority(t *testing.T) {
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := NewRegistry(canceled, RegistryConfig{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("NewRegistry(canceled) error = %v", err)
	}
	if _, err := NewRegistry(nil, RegistryConfig{}); err == nil {
		t.Fatal("NewRegistry(nil context) succeeded")
	}
	var registry *Registry
	if _, err := registry.InspectCandidate(context.Background(), CandidateSnapshotRequest{}); err == nil {
		t.Fatal("InspectCandidate(nil registry) succeeded")
	}
	if _, err := registry.PrepareFixtureCandidate(context.Background(), FixtureCandidateRequest{}); err == nil {
		t.Fatal("PrepareFixtureCandidate(nil registry) succeeded")
	}
	if _, err := registry.PrepareWorktree(context.Background(), PrepareWorktreeRequest{}); err == nil {
		t.Fatal("PrepareWorktree(nil registry) succeeded")
	}
	if err := registry.CleanupWorktree(context.Background(), CleanupWorktreeRequest{}); err == nil {
		t.Fatal("CleanupWorktree(nil registry) succeeded")
	}
	if err := registry.RemoveDeliveredWorktree(context.Background(), DeliveredWorktreeCleanupRequest{}); err == nil {
		t.Fatal("RemoveDeliveredWorktree(nil registry) succeeded")
	}
}

func TestGitEntryPointsHonorCanceledAuthorityFirst(t *testing.T) {
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	registry := &Registry{}
	if _, err := registry.InspectCandidate(canceled, CandidateSnapshotRequest{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("InspectCandidate(canceled) error = %v", err)
	}
	if _, err := registry.PrepareFixtureCandidate(canceled, FixtureCandidateRequest{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("PrepareFixtureCandidate(canceled) error = %v", err)
	}
	if _, err := registry.InspectReconciliationCandidate(canceled, application.ReconciliationWorkspaceRequest{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("InspectReconciliationCandidate(canceled) error = %v", err)
	}
	if _, err := registry.PromoteReconciliationCandidate(canceled, application.ReconciliationWorkspaceRequest{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("PromoteReconciliationCandidate(canceled) error = %v", err)
	}
	if err := registry.CleanupWorktree(canceled, CleanupWorktreeRequest{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("CleanupWorktree(canceled) error = %v", err)
	}
	if err := registry.RemoveDeliveredWorktree(canceled, DeliveredWorktreeCleanupRequest{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("RemoveDeliveredWorktree(canceled) error = %v", err)
	}
}

func TestGitEntryPointsRejectInvalidRequestIdentity(t *testing.T) {
	registry := &Registry{}
	if _, err := registry.InspectCandidate(context.Background(), CandidateSnapshotRequest{}); err == nil {
		t.Fatal("InspectCandidate accepted empty identity")
	}
	if _, err := registry.PrepareFixtureCandidate(context.Background(), FixtureCandidateRequest{}); err == nil {
		t.Fatal("PrepareFixtureCandidate accepted empty identity")
	}
	if _, err := registry.InspectReconciliationCandidate(
		context.Background(), application.ReconciliationWorkspaceRequest{},
	); err == nil {
		t.Fatal("InspectReconciliationCandidate accepted empty identity")
	}
	if _, err := registry.PromoteReconciliationCandidate(
		context.Background(), application.ReconciliationWorkspaceRequest{},
	); err == nil {
		t.Fatal("PromoteReconciliationCandidate accepted empty identity")
	}
}

func TestGitAdaptersRejectUnconfiguredRepositoryBoundaries(t *testing.T) {
	var registry *Registry
	if _, err := registry.PrepareWorkspace(context.Background(), application.WorkspacePreparationRequest{}); err == nil {
		t.Fatal("PrepareWorkspace(nil registry) succeeded")
	}
	if _, err := commonDirectoryIdentity(filepath.Join(t.TempDir(), "missing")); err == nil {
		t.Fatal("commonDirectoryIdentity(missing) succeeded")
	}
	registry = &Registry{repositories: make(map[string]Repository)}
	request := CandidateSnapshotRequest{
		TaskHandle: "task-git-boundary", RepositoryID: "repository-boundary", WorktreePath: "/worktrees/task-git-boundary",
	}
	if _, err := registry.InspectCandidate(context.Background(), request); err == nil {
		t.Fatal("InspectCandidate accepted an unconfigured repository")
	}
	registry.repositories[request.RepositoryID] = Repository{
		ID: request.RepositoryID, WorktreeRoot: "/different-worktrees",
	}
	if _, err := registry.InspectCandidate(context.Background(), request); err == nil {
		t.Fatal("InspectCandidate accepted a worktree outside the task root")
	}
	registry.repositories = make(map[string]Repository)
	revision := strings.Repeat("a", 40)
	if _, err := registry.PrepareWorktree(context.Background(), PrepareWorktreeRequest{
		OperationID: "operation-git-boundary", TaskHandle: request.TaskHandle,
		RepositoryID: request.RepositoryID, BaseRevision: revision,
	}); err == nil {
		t.Fatal("PrepareWorktree accepted an unconfigured repository")
	}
	if _, err := registry.PrepareFixtureCandidate(context.Background(), FixtureCandidateRequest{
		TaskHandle: request.TaskHandle, RepositoryID: request.RepositoryID,
		BaseRevision: revision, WorktreePath: request.WorktreePath, ArtifactRelativePath: "report.md",
	}); err == nil {
		t.Fatal("PrepareFixtureCandidate accepted an unconfigured repository")
	}
	if err := registry.CleanupWorktree(context.Background(), CleanupWorktreeRequest{
		OperationID: "operation-git-boundary", TaskHandle: request.TaskHandle,
		RepositoryID: request.RepositoryID, BaseRevision: revision,
	}); err == nil {
		t.Fatal("CleanupWorktree accepted an unconfigured repository")
	}
}

func TestGitMachineReadersRejectAmbiguousSuccessfulOutput(t *testing.T) {
	if _, err := runGit(context.Background(), "/bin/sh", "-c", `printf '\n'`); err == nil {
		t.Fatal("runGit accepted an empty successful result")
	}
	entries, err := decodeWorktreeList([]byte("worktree /worktrees/task-boundary\x00HEAD " + strings.Repeat("a", 40)))
	if err != nil || len(entries) != 1 || entries[0].path != "/worktrees/task-boundary" {
		t.Fatalf("decodeWorktreeList(unterminated record) = %#v, %v", entries, err)
	}
	executable := filepath.Join(internalCanonicalTempDir(t), "git-output-fixture")
	if err := os.WriteFile(executable, []byte("#!/bin/sh\nprintf 'unknown\\000'\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	registry := &Registry{gitExecutable: executable}
	if _, err := registry.worktreeEntries(context.Background(), Repository{PrimaryCheckout: internalCanonicalTempDir(t)}); err == nil {
		t.Fatal("worktreeEntries accepted an unknown machine-output field")
	}
}
