//go:build linux

package reporter

import (
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/unix"
)

func TestQuarantineRuntimePathSynchronizesThroughPinnedDirectory(t *testing.T) {
	root := t.TempDir()
	if err := os.Chmod(root, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "record")
	if err := os.WriteFile(path, []byte("record"), 0o600); err != nil {
		t.Fatal(err)
	}
	var stat unix.Stat_t
	if err := unix.Lstat(path, &stat); err != nil {
		t.Fatal(err)
	}
	identity, err := runtimeSocketStatIdentity(stat)
	if err != nil {
		t.Fatal(err)
	}
	pinned, err := pinRuntimeMountDirectory(root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = pinned.close() })
	directoryDescriptor := pinned.descriptors[len(pinned.descriptors)-1]
	if err := QuarantineRuntimePath(directoryDescriptor, "record", identity, RuntimePathRegular, 0o600); err != nil {
		t.Fatalf("QuarantineRuntimePath() error = %v", err)
	}
	if _, err := os.Lstat(path); !os.IsNotExist(err) {
		t.Fatalf("quarantined path remained: %v", err)
	}
}
