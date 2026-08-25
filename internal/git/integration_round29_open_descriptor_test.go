package git

import (
	"os"
	"path/filepath"
	"testing"
)

func TestMaterializationPreservesWritesThroughOpenTrackedDescriptor(t *testing.T) {
	for _, boundary := range []string{"before-publication", "before-recovery-retirement"} {
		t.Run(boundary, func(t *testing.T) {
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
			developer := []byte("developer-write\n")
			invoked := false
			materializeErr := materializeIntegrationWorktreeAtBoundary(root, expected, resulting,
				func(observed string, _ string) {
					if observed != boundary || invoked {
						return
					}
					invoked = true
					if _, err := writer.Seek(0, 0); err != nil {
						t.Fatal(err)
					}
					if written, err := writer.Write(developer); err != nil || written != len(developer) {
						t.Fatalf("open descriptor write = %d, %v", written, err)
					}
					if err := writer.Truncate(int64(len(developer))); err != nil {
						t.Fatal(err)
					}
					if err := writer.Sync(); err != nil {
						t.Fatal(err)
					}
				})
			if !invoked {
				t.Fatalf("materialization boundary %q was not reached: %v", boundary, materializeErr)
			}
			contents, err := os.ReadFile(path)
			if err != nil || string(contents) != string(developer) {
				t.Fatalf("developer bytes after materialization = %q, %v; materialization error = %v",
					contents, err, materializeErr)
			}
		})
	}
}
