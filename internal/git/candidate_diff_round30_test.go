package git

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCandidateWorktreeSnapshotBoundsAggregateRetainedContent(t *testing.T) {
	root := t.TempDir()
	contents := bytes.Repeat([]byte{'x'}, 1<<20)
	index := make(candidateDiffSnapshot)
	for number := 0; number < 65; number++ {
		name := fmt.Sprintf("expanded-%03d", number)
		if err := os.WriteFile(filepath.Join(root, name), contents, 0o600); err != nil {
			t.Fatal(err)
		}
		index[name] = candidateDiffEntry{mode: "100644", objectID: strings.Repeat("a", 40), size: int64(len(contents))}
	}
	snapshot, err := (&Registry{}).candidateWorktreeSnapshot(context.Background(), root, index)
	if err != nil {
		t.Fatalf("candidateWorktreeSnapshot(aggregate over bound) error = %v", err)
	}
	known := 0
	for _, entry := range snapshot {
		if entry.detailKnown {
			known++
		}
	}
	if known != 64 {
		t.Fatalf("retained detailed entries = %d, want 64", known)
	}
}

func TestCandidateRenameMatchingHasBoundedWork(t *testing.T) {
	if os.Getenv("DEV_CREW_RENAME_STRESS") == "1" {
		deleted := make(candidateDiffSnapshot, 32000)
		added := make(candidateDiffSnapshot, 32000)
		entry := candidateDiffEntry{mode: "100644", objectID: strings.Repeat("a", 40), contents: []byte("same\n"), detailKnown: true}
		for number := 0; number < 32000; number++ {
			deleted[fmt.Sprintf("old-%05d", number)] = entry
			added[fmt.Sprintf("new-%05d", number)] = entry
		}
		if changes, truncated := candidateRenamesAndUnpaired(deleted, added); len(changes) != 32000 || truncated {
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

func TestCandidateContentChangeHandlesLineEdgeCases(t *testing.T) {
	tests := []struct {
		name           string
		before, after  []byte
		added, deleted int
		binary         bool
	}{
		{name: "repeated lines", before: []byte("a\nb\na\n"), after: []byte("a\na\nb\n"), added: 1, deleted: 1},
		{name: "no final newline", before: []byte("before"), after: []byte("after"), added: 1, deleted: 1},
		{name: "binary", before: []byte{'a', 0}, after: []byte{'b', 0}, binary: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			change, truncated := candidateContentChangeExtent("component", "", test.before, test.after)
			if truncated || change.Added != test.added || change.Deleted != test.deleted || change.Binary != test.binary {
				t.Fatalf("candidateContentChangeExtent() = %#v, truncated=%t", change, truncated)
			}
		})
	}
}

func TestCandidateContentChangeReportsBoundExhaustion(t *testing.T) {
	before := make([][]byte, 3000)
	after := make([][]byte, 3000)
	for index := range before {
		before[index] = []byte(fmt.Sprintf("before-%04d", index))
		after[index] = []byte(fmt.Sprintf("after-%04d", index))
	}
	if _, _, exact := candidateLineExtent(before, after); exact {
		t.Fatal("candidateLineExtent(adversarial input) reported an exact extent")
	}
	beforeContents := bytes.Join(before, []byte{'\n'})
	afterContents := bytes.Join(after, []byte{'\n'})
	changes, truncated := candidateSnapshotChanges(
		candidateDiffSnapshot{"component": {mode: "100644", objectID: strings.Repeat("a", 40), contents: beforeContents, detailKnown: true}},
		candidateDiffSnapshot{"component": {mode: "100644", objectID: strings.Repeat("b", 40), contents: afterContents, detailKnown: true}},
	)
	if len(changes) != 1 || !truncated {
		t.Fatalf("candidateSnapshotChanges(bound exhaustion) = %#v, truncated=%t", changes, truncated)
	}
}

func TestCandidateContentChangeBoundsLineMetadata(t *testing.T) {
	contents := bytes.Repeat([]byte("x\n"), maximumCandidateDiffLines+1)
	change, truncated := candidateContentChangeExtent("component", "", contents, []byte("result\n"))
	if !truncated || change.Binary || change.Added != 0 || change.Deleted != 0 {
		t.Fatalf("candidateContentChangeExtent(line bound) = %#v, truncated=%t", change, truncated)
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
