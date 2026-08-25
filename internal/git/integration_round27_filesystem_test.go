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
