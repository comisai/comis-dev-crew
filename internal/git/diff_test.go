package git_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	devgit "github.com/comisai/comis-dev-crew/internal/git"
)

// An operator supervising development needs to know what a worker changed. The
// committed and uncommitted halves are separated because they mean different
// things: one is work the worker stands behind, the other is work in progress
// that a handback would land in a developer's editor.
func TestRegistry_InspectCandidateDiffSeparatesCommittedFromUncommittedWork(t *testing.T) {
	fixture := newRepositoryFixture(t, "product-api")
	registry := newLifecycleRegistry(t, fixture)
	request := lifecycleRequest(t, fixture, "prepare-diff", "task-diff")
	prepared, err := registry.PrepareWorktree(context.Background(), request)
	if err != nil {
		t.Fatalf("PrepareWorktree() error = %v", err)
	}

	empty, err := registry.InspectCandidateDiff(context.Background(), devgit.CandidateDiffRequest{
		TaskHandle: request.TaskHandle, RepositoryID: request.RepositoryID,
		WorktreePath: prepared.CanonicalPath, BaseRevision: request.BaseRevision,
	})
	if err != nil {
		t.Fatalf("InspectCandidateDiff(unchanged) error = %v", err)
	}
	if len(empty.Committed) != 0 || len(empty.Uncommitted) != 0 || empty.HeadRevision != prepared.HeadRevision {
		t.Fatalf("unchanged diff = %#v", empty)
	}

	committed := filepath.Join(prepared.CanonicalPath, "committed.txt")
	if err := os.WriteFile(committed, []byte("one\ntwo\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runGit(t, fixture.gitExecutable, "--no-optional-locks", "-C", prepared.CanonicalPath, "add", "committed.txt")
	runGit(t, fixture.gitExecutable, "--no-optional-locks", "-C", prepared.CanonicalPath,
		"-c", "user.name=DevCrew Fixture", "-c", "user.email=fixture@example.invalid",
		"commit", "-m", "committed change")
	if err := os.WriteFile(filepath.Join(prepared.CanonicalPath, "fixture.txt"), []byte("changed\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	diff, err := registry.InspectCandidateDiff(context.Background(), devgit.CandidateDiffRequest{
		TaskHandle: request.TaskHandle, RepositoryID: request.RepositoryID,
		WorktreePath: prepared.CanonicalPath, BaseRevision: request.BaseRevision,
	})
	if err != nil {
		t.Fatalf("InspectCandidateDiff() error = %v", err)
	}
	if diff.BaseRevision != request.BaseRevision || diff.HeadRevision == request.BaseRevision {
		t.Fatalf("diff revisions = %#v", diff)
	}
	if len(diff.Committed) != 1 || diff.Committed[0].Path != "committed.txt" ||
		diff.Committed[0].Added != 2 || diff.Committed[0].Deleted != 0 {
		t.Fatalf("committed changes = %#v", diff.Committed)
	}
	if len(diff.Uncommitted) != 1 || diff.Uncommitted[0].Path != "fixture.txt" {
		t.Fatalf("uncommitted changes = %#v", diff.Uncommitted)
	}
	if diff.CommittedTotals.Files != 1 || diff.CommittedTotals.Added != 2 {
		t.Fatalf("committed totals = %#v", diff.CommittedTotals)
	}
	if diff.FileListTruncated {
		t.Error("a two-file change reported a truncated file list")
	}
}

// A binary file has no line counts. Reporting zeros would read as an empty
// change, so the row says binary instead of claiming nothing moved.
func TestRegistry_InspectCandidateDiffMarksABinaryChangeRatherThanCountingZero(t *testing.T) {
	fixture := newRepositoryFixture(t, "product-api")
	registry := newLifecycleRegistry(t, fixture)
	request := lifecycleRequest(t, fixture, "prepare-binary", "task-binary")
	prepared, err := registry.PrepareWorktree(context.Background(), request)
	if err != nil {
		t.Fatalf("PrepareWorktree() error = %v", err)
	}
	if err := os.WriteFile(
		filepath.Join(prepared.CanonicalPath, "asset.bin"), []byte{0x00, 0x01, 0x02, 0x00}, 0o600,
	); err != nil {
		t.Fatal(err)
	}
	runGit(t, fixture.gitExecutable, "--no-optional-locks", "-C", prepared.CanonicalPath, "add", "asset.bin")
	runGit(t, fixture.gitExecutable, "--no-optional-locks", "-C", prepared.CanonicalPath,
		"-c", "user.name=DevCrew Fixture", "-c", "user.email=fixture@example.invalid",
		"commit", "-m", "binary change")

	diff, err := registry.InspectCandidateDiff(context.Background(), devgit.CandidateDiffRequest{
		TaskHandle: request.TaskHandle, RepositoryID: request.RepositoryID,
		WorktreePath: prepared.CanonicalPath, BaseRevision: request.BaseRevision,
	})
	if err != nil {
		t.Fatalf("InspectCandidateDiff() error = %v", err)
	}
	if len(diff.Committed) != 1 || !diff.Committed[0].Binary || diff.Committed[0].Path != "asset.bin" {
		t.Fatalf("binary change = %#v", diff.Committed)
	}
}

// A rename is one change with a current path, not two half-changes. The record
// carries both paths so the row cannot silently attribute the work to a file
// that no longer exists.
func TestRegistry_InspectCandidateDiffReportsARenameWithBothPaths(t *testing.T) {
	fixture := newRepositoryFixture(t, "product-api")
	registry := newLifecycleRegistry(t, fixture)
	request := lifecycleRequest(t, fixture, "prepare-rename", "task-rename")
	prepared, err := registry.PrepareWorktree(context.Background(), request)
	if err != nil {
		t.Fatalf("PrepareWorktree() error = %v", err)
	}
	runGit(t, fixture.gitExecutable, "--no-optional-locks", "-C", prepared.CanonicalPath,
		"mv", "fixture.txt", "renamed.txt")
	runGit(t, fixture.gitExecutable, "--no-optional-locks", "-C", prepared.CanonicalPath,
		"-c", "user.name=DevCrew Fixture", "-c", "user.email=fixture@example.invalid",
		"commit", "-m", "renamed")

	diff, err := registry.InspectCandidateDiff(context.Background(), devgit.CandidateDiffRequest{
		TaskHandle: request.TaskHandle, RepositoryID: request.RepositoryID,
		WorktreePath: prepared.CanonicalPath, BaseRevision: request.BaseRevision,
	})
	if err != nil {
		t.Fatalf("InspectCandidateDiff() error = %v", err)
	}
	if len(diff.Committed) != 1 {
		t.Fatalf("rename = %#v", diff.Committed)
	}
	if diff.Committed[0].Path != "renamed.txt" || diff.Committed[0].PreviousPath != "fixture.txt" {
		t.Fatalf("rename paths = %#v", diff.Committed[0])
	}
}

// The diff is read from the task's own verified worktree. A path outside that
// root, an unverified worktree, or a base revision the repository does not know
// is refused rather than read from somewhere else.
func TestRegistry_InspectCandidateDiffRefusesUnverifiedAuthority(t *testing.T) {
	fixture := newRepositoryFixture(t, "product-api")
	registry := newLifecycleRegistry(t, fixture)
	request := lifecycleRequest(t, fixture, "prepare-refusal", "task-refusal")
	prepared, err := registry.PrepareWorktree(context.Background(), request)
	if err != nil {
		t.Fatalf("PrepareWorktree() error = %v", err)
	}
	base := devgit.CandidateDiffRequest{
		TaskHandle: request.TaskHandle, RepositoryID: request.RepositoryID,
		WorktreePath: prepared.CanonicalPath, BaseRevision: request.BaseRevision,
	}
	sibling := base
	sibling.WorktreePath = fixture.primary
	unknownRepository := base
	unknownRepository.RepositoryID = "absent-repository"
	invalidHandle := base
	invalidHandle.TaskHandle = "not a handle"
	unknownBase := base
	unknownBase.BaseRevision = strings.Repeat("b", 40)
	emptyBase := base
	emptyBase.BaseRevision = ""

	for name, request := range map[string]devgit.CandidateDiffRequest{
		"sibling worktree":    sibling,
		"unknown repository":  unknownRepository,
		"invalid task handle": invalidHandle,
		"unknown base":        unknownBase,
		"absent base":         emptyBase,
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := registry.InspectCandidateDiff(context.Background(), request); err == nil {
				t.Fatal("InspectCandidateDiff() error = nil, want a refusal")
			}
		})
	}
	if _, err := registry.InspectCandidateDiff(missingGitContext(), base); err == nil {
		t.Error("InspectCandidateDiff(no context) error = nil")
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := registry.InspectCandidateDiff(canceled, base); err == nil {
		t.Error("InspectCandidateDiff(canceled) error = nil")
	}
}
