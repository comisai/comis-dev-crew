//go:build darwin

package reporter

import (
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/unix"
)

func TestQuarantineRuntimePathReconcilesOriginalDarwinAnchor(t *testing.T) {
	root := boundaryRuntimeDirectory(t)
	socketPath := filepath.Join(root, "attachment.sock")
	listener := listenRuntimeQuarantineSocket(t, socketPath)
	t.Cleanup(func() { _ = listener.Close() })
	expected := runtimePathTestIdentity(t, socketPath)
	directory := runtimePathTestDirectoryDescriptor(t, root)
	name := filepath.Base(socketPath)
	quarantine := runtimePathQuarantineName(name, expected, RuntimePathSocket, 0o600)
	anchor := runtimeRemovalAnchorName(name, expected)
	if err := unix.Linkat(directory, name, directory, anchor, 0); err != nil {
		t.Fatal(err)
	}
	if err := renameRuntimePathNoReplace(directory, name, quarantine); err != nil {
		t.Fatal(err)
	}
	if err := unix.Fsync(directory); err != nil {
		t.Fatal(err)
	}
	if err := QuarantineRuntimePath(directory, name, expected, RuntimePathSocket, 0o600); err != nil {
		t.Fatalf("QuarantineRuntimePath(stranded Darwin anchor) error = %v", err)
	}
	if _, err := os.Lstat(filepath.Join(root, anchor)); !os.IsNotExist(err) {
		t.Fatalf("original Darwin anchor error = %v, want not exist", err)
	}
}
