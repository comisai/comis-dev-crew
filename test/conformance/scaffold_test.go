package conformance_test

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestProtocolFoundation_BeforeHandoffContainsNoPrivateComisDTOs(t *testing.T) {
	_, sourceFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve conformance test path")
	}
	protocolRoot := filepath.Clean(filepath.Join(filepath.Dir(sourceFile), "..", "..", "protocol", "comis"))
	entries, err := os.ReadDir(protocolRoot)
	if err != nil {
		t.Fatalf("read Comis protocol handoff directory: %v", err)
	}
	if len(entries) != 1 || entries[0].Name() != "README.md" {
		t.Fatalf("uncommitted Comis protocol handoff must contain only README.md, got %v", entryNames(entries))
	}
}

func entryNames(entries []os.DirEntry) []string {
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	return names
}
