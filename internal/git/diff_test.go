package git_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/comisai/comis-dev-crew/internal/application"
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
	// A task whose worktree is gone has no work to describe. Reporting an empty
	// change set would say the worker changed nothing.
	removed := base
	removed.TaskHandle = "task-absent"
	removed.WorktreePath = filepath.Join(fixture.worktreeRoot, "task-absent")
	if _, err := registry.InspectCandidateDiff(context.Background(), removed); err == nil {
		t.Error("InspectCandidateDiff(missing worktree) error = nil")
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

// A change set larger than this read bounds reports a truncated listing instead
// of an error or a partial listing presented as complete. An operator deciding
// from a file list has to know when it is not the whole list.
func TestRegistry_InspectCandidateDiffReportsATruncatedListingRatherThanFailing(t *testing.T) {
	fixture := newRepositoryFixture(t, "product-api")
	registry := newLifecycleRegistry(t, fixture)
	request := lifecycleRequest(t, fixture, "prepare-truncated", "task-truncated")
	prepared, err := registry.PrepareWorktree(context.Background(), request)
	if err != nil {
		t.Fatalf("PrepareWorktree() error = %v", err)
	}
	// Enough files that the numeric summary outgrows both the file-count bound
	// and the byte bound the Git runner enforces.
	for index := 0; index < 700; index++ {
		name := fmt.Sprintf("file-%04d.txt", index)
		if err := os.WriteFile(filepath.Join(prepared.CanonicalPath, name), []byte("one\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	runGit(t, fixture.gitExecutable, "--no-optional-locks", "-C", prepared.CanonicalPath, "add", ".")
	runGit(t, fixture.gitExecutable, "--no-optional-locks", "-C", prepared.CanonicalPath,
		"-c", "user.name=DevCrew Fixture", "-c", "user.email=fixture@example.invalid",
		"commit", "-m", "many files")

	diff, err := registry.InspectCandidateDiff(context.Background(), devgit.CandidateDiffRequest{
		TaskHandle: request.TaskHandle, RepositoryID: request.RepositoryID,
		WorktreePath: prepared.CanonicalPath, BaseRevision: request.BaseRevision,
	})
	if err != nil {
		t.Fatalf("InspectCandidateDiff(large change set) error = %v", err)
	}
	if !diff.FileListTruncated {
		t.Fatalf("a %d-file change set did not report truncation: %#v", 700, diff.CommittedTotals)
	}
	if len(diff.Committed) > 256 {
		t.Fatalf("the listing exceeded its own bound: %d rows", len(diff.Committed))
	}
	if diff.HeadRevision == request.BaseRevision {
		t.Fatal("the head was not read for a truncated change set")
	}
}

// A change set past the file-count bound but inside the byte bound is trimmed to
// the bound and says so, rather than being reported as a complete listing.
func TestRegistry_InspectCandidateDiffTrimsToItsFileBound(t *testing.T) {
	fixture := newRepositoryFixture(t, "product-api")
	registry := newLifecycleRegistry(t, fixture)
	request := lifecycleRequest(t, fixture, "prepare-trimmed", "task-trimmed")
	prepared, err := registry.PrepareWorktree(context.Background(), request)
	if err != nil {
		t.Fatalf("PrepareWorktree() error = %v", err)
	}
	for index := 0; index < 260; index++ {
		name := fmt.Sprintf("f%03d", index)
		if err := os.WriteFile(filepath.Join(prepared.CanonicalPath, name), []byte("a\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	runGit(t, fixture.gitExecutable, "--no-optional-locks", "-C", prepared.CanonicalPath, "add", ".")
	runGit(t, fixture.gitExecutable, "--no-optional-locks", "-C", prepared.CanonicalPath,
		"-c", "user.name=DevCrew Fixture", "-c", "user.email=fixture@example.invalid",
		"commit", "-m", "just past the bound")

	diff, err := registry.InspectCandidateDiff(context.Background(), devgit.CandidateDiffRequest{
		TaskHandle: request.TaskHandle, RepositoryID: request.RepositoryID,
		WorktreePath: prepared.CanonicalPath, BaseRevision: request.BaseRevision,
	})
	if err != nil {
		t.Fatalf("InspectCandidateDiff() error = %v", err)
	}
	if !diff.FileListTruncated || len(diff.Committed) != 256 {
		t.Fatalf("trimmed listing = %d rows, truncated = %t", len(diff.Committed), diff.FileListTruncated)
	}
}

// The application port is what the service actually calls, so the change record
// has to survive the crossing intact: a rename that lost its previous path, or a
// binary file ported as a zero-line change, would misdescribe the work at the
// only layer an operator ever sees.
func TestRegistry_InspectTaskDiffPortsEveryChangeRecordIntact(t *testing.T) {
	fixture := newRepositoryFixture(t, "product-api")
	registry := newLifecycleRegistry(t, fixture)
	request := lifecycleRequest(t, fixture, "prepare-port", "task-port")
	prepared, err := registry.PrepareWorktree(context.Background(), request)
	if err != nil {
		t.Fatalf("PrepareWorktree() error = %v", err)
	}
	if err := os.WriteFile(
		filepath.Join(prepared.CanonicalPath, "asset.bin"), []byte{0x00, 0x01, 0x02, 0x00}, 0o600,
	); err != nil {
		t.Fatal(err)
	}
	runGit(t, fixture.gitExecutable, "--no-optional-locks", "-C", prepared.CanonicalPath,
		"mv", "fixture.txt", "renamed.txt")
	runGit(t, fixture.gitExecutable, "--no-optional-locks", "-C", prepared.CanonicalPath, "add", ".")
	runGit(t, fixture.gitExecutable, "--no-optional-locks", "-C", prepared.CanonicalPath,
		"-c", "user.name=DevCrew Fixture", "-c", "user.email=fixture@example.invalid",
		"commit", "-m", "ported change")
	if err := os.WriteFile(filepath.Join(prepared.CanonicalPath, "renamed.txt"), []byte("edited\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	var inspector application.TaskDiffInspector = registry
	view, err := inspector.InspectTaskDiff(context.Background(), application.TaskDiffRequest{
		TaskHandle: request.TaskHandle, RepositoryID: request.RepositoryID,
		WorktreePath: prepared.CanonicalPath, BaseRevision: request.BaseRevision,
	})
	if err != nil {
		t.Fatalf("InspectTaskDiff() error = %v", err)
	}
	if view.BaseRevision != request.BaseRevision || view.HeadRevision == request.BaseRevision {
		t.Fatalf("ported revisions = %#v", view)
	}
	var binary, renamed bool
	for _, change := range view.Committed {
		if change.Path == "asset.bin" && change.Binary {
			binary = true
		}
		if change.Path == "renamed.txt" && change.PreviousPath == "fixture.txt" {
			renamed = true
		}
	}
	if !binary || !renamed {
		t.Fatalf("ported committed changes = %#v", view.Committed)
	}
	if view.CommittedTotals.BinaryFiles != 1 || view.CommittedTotals.Files != 2 {
		t.Fatalf("ported committed totals = %#v", view.CommittedTotals)
	}
	if len(view.Uncommitted) != 1 || view.Uncommitted[0].Path != "renamed.txt" {
		t.Fatalf("ported uncommitted changes = %#v", view.Uncommitted)
	}
	if view.UncommittedTotals.Files != 1 {
		t.Fatalf("ported uncommitted totals = %#v", view.UncommittedTotals)
	}

	// A refusal has to cross the port as a refusal, never as an empty change set.
	if _, err := inspector.InspectTaskDiff(context.Background(), application.TaskDiffRequest{
		TaskHandle: "task-absent", RepositoryID: request.RepositoryID,
		WorktreePath: filepath.Join(fixture.worktreeRoot, "task-absent"), BaseRevision: request.BaseRevision,
	}); err == nil {
		t.Error("InspectTaskDiff(missing worktree) error = nil")
	}
}
