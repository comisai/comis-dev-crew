package git

import (
	"os"
	"path/filepath"
	"testing"
)

func TestMaterializationRewritesTrackedEntryThroughItsExistingInode(t *testing.T) {
	root := t.TempDir()
	name := "component.txt"
	path := filepath.Join(root, name)
	if err := os.WriteFile(path, []byte("expected\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	before, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	writer, err := os.OpenFile(path, os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = writer.Close() })
	expected := integrationTreeSnapshot{name: {
		mode: "100644", objectID: "expected-object", contents: []byte("expected\n"),
	}}
	resulting := integrationTreeSnapshot{name: {
		mode: "100644", objectID: "result-object", contents: []byte("result\n"),
	}}
	invoked := false
	err = materializeIntegrationWorktreeAtBoundary(root, expected, resulting, func(observed string, _ string) {
		if observed == "before-publication" {
			invoked = true
		}
	})
	if err != nil {
		t.Fatalf("materializeIntegrationWorktreeAtBoundary(tracked rewrite) error = %v", err)
	}
	if !invoked {
		t.Fatal("tracked rewrite never reached its publication boundary")
	}
	contents, err := os.ReadFile(path)
	if err != nil || string(contents) != "result\n" {
		t.Fatalf("materialized bytes = %q, %v", contents, err)
	}
	after, err := os.Lstat(path)
	if err != nil || !os.SameFile(before, after) {
		t.Fatalf("tracked entry identity changed across materialization: %v", err)
	}
	developer := []byte("developer-after-rewrite\n")
	if _, err := writer.WriteAt(developer, 0); err != nil {
		t.Fatal(err)
	}
	if err := writer.Truncate(int64(len(developer))); err != nil {
		t.Fatal(err)
	}
	contents, err = os.ReadFile(path)
	if err != nil || string(contents) != string(developer) {
		t.Fatalf("developer bytes through retained descriptor = %q, %v", contents, err)
	}
}
