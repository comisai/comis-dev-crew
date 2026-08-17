package reporter

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRemoveRuntimeSocketAdoptsUnpinnedIdentityFromTheOriginalNode(t *testing.T) {
	root := boundaryRuntimeDirectory(t)
	socketPath := filepath.Join(root, "attachment.sock")
	listenBoundarySocket(t, socketPath, 0o600)
	original, err := os.Lstat(socketPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := removeRuntimeSocket(socketPath, original, RuntimeSocketIdentity{}); err != nil {
		t.Fatalf("removeRuntimeSocket(unpinned identity) error = %v", err)
	}
	if _, statErr := os.Lstat(socketPath); !os.IsNotExist(statErr) {
		t.Fatalf("socket after adopted removal error = %v, want absent", statErr)
	}
}

func TestRemoveRuntimeSocketAcceptsAlreadyAbsentSocket(t *testing.T) {
	root := boundaryRuntimeDirectory(t)
	socketPath := filepath.Join(root, "attachment.sock")
	listenBoundarySocket(t, socketPath, 0o600)
	original, err := os.Lstat(socketPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(socketPath); err != nil {
		t.Fatal(err)
	}
	if err := removeRuntimeSocket(socketPath, original, RuntimeSocketIdentity{}); err != nil {
		t.Fatalf("removeRuntimeSocket(absent socket) error = %v", err)
	}
}

func TestCaptureRuntimeSocketIdentityRejectsAbsentSocket(t *testing.T) {
	root := boundaryRuntimeDirectory(t)
	if _, err := captureRuntimeSocketIdentity(filepath.Join(root, "attachment.sock"), fakeRuntimeFileInfo{}); err == nil {
		t.Fatal("captureRuntimeSocketIdentity accepted an absent socket")
	}
}
