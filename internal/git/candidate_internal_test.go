package git

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestInspectCandidateDistinguishesInfrastructureFromStructuralFailures(t *testing.T) {
	root := internalCanonicalTempDir(t)
	worktreeRoot := filepath.Join(root, "worktrees")
	worktreePath := filepath.Join(worktreeRoot, "task-candidate-infrastructure")
	if err := os.MkdirAll(worktreePath, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(worktreePath, ".git"), []byte("gitdir: unavailable\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	registry := &Registry{
		gitExecutable: filepath.Join(root, "missing-git"),
		repositories: map[string]Repository{"product-api": {
			ID: "product-api", WorktreeRoot: worktreeRoot,
		}},
	}
	request := CandidateSnapshotRequest{
		TaskHandle: "task-candidate-infrastructure", RepositoryID: "product-api", WorktreePath: worktreePath,
	}
	if _, err := registry.InspectCandidate(context.Background(), request); err == nil ||
		errors.Is(err, ErrCandidateWorktreeUnverified) || !errors.Is(err, errGitInfrastructure) {
		t.Fatalf("InspectCandidate(missing Git) error = %v", err)
	}

	registry.gitExecutable = internalGitExecutable(t)
	if err := os.Chmod(worktreePath, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(worktreePath, 0o700) })
	if _, err := registry.InspectCandidate(context.Background(), request); err == nil ||
		errors.Is(err, ErrCandidateWorktreeUnverified) || !errors.Is(err, errFilesystemInfrastructure) {
		t.Fatalf("InspectCandidate(filesystem unavailable) error = %v", err)
	}
}

func TestInspectCandidatePreservesInfrastructureFailureAfterRootInspection(t *testing.T) {
	root := internalCanonicalTempDir(t)
	worktreeRoot := filepath.Join(root, "worktrees")
	worktreePath := filepath.Join(worktreeRoot, "task-candidate-late-infrastructure")
	if err := os.MkdirAll(worktreePath, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(worktreePath, ".git"), []byte("gitdir: unavailable\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	executable := filepath.Join(root, "one-shot-git")
	if err := os.WriteFile(executable, []byte("#!/bin/sh\nrm -- \"$0\"\nprintf '%s\\n' \"$3\"\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	registry := &Registry{
		gitExecutable: executable,
		repositories: map[string]Repository{"product-api": {
			ID: "product-api", WorktreeRoot: worktreeRoot,
		}},
	}
	request := CandidateSnapshotRequest{
		TaskHandle: "task-candidate-late-infrastructure", RepositoryID: "product-api", WorktreePath: worktreePath,
	}
	if _, err := registry.InspectCandidate(context.Background(), request); err == nil ||
		errors.Is(err, ErrCandidateWorktreeUnverified) || !errors.Is(err, errGitInfrastructure) {
		t.Fatalf("InspectCandidate(late Git infrastructure failure) error = %v", err)
	}
}
