package git

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestCandidateRevisionSnapshotStreamsTreeMetadataBeyondLegacyBuffer(t *testing.T) {
	executable := internalGitExecutable(t)
	repository := filepath.Join(internalCanonicalTempDir(t), "repository")
	if err := os.Mkdir(repository, 0o700); err != nil {
		t.Fatal(err)
	}
	runInternalGit(t, executable, nil, "init", repository)
	blob := runInternalGit(t, executable, []byte("\n"), "--no-optional-locks", "-C", repository,
		"hash-object", "-w", "--stdin")
	var tree bytes.Buffer
	prefix := strings.Repeat("metadata-", 27)
	for index := 0; index < maximumIntegrationTreeEntries; index++ {
		fmt.Fprintf(&tree, "100644 blob %s\t%s%05d%c", blob, prefix, index, byte(0))
	}
	if tree.Len() <= maximumIntegrationTreeListing {
		t.Fatalf("tree fixture metadata = %d, want more than %d", tree.Len(), maximumIntegrationTreeListing)
	}
	treeID := runInternalGit(t, executable, tree.Bytes(), "--no-optional-locks", "-C", repository, "mktree", "-z")

	snapshot, err := (&Registry{gitExecutable: executable}).candidateRevisionSnapshot(
		context.Background(), repository, treeID,
	)
	if err != nil {
		t.Fatalf("candidateRevisionSnapshot(large metadata) error = %v", err)
	}
	if len(snapshot) != maximumIntegrationTreeEntries {
		t.Fatalf("candidateRevisionSnapshot entries = %d, want %d", len(snapshot), maximumIntegrationTreeEntries)
	}
}

func TestCandidateTreeMetadataRejectsUnterminatedAndUnorderedRecords(t *testing.T) {
	objectID := strings.Repeat("a", 40)
	record := func(name string, terminal bool) []byte {
		value := []byte("100644 blob " + objectID + " 1\t" + name)
		if terminal {
			value = append(value, 0)
		}
		return value
	}
	for _, listing := range [][]byte{
		record("unterminated", false),
		append(record("z-last", true), record("a-first", true)...),
	} {
		if _, err := parseCandidateDiffTree(listing); err == nil {
			t.Fatal("parseCandidateDiffTree(ambiguous metadata) error = nil")
		}
	}
}

func runInternalGit(t *testing.T, executable string, input []byte, arguments ...string) string {
	t.Helper()
	command := exec.Command(executable, arguments...)
	command.Env = []string{"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1", "LC_ALL=C"}
	if input != nil {
		command.Stdin = bytes.NewReader(input)
	}
	output, err := command.Output()
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(output))
}
