package git

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestImportIsolatedGitObjectsRejectsOversizedObjectBeforePublication(t *testing.T) {
	root := internalCanonicalTempDir(t)
	source := filepath.Join(root, "source")
	destination := filepath.Join(root, "destination")
	for _, directory := range []string{source, destination} {
		if err := os.Mkdir(directory, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	writeLooseObjectForImportTest(t, source, make([]byte, maximumIntegrationBlobBytes+1))

	if err := importIsolatedGitObjects(source, destination); err == nil {
		t.Fatal("importIsolatedGitObjects(oversized object) error = nil")
	}
	assertObjectDirectoryEmpty(t, destination)
}

func TestImportIsolatedGitObjectsRejectsExcessiveSetBeforePublication(t *testing.T) {
	root := internalCanonicalTempDir(t)
	source := filepath.Join(root, "source")
	destination := filepath.Join(root, "destination")
	for _, directory := range []string{source, destination} {
		if err := os.Mkdir(directory, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	for index := 0; index < 8_193; index++ {
		writeLooseObjectForImportTest(t, source, []byte(fmt.Sprintf("isolated-object-%05d\n", index)))
	}

	if err := importIsolatedGitObjects(source, destination); err == nil {
		t.Fatal("importIsolatedGitObjects(excessive object set) error = nil")
	}
	assertObjectDirectoryEmpty(t, destination)
}

func assertObjectDirectoryEmpty(t *testing.T, directory string) {
	t.Helper()
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("shared object directory contains %d entries after refused import", len(entries))
	}
}
