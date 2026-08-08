package localconfig

import (
	"path/filepath"
	"testing"
)

func TestUnder_DerivesSharedServiceAndCLIPaths(t *testing.T) {
	paths, err := Under(filepath.Join(string(filepath.Separator), "private", "config"))
	if err != nil {
		t.Fatalf("Under() error = %v", err)
	}
	if paths.Database != filepath.Join(string(filepath.Separator), "private", "config", "comis-dev-crew", "state", "devcrew.db") {
		t.Fatalf("database path = %q", paths.Database)
	}
	if paths.Socket != filepath.Join(string(filepath.Separator), "private", "config", "comis-dev-crew", "run", "devcrew.sock") {
		t.Fatalf("socket path = %q", paths.Socket)
	}
	nonCanonical := string(filepath.Separator) + "private" + string(filepath.Separator) + "nested" + string(filepath.Separator) + ".." + string(filepath.Separator) + "config"
	for _, root := range []string{"", "relative", nonCanonical} {
		if _, err := Under(root); err == nil {
			t.Fatalf("Under(%q) error = nil", root)
		}
	}
}
