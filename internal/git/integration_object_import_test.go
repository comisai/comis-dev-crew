package git

import (
	"compress/zlib"
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
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

func TestImportIsolatedGitObjectsHoldsDestinationAcrossSymlinkSwap(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(root, "source")
	destination := filepath.Join(root, "destination")
	heldDestination := filepath.Join(root, "destination-held")
	outside := filepath.Join(root, "outside")
	for _, directory := range []string{source, destination, outside} {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	firstPayload := payloadWithLooseObjectPrefix(t, "00")
	firstID, _ := writeLooseObjectForImportTest(t, source, firstPayload)
	for seed := byte(1); seed <= 4; seed++ {
		payload := make([]byte, 8*1024*1024)
		state := uint32(seed)
		for index := range payload {
			state = state*1664525 + 1013904223
			payload[index] = byte(state >> 24)
		}
		if objectIDForPayload(payload)[:2] == "00" {
			payload[len(payload)-1]++
		}
		writeLooseObjectForImportTest(t, source, payload)
	}

	result := make(chan error, 1)
	go func() { result <- importIsolatedGitObjects(source, destination) }()
	firstTarget := filepath.Join(destination, firstID[:2], firstID[2:])
	deadline := time.Now().Add(20 * time.Second)
	for {
		if _, err := os.Lstat(firstTarget); err == nil {
			break
		}
		select {
		case err := <-result:
			t.Fatalf("import completed before destination swap: %v", err)
		default:
		}
		if time.Now().After(deadline) {
			t.Fatal("first imported object was not published")
		}
		runtime.Gosched()
	}
	if err := os.Rename(destination, heldDestination); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, destination); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-result:
		if err != nil {
			t.Fatalf("importIsolatedGitObjects() error = %v", err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("import did not finish")
	}
	outsideEntries, err := os.ReadDir(outside)
	if err != nil {
		t.Fatal(err)
	}
	if len(outsideEntries) != 0 {
		t.Fatalf("outside entries = %d, want 0", len(outsideEntries))
	}
	if _, err := os.Stat(filepath.Join(heldDestination, firstID[:2], firstID[2:])); err != nil {
		t.Fatalf("held destination first object: %v", err)
	}
}

func payloadWithLooseObjectPrefix(t *testing.T, prefix string) []byte {
	t.Helper()
	for index := 0; index < 100000; index++ {
		payload := []byte(fmt.Sprintf("first-object-%d\n", index))
		if objectIDForPayload(payload)[:2] == prefix {
			return payload
		}
	}
	t.Fatalf("no loose object with prefix %q", prefix)
	return nil
}

func objectIDForPayload(payload []byte) string {
	raw := append([]byte(fmt.Sprintf("blob %d\x00", len(payload))), payload...)
	digest := sha1.Sum(raw)
	return hex.EncodeToString(digest[:])
}

func writeLooseObjectForImportTest(t *testing.T, root string, payload []byte) (string, string) {
	t.Helper()
	raw := append([]byte(fmt.Sprintf("blob %d\x00", len(payload))), payload...)
	objectID := objectIDForPayload(payload)
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
