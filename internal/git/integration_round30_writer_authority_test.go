package git

import (
	"os"
	"path/filepath"
	"testing"
)

func TestMaterializationRejectsTrackedRewriteBeforePublication(t *testing.T) {
	root := t.TempDir()
	name := "component.txt"
	path := filepath.Join(root, name)
	if err := os.WriteFile(path, []byte("expected\n"), 0o600); err != nil {
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
	err = materializeIntegrationWorktreeAtBoundary(root, expected, resulting, func(string, string) {
		invoked = true
	})
	if err == nil {
		t.Fatal("materializeIntegrationWorktreeAtBoundary(existing tracked rewrite) error = nil")
	}
	if invoked {
		t.Fatal("tracked rewrite reached a publication boundary")
	}
	developer := []byte("developer-after-refusal\n")
	if _, err := writer.WriteAt(developer, 0); err != nil {
		t.Fatal(err)
	}
	if err := writer.Truncate(int64(len(developer))); err != nil {
		t.Fatal(err)
	}
	contents, err := os.ReadFile(path)
	if err != nil || string(contents) != string(developer) {
		t.Fatalf("developer bytes after refusal = %q, %v", contents, err)
	}
}
