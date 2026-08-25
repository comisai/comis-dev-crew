package git

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestMaterializeIntegrationWorktreePreservesRacingDeveloperReplacement(t *testing.T) {
	root := t.TempDir()
	name := "component.txt"
	if err := os.WriteFile(filepath.Join(root, name), []byte("expected\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	resultContents := make([]byte, maximumIntegrationBlobBytes)
	for index := range resultContents {
		resultContents[index] = byte(index%251 + 1)
	}
	expected := integrationTreeSnapshot{name: {
		mode: "100644", objectID: "expected-object", contents: []byte("expected\n"),
	}}
	resulting := integrationTreeSnapshot{name: {
		mode: "100644", objectID: "result-object", contents: resultContents,
	}}
	digest := sha256.Sum256([]byte(name + "\x00" + resulting[name].objectID))
	temporary := filepath.Join(root, ".comis-materialize-"+hex.EncodeToString(digest[:12]))
	traced := make(chan error, 1)
	stop := make(chan struct{})
	go func() {
		for {
			if _, err := os.Lstat(temporary); err == nil {
				developer := filepath.Join(root, "developer-replacement")
				if err := os.WriteFile(developer, []byte("developer edit\n"), 0o600); err != nil {
					traced <- err
					return
				}
				traced <- os.Rename(developer, filepath.Join(root, name))
				return
			}
			if _, err := os.Lstat(filepath.Join(root, name)); errors.Is(err, os.ErrNotExist) {
				traced <- os.WriteFile(filepath.Join(root, name), []byte("developer edit\n"), 0o600)
				return
			}
			select {
			case <-stop:
				traced <- errors.New("materialization race boundary was not observed")
				return
			default:
				runtime.Gosched()
			}
		}
	}()
	err := materializeIntegrationWorktree(root, expected, resulting)
	close(stop)
	if raceErr := <-traced; raceErr != nil {
		t.Fatal(raceErr)
	}
	if err == nil {
		t.Fatal("materializeIntegrationWorktree(racing replacement) error = nil")
	}
	contents, readErr := os.ReadFile(filepath.Join(root, name))
	if readErr != nil || string(contents) != "developer edit\n" {
		t.Fatalf("developer replacement = %q, %v", contents, readErr)
	}
}

func TestIntegrationRootMatchesSnapshotBoundsOversizedTrackedFile(t *testing.T) {
	rootPath := t.TempDir()
	file, err := os.OpenFile(filepath.Join(rootPath, "tracked.bin"), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if err := file.Truncate(128 << 20); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	root, err := os.OpenRoot(rootPath)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	snapshot := integrationTreeSnapshot{"tracked.bin": {
		mode: "100644", objectID: "expected-object", contents: []byte("x"),
	}}
	runtime.GC()
	var before runtime.MemStats
	runtime.ReadMemStats(&before)
	matches, err := integrationRootMatchesSnapshot(root, snapshot)
	var after runtime.MemStats
	runtime.ReadMemStats(&after)
	if err != nil || matches {
		t.Fatalf("integrationRootMatchesSnapshot(oversized) = %t, %v", matches, err)
	}
	if allocated := after.TotalAlloc - before.TotalAlloc; allocated > 4<<20 {
		t.Fatalf("oversized comparison allocated %d bytes", allocated)
	}
}

func TestMaterializeIntegrationWorktreePreservesEditsAtPublicationBoundaries(t *testing.T) {
	for _, boundary := range []string{"before-capture", "before-publication"} {
		t.Run(boundary, func(t *testing.T) {
			root := t.TempDir()
			name := "component.txt"
			path := filepath.Join(root, name)
			if err := os.WriteFile(path, []byte("expected\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			expected := integrationTreeSnapshot{name: {
				mode: "100644", objectID: "expected-object", contents: []byte("expected\n"),
			}}
			resulting := integrationTreeSnapshot{name: {
				mode: "100644", objectID: "result-object", contents: []byte("result\n"),
			}}
			invoked := false
			err := materializeIntegrationWorktreeAtBoundary(root, expected, resulting,
				func(observed string, _ string) {
					if observed != boundary || invoked {
						return
					}
					invoked = true
					developer := filepath.Join(root, "developer")
					if err := os.WriteFile(developer, []byte("developer edit\n"), 0o600); err != nil {
						t.Fatal(err)
					}
					if err := os.Rename(developer, path); err != nil {
						t.Fatal(err)
					}
				})
			if !invoked || err == nil {
				t.Fatalf("materializeIntegrationWorktreeAtBoundary(%s) invoked = %t, error = %v",
					boundary, invoked, err)
			}
			contents, readErr := os.ReadFile(path)
			if readErr != nil || string(contents) != "developer edit\n" {
				t.Fatalf("developer edit = %q, %v", contents, readErr)
			}
		})
	}
}

func TestIntegrationRegularFileComparisonRejectsConcurrentReplacement(t *testing.T) {
	rootPath := t.TempDir()
	name := "tracked.bin"
	expected := make([]byte, 1<<20)
	if err := os.WriteFile(filepath.Join(rootPath, name), expected, 0o600); err != nil {
		t.Fatal(err)
	}
	root, err := os.OpenRoot(rootPath)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	initial, err := root.Lstat(name)
	if err != nil {
		t.Fatal(err)
	}
	matches, err := integrationRegularFileMatchesAtBoundary(root, name, initial, expected, func() {
		replacement := filepath.Join(rootPath, "replacement")
		contents := make([]byte, len(expected))
		contents[0] = 1
		if err := os.WriteFile(replacement, contents, 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Rename(replacement, filepath.Join(rootPath, name)); err != nil {
			t.Fatal(err)
		}
	})
	if err != nil || matches {
		t.Fatalf("integrationRegularFileMatchesAtBoundary(replaced) = %t, %v", matches, err)
	}
	contents, err := os.ReadFile(filepath.Join(rootPath, name))
	if err != nil || len(contents) != len(expected) || contents[0] != 1 {
		t.Fatalf("replacement identity = %d bytes, %v", len(contents), err)
	}
}
