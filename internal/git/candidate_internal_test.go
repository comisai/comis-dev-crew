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

func TestInspectCandidatePreservesLaunchedGitIOFailure(t *testing.T) {
	root := internalCanonicalTempDir(t)
	worktreeRoot := filepath.Join(root, "worktrees")
	worktreePath := filepath.Join(worktreeRoot, "task-candidate-child-io")
	if err := os.MkdirAll(worktreePath, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(worktreePath, ".git"), []byte("gitdir: unavailable\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	executable := filepath.Join(root, "failing-git")
	if err := os.WriteFile(executable, []byte("#!/bin/sh\nprintf '%s\\n' 'fatal: unable to read index: Input/output error' >&2\nexit 128\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	registry := &Registry{
		gitExecutable: executable,
		repositories: map[string]Repository{"product-api": {
			ID: "product-api", WorktreeRoot: worktreeRoot,
		}},
	}
	request := CandidateSnapshotRequest{
		TaskHandle: "task-candidate-child-io", RepositoryID: "product-api", WorktreePath: worktreePath,
	}
	if _, err := registry.InspectCandidate(context.Background(), request); err == nil ||
		errors.Is(err, ErrCandidateWorktreeUnverified) || !errors.Is(err, errGitInfrastructure) {
		t.Fatalf("InspectCandidate(child I/O failure) error = %v", err)
	}
}

func TestInspectCandidatePreservesGitLoaderFailure(t *testing.T) {
	root := internalCanonicalTempDir(t)
	worktreeRoot := filepath.Join(root, "worktrees")
	worktreePath := filepath.Join(worktreeRoot, "task-candidate-loader")
	if err := os.MkdirAll(worktreePath, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(worktreePath, ".git"), []byte("gitdir: unavailable\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	executable := filepath.Join(root, "loader-failing-git")
	if err := os.WriteFile(
		executable,
		[]byte("#!/bin/sh\nprintf '%s\\n' 'git: error while loading shared libraries: libz.so: cannot open shared object file' >&2\nexit 127\n"),
		0o700,
	); err != nil {
		t.Fatal(err)
	}
	registry := &Registry{
		gitExecutable: executable,
		repositories: map[string]Repository{"product-api": {
			ID: "product-api", WorktreeRoot: worktreeRoot,
		}},
	}
	request := CandidateSnapshotRequest{
		TaskHandle: "task-candidate-loader", RepositoryID: "product-api", WorktreePath: worktreePath,
	}
	if _, err := registry.InspectCandidate(context.Background(), request); err == nil ||
		errors.Is(err, ErrCandidateWorktreeUnverified) || !errors.Is(err, errGitInfrastructure) {
		t.Fatalf("InspectCandidate(loader failure) error = %v", err)
	}
}

func TestInspectCandidateTreatsCorruptIndexAsUnverifiedWorktree(t *testing.T) {
	ctx := context.Background()
	root := internalCanonicalTempDir(t)
	executable := internalGitExecutable(t)
	primary := filepath.Join(root, "primary")
	worktreeRoot := filepath.Join(root, "worktrees")
	if err := os.Mkdir(worktreeRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, arguments := range [][]string{
		{"init", "--initial-branch=main", primary},
		{"--no-optional-locks", "-C", primary, "add", "fixture.txt"},
		{"--no-optional-locks", "-C", primary, "-c", "user.name=DevCrew Fixture", "-c", "user.email=fixture@example.invalid", "commit", "-m", "fixture"},
	} {
		if arguments[0] == "--no-optional-locks" && arguments[3] == "add" {
			if err := os.WriteFile(filepath.Join(primary, "fixture.txt"), []byte("fixture\n"), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := runGitBytes(ctx, executable, arguments...); err != nil {
			t.Fatalf("Git fixture command %q: %v", arguments, err)
		}
	}
	registry, err := NewRegistry(ctx, RegistryConfig{
		GitExecutable: executable, ApprovedRoots: []string{root},
		Repositories: []RepositoryConfig{{
			ID: "product-api", PrimaryCheckout: primary, WorktreeRoot: worktreeRoot, DefaultBranch: "main",
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	base, err := runGit(ctx, executable, "--no-optional-locks", "-C", primary, "rev-parse", "--verify", "HEAD^{commit}")
	if err != nil {
		t.Fatal(err)
	}
	prepared, err := registry.PrepareWorktree(ctx, PrepareWorktreeRequest{
		OperationID: "operation-corrupt-index", TaskHandle: "task-corrupt-index",
		RepositoryID: "product-api", BaseRevision: base,
	})
	if err != nil {
		t.Fatal(err)
	}
	indexPath, err := runGit(ctx, executable, "--no-optional-locks", "-C", prepared.CanonicalPath, "rev-parse", "--git-path", "index")
	if err != nil {
		t.Fatal(err)
	}
	if !filepath.IsAbs(indexPath) {
		indexPath = filepath.Join(prepared.CanonicalPath, indexPath)
	}
	if err := os.WriteFile(indexPath, []byte("bad"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err = registry.InspectCandidate(ctx, CandidateSnapshotRequest{
		TaskHandle: prepared.TaskHandle, RepositoryID: prepared.RepositoryID, WorktreePath: prepared.CanonicalPath,
	})
	if err == nil || !errors.Is(err, ErrCandidateWorktreeUnverified) || errors.Is(err, errGitInfrastructure) {
		t.Fatalf("InspectCandidate(corrupt index) error = %v", err)
	}
}
