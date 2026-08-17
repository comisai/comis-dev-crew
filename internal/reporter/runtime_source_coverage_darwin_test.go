//go:build darwin

package reporter

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/unix"
)

func TestDarwinSourceIdentityUsesVolumeDescriptorPaths(t *testing.T) {
	root := boundaryRuntimeDirectory(t)
	pinned, err := pinRuntimeMountDirectory(root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = pinned.close() })
	descriptor := pinned.descriptors[len(pinned.descriptors)-1]
	identity := pinned.identities[len(pinned.identities)-1]
	path, err := runtimePinnedDirectoryPath(descriptor, identity)
	if err != nil || path == "" {
		t.Fatalf("runtimePinnedDirectoryPath() = %q, %v", path, err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("volume descriptor path is unavailable: %v", err)
	}
	if mountID, err := runtimeDescriptorMountID(descriptor); mountID != 0 || !errors.Is(err, unix.ENOTSUP) {
		t.Fatalf("runtimeDescriptorMountID() = %d, %v", mountID, err)
	}

	regular := filepath.Join(root, "regular")
	if err := os.WriteFile(regular, []byte("regular"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := runtimePinnedSocketIdentity(descriptor, "regular"); err != nil {
		t.Fatalf("runtimePinnedSocketIdentity(regular) error = %v", err)
	}
	if _, err := runtimePinnedSocketIdentity(descriptor, "missing"); !errors.Is(err, unix.ENOENT) {
		t.Fatalf("runtimePinnedSocketIdentity(missing) error = %v", err)
	}
}
