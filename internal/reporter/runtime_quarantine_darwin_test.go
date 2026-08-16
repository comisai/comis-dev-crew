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
	quarantinePath := filepath.Join(root, quarantine)
	if err := os.Mkdir(quarantinePath, 0o700); err != nil {
		t.Fatal(err)
	}
	isolation := runtimePathTestDirectoryDescriptor(t, quarantinePath)
	anchor := runtimeRemovalAnchorName(name, expected)
	if err := unix.Linkat(directory, name, isolation, anchor, 0); err != nil {
		t.Fatal(err)
	}
	if err := renameRuntimePathNoReplaceBetween(directory, name, isolation, runtimePathIsolationTarget); err != nil {
		t.Fatal(err)
	}
	if err := unix.Fsync(directory); err != nil {
		t.Fatal(err)
	}
	if err := QuarantineRuntimePath(directory, name, expected, RuntimePathSocket, 0o600); err != nil {
		t.Fatalf("QuarantineRuntimePath(stranded Darwin anchor) error = %v", err)
	}
	if info, err := os.Lstat(filepath.Join(quarantinePath, runtimePathIsolationTarget)); err != nil || info.Mode()&os.ModeSocket == 0 {
		t.Fatalf("Darwin isolated target = %#v, %v", info, err)
	}
	if info, err := os.Lstat(filepath.Join(quarantinePath, anchor)); err != nil || info.Mode()&os.ModeSocket == 0 {
		t.Fatalf("Darwin isolated anchor = %#v, %v", info, err)
	}
}

func TestQuarantineRuntimePathReconcilesDarwinAnchorAfterTargetRemoval(t *testing.T) {
	root := boundaryRuntimeDirectory(t)
	socketPath := filepath.Join(root, "attachment.sock")
	listener := listenRuntimeQuarantineSocket(t, socketPath)
	t.Cleanup(func() { _ = listener.Close() })
	expected := runtimePathTestIdentity(t, socketPath)
	directory := runtimePathTestDirectoryDescriptor(t, root)
	name := filepath.Base(socketPath)
	quarantine := runtimePathQuarantineName(name, expected, RuntimePathSocket, 0o600)
	quarantinePath := filepath.Join(root, quarantine)
	if err := os.Mkdir(quarantinePath, 0o700); err != nil {
		t.Fatal(err)
	}
	isolation := runtimePathTestDirectoryDescriptor(t, quarantinePath)
	anchor := runtimeRemovalAnchorName(name, expected)
	if err := unix.Linkat(directory, name, isolation, anchor, 0); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(socketPath); err != nil {
		t.Fatal(err)
	}
	if err := QuarantineRuntimePath(directory, name, expected, RuntimePathSocket, 0o600); err != nil {
		t.Fatalf("QuarantineRuntimePath(stranded Darwin anchor only) error = %v", err)
	}
	if info, err := os.Lstat(filepath.Join(quarantinePath, anchor)); err != nil || info.Mode()&os.ModeSocket == 0 {
		t.Fatalf("Darwin anchor-only isolation = %#v, %v", info, err)
	}
}
