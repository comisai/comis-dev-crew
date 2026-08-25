package git

import (
	"compress/zlib"
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestImportIsolatedGitObjectsCopiesAndValidatesLooseObjects(t *testing.T) {
	for _, test := range []struct {
		name    string
		corrupt bool
	}{
		{name: "independent copy"},
		{name: "corrupt object", corrupt: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			root, err := filepath.EvalSymlinks(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			source := filepath.Join(root, "source")
			destination := filepath.Join(root, "destination")
			if err := os.MkdirAll(source, 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.MkdirAll(destination, 0o700); err != nil {
				t.Fatal(err)
			}
			objectID, sourcePath := writeLooseObjectForImportTest(t, source, []byte("isolated object\n"))
			if test.corrupt {
				if err := os.WriteFile(sourcePath, []byte("not a loose object"), 0o600); err != nil {
					t.Fatal(err)
				}
				if err := importIsolatedGitObjects(source, destination); err == nil {
					t.Fatal("importIsolatedGitObjects(corrupt) error = nil")
				}
				return
			}
			if err := importIsolatedGitObjects(source, destination); err != nil {
				t.Fatal(err)
			}
			destinationPath := filepath.Join(destination, objectID[:2], objectID[2:])
			sourceInfo, err := os.Stat(sourcePath)
			if err != nil {
				t.Fatal(err)
			}
			destinationInfo, err := os.Stat(destinationPath)
			if err != nil {
				t.Fatal(err)
			}
			if os.SameFile(sourceInfo, destinationInfo) {
				t.Fatal("imported object shares its source inode")
			}
		})
	}
}

func writeLooseObjectForImportTest(t *testing.T, root string, payload []byte) (string, string) {
	t.Helper()
	raw := append([]byte(fmt.Sprintf("blob %d\x00", len(payload))), payload...)
	digest := sha1.Sum(raw)
	objectID := hex.EncodeToString(digest[:])
	directory := filepath.Join(root, objectID[:2])
	if err := os.MkdirAll(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(directory, objectID[2:])
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	compressed := zlib.NewWriter(file)
	if _, err := compressed.Write(raw); err != nil {
		t.Fatal(err)
	}
	if err := compressed.Close(); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	return objectID, path
}
