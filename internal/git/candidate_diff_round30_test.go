package git

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestCandidateWorktreeSnapshotBoundsAggregateRetainedContent(t *testing.T) {
	root := t.TempDir()
	contents := bytes.Repeat([]byte{'x'}, 1<<20)
	index := make(integrationTreeSnapshot)
	for number := 0; number < 65; number++ {
		name := fmt.Sprintf("expanded-%03d", number)
		if err := os.WriteFile(filepath.Join(root, name), contents, 0o600); err != nil {
			t.Fatal(err)
		}
		index[name] = integrationTreeEntry{mode: "100644", objectID: fmt.Sprintf("object-%03d", number)}
	}
	if _, err := candidateWorktreeSnapshot(root, index); err == nil {
		t.Fatal("candidateWorktreeSnapshot(aggregate over bound) error = nil")
	}
}

func TestCandidateRenameMatchingHasBoundedWork(t *testing.T) {
	if os.Getenv("DEV_CREW_RENAME_STRESS") == "1" {
		deleted := make(map[string]integrationTreeEntry, 32000)
		added := make(map[string]integrationTreeEntry, 32000)
		entry := integrationTreeEntry{mode: "100644", contents: []byte("same\n")}
		for number := 0; number < 32000; number++ {
			deleted[fmt.Sprintf("old-%05d", number)] = entry
			added[fmt.Sprintf("new-%05d", number)] = entry
		}
		if changes := candidateRenamesAndUnpaired(deleted, added); len(changes) != 32000 {
			t.Fatalf("rename count = %d", len(changes))
		}
		return
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, executable, "-test.run=^TestCandidateRenameMatchingHasBoundedWork$")
	command.Env = append(os.Environ(), "DEV_CREW_RENAME_STRESS=1")
	output, runErr := command.CombinedOutput()
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		t.Fatal("rename matching exceeded its deterministic work deadline")
	}
	if runErr != nil {
		t.Fatalf("rename matching subprocess: %v\n%s", runErr, output)
	}
}

func TestCandidateContentChangeCountsInteriorMatchesExactly(t *testing.T) {
	change := candidateContentChange(
		"component.txt", "",
		[]byte("before-one\nretained\nbefore-three\n"),
		[]byte("after-one\nretained\nafter-three\n"),
	)
	if change.Added != 2 || change.Deleted != 2 || change.Binary {
		t.Fatalf("candidateContentChange() = %#v", change)
	}
}
