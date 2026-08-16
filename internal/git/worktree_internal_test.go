package git

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestWorktreeInventoryDecoder_AcceptsMachineRecordsAndRejectsAmbiguity(t *testing.T) {
	encoded := []byte("worktree /repo\x00HEAD " + strings.Repeat("a", 40) +
		"\x00branch refs/heads/main\x00\x00worktree /task\x00HEAD " + strings.Repeat("b", 40) +
		"\x00branch refs/heads/devcrew/task\x00locked fixture\x00prunable stale\x00\x00")
	entries, err := decodeWorktreeList(encoded)
	if err != nil {
		t.Fatalf("decodeWorktreeList(valid) error = %v", err)
	}
	if len(entries) != 2 || entries[0].path != "/repo" || entries[0].branch != "main" ||
		entries[1].path != "/task" || !entries[1].locked || !entries[1].prunable {
		t.Fatalf("decodeWorktreeList(valid) = %#v", entries)
	}
	if entry, found := findWorktreeEntry(entries, "/task"); !found || entry.branch != "devcrew/task" {
		t.Fatalf("findWorktreeEntry(task) = %#v, %t", entry, found)
	}
	if _, found := findWorktreeEntry(entries, "/missing"); found {
		t.Fatal("findWorktreeEntry(missing) found an entry")
	}

	tests := [][]byte{
		[]byte("worktree\x00"),
		[]byte("worktree /one\x00worktree /two\x00HEAD " + strings.Repeat("a", 40) + "\x00"),
		[]byte("worktree /one\x00unknown value\x00HEAD " + strings.Repeat("a", 40) + "\x00"),
		[]byte("worktree /one\x00branch refs/heads/main\x00"),
	}
	for index, invalid := range tests {
		if _, err := decodeWorktreeList(invalid); err == nil {
			t.Fatalf("decodeWorktreeList(invalid %d) error = nil", index)
		}
	}
}

func TestPreparedWorktreeBoundaryHelpers_AreBoundedAndFailClosed(t *testing.T) {
	repository := Repository{PrimaryCheckout: "/approved/primary", WorktreeRoot: "/approved/worktrees"}
	target := "/approved/worktrees/task-valid"
	if err := validatePreparedTarget(repository, target, nil); err != nil {
		t.Fatalf("validatePreparedTarget(valid) error = %v", err)
	}
	for _, test := range []struct {
		target string
		live   []string
	}{
		{target: repository.PrimaryCheckout},
		{target: "/approved/worktrees-escape/task"},
		{target: "/approved/worktrees/nested/task"},
		{target: target + "\n"},
		{target: target, live: []string{"relative"}},
		{target: target, live: []string{target}},
	} {
		if err := validatePreparedTarget(repository, test.target, test.live); err == nil {
			t.Fatalf("validatePreparedTarget(%q, %q) error = nil", test.target, test.live)
		}
	}

	branch, suffix := preparedBranch("product-api", strings.Repeat("a", 47)+"-suffix", "operation-0001")
	if len(suffix) != 24 || len(branch) > 81 || strings.Contains(branch, "--"+suffix) {
		t.Fatalf("preparedBranch() = %q, %q", branch, suffix)
	}
}

func TestWorktreeLifecycleBoundaries_RejectUnavailableCancelledAndInvalidRequests(t *testing.T) {
	valid := PrepareWorktreeRequest{
		OperationID: "operation-0001", TaskHandle: "task-handle-0001", RepositoryID: "product-api",
		BaseRevision: strings.Repeat("a", 40),
	}
	var unavailable *Registry
	if _, err := unavailable.PrepareWorktree(context.Background(), valid); err == nil {
		t.Fatal("PrepareWorktree(unavailable) error = nil")
	}
	if err := unavailable.CleanupWorktree(context.Background(), CleanupWorktreeRequest(valid)); err == nil {
		t.Fatal("CleanupWorktree(unavailable) error = nil")
	}
	registry := &Registry{repositories: map[string]Repository{}}
	//lint:ignore SA1012 This boundary test proves nil cannot reach repository or Git inspection.
	if _, err := registry.PrepareWorktree(nil, valid); err == nil {
		t.Fatal("PrepareWorktree(nil context) error = nil")
	}
	//lint:ignore SA1012 This boundary test proves nil cannot reach repository or Git inspection.
	if err := registry.CleanupWorktree(nil, CleanupWorktreeRequest(valid)); err == nil {
		t.Fatal("CleanupWorktree(nil context) error = nil")
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := registry.PrepareWorktree(cancelled, valid); !errors.Is(err, context.Canceled) {
		t.Fatalf("PrepareWorktree(cancelled) error = %v", err)
	}
	if err := registry.CleanupWorktree(cancelled, CleanupWorktreeRequest(valid)); !errors.Is(err, context.Canceled) {
		t.Fatalf("CleanupWorktree(cancelled) error = %v", err)
	}
	invalid := valid
	invalid.OperationID = "INVALID"
	if _, err := registry.PrepareWorktree(context.Background(), invalid); err == nil {
		t.Fatal("PrepareWorktree(invalid) error = nil")
	}
	if err := registry.CleanupWorktree(context.Background(), CleanupWorktreeRequest(invalid)); err == nil {
		t.Fatal("CleanupWorktree(invalid) error = nil")
	}
	if _, err := registry.PrepareWorktree(context.Background(), valid); !errors.Is(err, ErrRepositoryNotFound) {
		t.Fatalf("PrepareWorktree(missing repository) error = %v", err)
	}
	if err := registry.CleanupWorktree(context.Background(), CleanupWorktreeRequest(valid)); !errors.Is(err, ErrRepositoryNotFound) {
		t.Fatalf("CleanupWorktree(missing repository) error = %v", err)
	}
}

func TestGitMachineRunner_HandlesByteResultsAndPredicateExitCodes(t *testing.T) {
	executable := internalGitExecutable(t)
	output, err := runGitBytes(context.Background(), executable, "--version")
	if err != nil || !strings.HasPrefix(string(output), "git version ") {
		t.Fatalf("runGitBytes(--version) = %q, %v", output, err)
	}
	if _, err := runGitBytes(context.Background(), executable, "not-a-real-command"); err == nil {
		t.Fatal("runGitBytes(invalid) error = nil")
	}

	root := internalCanonicalTempDir(t)
	left := filepath.Join(root, "left")
	right := filepath.Join(root, "right")
	if err := os.WriteFile(left, []byte("same\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(right, []byte("same\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	equal, err := gitPredicate(context.Background(), executable, "diff", "--quiet", "--no-index", left, right)
	if err != nil || !equal {
		t.Fatalf("gitPredicate(equal) = %t, %v", equal, err)
	}
	if err := os.WriteFile(right, []byte("different\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	equal, err = gitPredicate(context.Background(), executable, "diff", "--quiet", "--no-index", left, right)
	if err != nil || equal {
		t.Fatalf("gitPredicate(different) = %t, %v", equal, err)
	}
	if _, err := gitPredicate(context.Background(), executable, "diff", "--bad-option"); err == nil {
		t.Fatal("gitPredicate(invalid) error = nil")
	}
}

func TestGitMachineRunner_ClassifiesCommandUnavailableExitsAsInfrastructure(t *testing.T) {
	root := internalCanonicalTempDir(t)
	for _, exitCode := range []int{126, 127} {
		t.Run(strconv.Itoa(exitCode), func(t *testing.T) {
			executable := filepath.Join(root, "git-exit-"+strconv.Itoa(exitCode))
			if err := os.WriteFile(executable, []byte("#!/bin/sh\nexit "+strconv.Itoa(exitCode)+"\n"), 0o700); err != nil {
				t.Fatal(err)
			}
			if _, _, err := executeGit(context.Background(), executable, "--version"); !errors.Is(err, errGitInfrastructure) {
				t.Fatalf("executeGit(exit %d) error = %v", exitCode, err)
			}
		})
	}
}

func TestWorktreeGitQueries_RefuseUnavailableInventory(t *testing.T) {
	registry := &Registry{gitExecutable: "/usr/bin/false"}
	repository := Repository{PrimaryCheckout: "/unavailable"}
	if _, err := registry.worktreeEntries(context.Background(), repository); err == nil {
		t.Fatal("worktreeEntries(unavailable) error = nil")
	}
	if err := registry.validateOperationBranch(context.Background(), repository, "devcrew/task", "suffix"); err == nil {
		t.Fatal("validateOperationBranch(unavailable) error = nil")
	}
}
