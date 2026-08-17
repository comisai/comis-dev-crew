//go:build darwin

package reporter

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/unix"
)

func TestRuntimeSocketPinReleasesItsAnchorLink(t *testing.T) {
	root := boundaryRuntimeDirectory(t)
	socketPath := filepath.Join(root, "attachment.sock")
	listenBoundarySocket(t, socketPath, 0o600)
	expected := runtimePathTestIdentity(t, socketPath)
	directory := runtimePathTestDirectoryDescriptor(t, root)
	pin, err := pinExpectedRuntimePath(directory, "attachment.sock", expected, RuntimePathSocket, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if pin.descriptor >= 0 {
		t.Fatalf("socket pin used a descriptor anchor: %d", pin.descriptor)
	}
	anchorPath := filepath.Join(root, pin.anchor)
	if _, statErr := os.Lstat(anchorPath); statErr != nil {
		t.Fatalf("socket pin anchor error = %v", statErr)
	}
	if err := closeRuntimeRemovalPin(pin); err != nil {
		t.Fatalf("closeRuntimeRemovalPin(socket anchor) error = %v", err)
	}
	if _, statErr := os.Lstat(anchorPath); !os.IsNotExist(statErr) {
		t.Fatalf("socket pin anchor after release error = %v, want absent", statErr)
	}
	if _, statErr := os.Lstat(socketPath); statErr != nil {
		t.Fatalf("socket after pin release error = %v", statErr)
	}
}

func TestRuntimeSocketPinReportsUnusableAnchorNamespace(t *testing.T) {
	root := boundaryRuntimeDirectory(t)
	socketPath := filepath.Join(root, "attachment.sock")
	listenBoundarySocket(t, socketPath, 0o600)
	expected := runtimePathTestIdentity(t, socketPath)
	directory := runtimePathTestDirectoryDescriptor(t, root)
	if _, err := openRuntimeRemovalPath(
		directory, "attachment.sock", "attachment.sock", -1, expected, RuntimePathSocket, 0o600,
	); err == nil {
		t.Fatal("socket pin accepted an unusable anchor directory")
	}
}

func TestRuntimeSocketPinRefusesForeignAnchorOccupant(t *testing.T) {
	root := boundaryRuntimeDirectory(t)
	socketPath := filepath.Join(root, "attachment.sock")
	listenBoundarySocket(t, socketPath, 0o600)
	expected := runtimePathTestIdentity(t, socketPath)
	anchorRoot := filepath.Join(root, "anchors")
	if err := os.Mkdir(anchorRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	occupant := filepath.Join(anchorRoot, runtimeRemovalAnchorName("attachment.sock", expected))
	if err := os.WriteFile(occupant, []byte("foreign"), 0o600); err != nil {
		t.Fatal(err)
	}
	directory := runtimePathTestDirectoryDescriptor(t, root)
	anchorDescriptor := runtimePathTestDirectoryDescriptor(t, anchorRoot)
	_, err := openRuntimeRemovalPath(
		directory, "attachment.sock", "attachment.sock", anchorDescriptor, expected, RuntimePathSocket, 0o600,
	)
	if !errors.Is(err, ErrRuntimePathIdentity) {
		t.Fatalf("openRuntimeRemovalPath(foreign anchor occupant) error = %v", err)
	}
	if contents, readErr := os.ReadFile(occupant); readErr != nil || string(contents) != "foreign" {
		t.Fatalf("preserved foreign anchor occupant = %q, %v", contents, readErr)
	}
	var stat unix.Stat_t
	if err := unix.Fstatat(directory, "attachment.sock", &stat, unix.AT_SYMLINK_NOFOLLOW); err != nil {
		t.Fatalf("socket after refused anchor error = %v", err)
	}
}
