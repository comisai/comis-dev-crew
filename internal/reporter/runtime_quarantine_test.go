package reporter

import (
	"errors"
	"net"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/unix"
)

func TestQuarantineRuntimePathPreservesConcurrentReplacement(t *testing.T) {
	root := boundaryRuntimeDirectory(t)
	socketPath := filepath.Join(root, "attachment.sock")
	original := listenRuntimeQuarantineSocket(t, socketPath)
	originalInfo, err := os.Lstat(socketPath)
	if err != nil {
		t.Fatal(err)
	}
	expected, err := captureRuntimeSocketIdentity(socketPath, originalInfo)
	if err != nil {
		t.Fatal(err)
	}
	pinned, err := pinRuntimeMountDirectory(root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = pinned.close() })
	var replacement *net.UnixListener
	var replacementInfo os.FileInfo
	err = quarantineRuntimePath(
		pinned.descriptors[len(pinned.descriptors)-1], filepath.Base(socketPath), expected,
		RuntimePathSocket, 0o600,
		func(pinnedIdentity RuntimeSocketIdentity) error {
			if !runtimeSocketIdentityMatches(pinnedIdentity, expected) {
				return errors.New("quarantine target was not pinned")
			}
			replacement = listenRuntimeQuarantineSocket(t, socketPath)
			var statErr error
			replacementInfo, statErr = os.Lstat(socketPath)
			return statErr
		},
	)
	if err != nil {
		t.Fatalf("quarantineRuntimePath(concurrent replacement) error = %v", err)
	}
	t.Cleanup(func() {
		_ = original.Close()
		if replacement != nil {
			_ = replacement.Close()
		}
		_ = os.Remove(socketPath)
	})
	current, err := os.Lstat(socketPath)
	if err != nil || replacementInfo == nil || !os.SameFile(current, replacementInfo) {
		t.Fatalf("concurrent replacement was not preserved: %#v, %v", current, err)
	}
}

func TestQuarantineRuntimePathKeepsPinnedTargetOutOfMutableUnlink(t *testing.T) {
	root := boundaryRuntimeDirectory(t)
	socketPath := filepath.Join(root, "attachment.sock")
	original := listenRuntimeQuarantineSocket(t, socketPath)
	t.Cleanup(func() { _ = original.Close() })
	expected := runtimePathTestIdentity(t, socketPath)
	directory := runtimePathTestDirectoryDescriptor(t, root)
	defer unix.Close(directory)
	err := QuarantineRuntimePath(directory, filepath.Base(socketPath), expected, RuntimePathSocket, 0o600)
	if err != nil {
		t.Fatalf("QuarantineRuntimePath(preserved target) error = %v", err)
	}
	isolationName := runtimePathQuarantineName(filepath.Base(socketPath), expected, RuntimePathSocket, 0o600)
	isolated := filepath.Join(root, isolationName, runtimePathIsolationTarget)
	current, statErr := os.Lstat(isolated)
	if statErr != nil || current.Mode()&os.ModeSocket == 0 {
		t.Fatalf("pinned target was not preserved in quarantine: %#v, %v", current, statErr)
	}
	if _, statErr := os.Lstat(socketPath); !os.IsNotExist(statErr) {
		t.Fatalf("authoritative socket path error = %v, want absent", statErr)
	}
}

func TestQuarantineRuntimePathRejectsSharedRemovalNamespace(t *testing.T) {
	root := boundaryRuntimeDirectory(t)
	socketPath := filepath.Join(root, "attachment.sock")
	listener := listenRuntimeQuarantineSocket(t, socketPath)
	defer listener.Close()
	expected := runtimePathTestIdentity(t, socketPath)
	if err := os.Chmod(root, 0o755); err != nil {
		t.Fatal(err)
	}
	directory := runtimePathTestDirectoryDescriptor(t, root)
	defer unix.Close(directory)
	if err := QuarantineRuntimePath(
		directory, filepath.Base(socketPath), expected, RuntimePathSocket, 0o600,
	); err == nil {
		t.Fatal("QuarantineRuntimePath accepted a shared removal namespace")
	}
	if info, err := os.Lstat(socketPath); err != nil || info.Mode()&os.ModeSocket == 0 {
		t.Fatalf("shared namespace target was not preserved: %#v, %v", info, err)
	}
}

func TestQuarantineRuntimePathRestoresIdentityMismatch(t *testing.T) {
	root := boundaryRuntimeDirectory(t)
	socketPath := filepath.Join(root, "attachment.sock")
	original := listenRuntimeQuarantineSocket(t, socketPath)
	originalInfo, err := os.Lstat(socketPath)
	if err != nil {
		t.Fatal(err)
	}
	expected, err := captureRuntimeSocketIdentity(socketPath, originalInfo)
	if err != nil {
		t.Fatal(err)
	}
	if err := original.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(socketPath); err != nil {
		t.Fatal(err)
	}
	replacement := listenRuntimeQuarantineSocket(t, socketPath)
	replacementInfo, err := os.Lstat(socketPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = replacement.Close()
		_ = os.Remove(socketPath)
	})
	pinned, err := pinRuntimeMountDirectory(root)
	if err != nil {
		t.Fatal(err)
	}
	defer pinned.close()
	err = QuarantineRuntimePath(
		pinned.descriptors[len(pinned.descriptors)-1], filepath.Base(socketPath), expected,
		RuntimePathSocket, 0o600,
	)
	if !errors.Is(err, ErrRuntimePathIdentity) {
		t.Fatalf("QuarantineRuntimePath(identity mismatch) error = %v", err)
	}
	current, err := os.Lstat(socketPath)
	if err != nil || !os.SameFile(current, replacementInfo) {
		t.Fatalf("identity mismatch was not restored: %#v, %v", current, err)
	}
}

func TestQuarantineRuntimeDirectoryPreservesConcurrentReplacement(t *testing.T) {
	root := boundaryRuntimeDirectory(t)
	taskPath := filepath.Join(root, "task-runtime-quarantine")
	if err := os.Mkdir(taskPath, 0o700); err != nil {
		t.Fatal(err)
	}
	var stat unix.Stat_t
	if err := unix.Lstat(taskPath, &stat); err != nil {
		t.Fatal(err)
	}
	expected, err := runtimeSocketStatIdentity(stat)
	if err != nil {
		t.Fatal(err)
	}
	pinned, err := pinRuntimeMountDirectory(root)
	if err != nil {
		t.Fatal(err)
	}
	defer pinned.close()
	var replacementInfo os.FileInfo
	err = quarantineRuntimePath(
		pinned.descriptors[len(pinned.descriptors)-1], filepath.Base(taskPath), expected,
		RuntimePathDirectory, 0o700,
		func(RuntimeSocketIdentity) error {
			if err := os.Mkdir(taskPath, 0o700); err != nil {
				return err
			}
			var statErr error
			replacementInfo, statErr = os.Lstat(taskPath)
			return statErr
		},
	)
	if err != nil {
		t.Fatalf("quarantineRuntimePath(concurrent directory replacement) error = %v", err)
	}
	current, err := os.Lstat(taskPath)
	if err != nil || replacementInfo == nil || !os.SameFile(current, replacementInfo) {
		t.Fatalf("concurrent directory replacement was not preserved: %#v, %v", current, err)
	}
}

func TestQuarantineRuntimePathReconcilesStrandedExactIdentity(t *testing.T) {
	root := boundaryRuntimeDirectory(t)
	socketPath := filepath.Join(root, "attachment.sock")
	listener := listenRuntimeQuarantineSocket(t, socketPath)
	t.Cleanup(func() { _ = listener.Close() })
	info, err := os.Lstat(socketPath)
	if err != nil {
		t.Fatal(err)
	}
	expected, err := captureRuntimeSocketIdentity(socketPath, info)
	if err != nil {
		t.Fatal(err)
	}
	pinned, err := pinRuntimeMountDirectory(root)
	if err != nil {
		t.Fatal(err)
	}
	defer pinned.close()
	directoryDescriptor := pinned.descriptors[len(pinned.descriptors)-1]
	quarantine := runtimePathQuarantineName(filepath.Base(socketPath), expected, RuntimePathSocket, 0o600)
	quarantinePath := filepath.Join(root, quarantine)
	if err := os.Mkdir(quarantinePath, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(socketPath, filepath.Join(quarantinePath, runtimePathIsolationTarget)); err != nil {
		t.Fatal(err)
	}
	if err := unix.Fsync(directoryDescriptor); err != nil {
		t.Fatal(err)
	}
	if err := QuarantineRuntimePath(
		directoryDescriptor, filepath.Base(socketPath), expected, RuntimePathSocket, 0o600,
	); err != nil {
		t.Fatalf("QuarantineRuntimePath(stranded identity) error = %v", err)
	}
	if _, err := os.Lstat(socketPath); !os.IsNotExist(err) {
		t.Fatalf("original path after reconciliation error = %v, want not exist", err)
	}
	if info, err := os.Lstat(filepath.Join(quarantinePath, runtimePathIsolationTarget)); err != nil || info.Mode()&os.ModeSocket == 0 {
		t.Fatalf("quarantined identity after reconciliation = %#v, %v", info, err)
	}
}

func listenRuntimeQuarantineSocket(t *testing.T, path string) *net.UnixListener {
	t.Helper()
	address, err := net.ResolveUnixAddr("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	listener, err := net.ListenUnix("unix", address)
	if err != nil {
		t.Fatal(err)
	}
	listener.SetUnlinkOnClose(false)
	if err := os.Chmod(path, 0o600); err != nil {
		_ = listener.Close()
		t.Fatal(err)
	}
	return listener
}
