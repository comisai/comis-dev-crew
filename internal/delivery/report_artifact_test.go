package delivery

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReportArtifactInspector_HashesOnlyStableBoundedRegularFile(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "report.md")
	contents := []byte("bounded scout report\n")
	if err := os.WriteFile(path, contents, 0o600); err != nil {
		t.Fatalf("write report: %v", err)
	}
	evidence, err := InspectReportArtifact(context.Background(), path, 16<<10, "text/markdown")
	if err != nil {
		t.Fatalf("InspectReportArtifact() error = %v", err)
	}
	if evidence.Size != int64(len(contents)) || evidence.MediaType != "text/markdown" ||
		evidence.ContentHash != "a5f88e36b0baf44a19e86dc43fa018b055a790b7754f76f2470dff3bc4380506" {
		t.Fatalf("InspectReportArtifact() = %#v", evidence)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("artifact was changed: %v", err)
	}
}

func TestReportArtifactInspector_RefusesSymlinkDirectoryOversizeAndChangedFile(t *testing.T) {
	root := t.TempDir()
	regular := filepath.Join(root, "regular.md")
	if err := os.WriteFile(regular, []byte("report"), 0o600); err != nil {
		t.Fatalf("write regular report: %v", err)
	}
	linked := filepath.Join(root, "linked.md")
	if err := os.Symlink(regular, linked); err != nil {
		t.Fatalf("link report: %v", err)
	}
	oversize := filepath.Join(root, "oversize.md")
	if err := os.WriteFile(oversize, []byte(strings.Repeat("x", 17)), 0o600); err != nil {
		t.Fatalf("write oversized report: %v", err)
	}
	for _, test := range []struct {
		name  string
		path  string
		limit int64
	}{
		{name: "symlink", path: linked, limit: 16},
		{name: "directory", path: root, limit: 16},
		{name: "oversize", path: oversize, limit: 16},
		{name: "relative", path: "report.md", limit: 16},
		{name: "zero limit", path: regular, limit: 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := InspectReportArtifact(context.Background(), test.path, test.limit, "text/markdown"); err == nil {
				t.Fatal("InspectReportArtifact() error = nil")
			}
		})
	}
	dependencies := reportArtifactDependencies{
		lstat: os.Lstat,
		open: func(path string) (*os.File, error) {
			file, err := os.Open(path)
			if err == nil {
				if writeErr := os.WriteFile(path, []byte("changed"), 0o600); writeErr != nil {
					t.Fatalf("change report after open: %v", writeErr)
				}
			}
			return file, err
		},
	}
	if _, err := inspectReportArtifact(context.Background(), regular, 16, "text/markdown", dependencies); err == nil {
		t.Fatal("inspectReportArtifact(changed identity) error = nil")
	}
}
