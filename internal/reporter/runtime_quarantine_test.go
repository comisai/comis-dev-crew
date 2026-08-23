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

func TestQuarantineRuntimePathRetiresPinnedTargetAfterIdentityVerification(t *testing.T) {
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
	if _, statErr := os.Lstat(filepath.Join(root, isolationName)); !os.IsNotExist(statErr) {
		t.Fatalf("retired socket isolation error = %v, want absent", statErr)
	}
	if _, statErr := os.Lstat(socketPath); !os.IsNotExist(statErr) {
		t.Fatalf("authoritative socket path error = %v, want absent", statErr)
	}
}

func TestQuarantineRuntimePathRetiresSuccessfulIsolationNamespace(t *testing.T) {
	root := boundaryRuntimeDirectory(t)
	target := filepath.Join(root, "record")
	if err := os.WriteFile(target, []byte("retire"), 0o600); err != nil {
		t.Fatal(err)
	}
	expected := runtimePathTestIdentity(t, target)
	directory := runtimePathTestDirectoryDescriptor(t, root)
	defer unix.Close(directory)
	isolationName := runtimePathQuarantineName("record", expected, RuntimePathRegular, 0o600)
	if err := QuarantineRuntimePath(directory, "record", expected, RuntimePathRegular, 0o600); err != nil {
		t.Fatalf("QuarantineRuntimePath(successful retirement) error = %v", err)
	}
	if _, err := os.Lstat(target); !os.IsNotExist(err) {
		t.Fatalf("retired target error = %v, want absent", err)
	}
	if _, err := os.Lstat(filepath.Join(root, isolationName)); !os.IsNotExist(err) {
		t.Fatalf("successful isolation namespace error = %v, want absent", err)
	}
}

func TestQuarantineRuntimePathDoesNotRetainIsolationForMissingTarget(t *testing.T) {
	root := boundaryRuntimeDirectory(t)
	target := filepath.Join(root, "record")
	if err := os.WriteFile(target, []byte("removed"), 0o600); err != nil {
		t.Fatal(err)
	}
	expected := runtimePathTestIdentity(t, target)
	if err := os.Remove(target); err != nil {
		t.Fatal(err)
	}
	directory := runtimePathTestDirectoryDescriptor(t, root)
	defer unix.Close(directory)
	isolationName := runtimePathQuarantineName("record", expected, RuntimePathRegular, 0o600)
	err := QuarantineRuntimePath(directory, "record", expected, RuntimePathRegular, 0o600)
	if !errors.Is(err, ErrRuntimePathMissing) {
		t.Fatalf("QuarantineRuntimePath(missing target) error = %v", err)
	}
	if _, err := os.Lstat(filepath.Join(root, isolationName)); !os.IsNotExist(err) {
		t.Fatalf("missing-target isolation error = %v, want absent", err)
	}
}

func TestQuarantineRuntimePathUsesPrecreatedEmptyIsolation(t *testing.T) {
	root := boundaryRuntimeDirectory(t)
	target := filepath.Join(root, "record")
	if err := os.WriteFile(target, []byte("retire"), 0o600); err != nil {
		t.Fatal(err)
	}
	expected := runtimePathTestIdentity(t, target)
	isolationName := runtimePathQuarantineName("record", expected, RuntimePathRegular, 0o600)
	if err := os.Mkdir(filepath.Join(root, isolationName), 0o700); err != nil {
		t.Fatal(err)
	}
	directory := runtimePathTestDirectoryDescriptor(t, root)
	defer unix.Close(directory)
	if err := QuarantineRuntimePath(directory, "record", expected, RuntimePathRegular, 0o600); err != nil {
		t.Fatalf("QuarantineRuntimePath(precreated isolation) error = %v", err)
	}
	if _, err := os.Lstat(target); !os.IsNotExist(err) {
		t.Fatalf("precreated-isolation target error = %v, want absent", err)
	}
	if _, err := os.Lstat(filepath.Join(root, isolationName)); !os.IsNotExist(err) {
		t.Fatalf("precreated isolation error = %v, want absent", err)
	}
}

func TestReconcileIsolatedRuntimePathPreservesUnexpectedEntryWithoutTarget(t *testing.T) {
	root := boundaryRuntimeDirectory(t)
	original := filepath.Join(root, "record")
	if err := os.WriteFile(original, []byte("removed"), 0o600); err != nil {
		t.Fatal(err)
	}
	expected := runtimePathTestIdentity(t, original)
	if err := os.Remove(original); err != nil {
		t.Fatal(err)
	}
	isolationName := "isolation-unexpected"
	isolationRoot := filepath.Join(root, isolationName)
	if err := os.Mkdir(isolationRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	unexpected := filepath.Join(isolationRoot, "unexpected")
	if err := os.WriteFile(unexpected, []byte("preserve"), 0o600); err != nil {
		t.Fatal(err)
	}
	directory := runtimePathTestDirectoryDescriptor(t, root)
	defer unix.Close(directory)
	isolation := runtimePathTestDirectoryDescriptor(t, isolationRoot)
	reconciled, err := reconcileIsolatedRuntimePath(
		directory, isolation, isolationName, filepath.Base(original), expected, RuntimePathRegular, 0o600,
	)
	if !reconciled || !errors.Is(err, ErrRuntimePathIdentity) {
		t.Fatalf("reconcileIsolatedRuntimePath(unexpected entry) = %t, %v", reconciled, err)
	}
	if contents, err := os.ReadFile(unexpected); err != nil || string(contents) != "preserve" {
		t.Fatalf("unexpected isolation entry = %q, %v", contents, err)
	}
}

func TestQuarantineRuntimePathRetiresOnlyAuthorizedRegularLink(t *testing.T) {
	root := boundaryRuntimeDirectory(t)
	anchor := filepath.Join(root, "generation-anchor")
	linked := filepath.Join(root, "generation-link")
	if err := os.WriteFile(anchor, []byte("generation"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(anchor, linked); err != nil {
		t.Fatal(err)
	}
	expected := runtimePathTestIdentity(t, linked)
	directory := runtimePathTestDirectoryDescriptor(t, root)
	defer unix.Close(directory)
	if err := QuarantineRuntimePath(
		directory, filepath.Base(linked), expected, RuntimePathLinkedRegular, 0o600,
	); err != nil {
		t.Fatalf("QuarantineRuntimePath(linked retirement) error = %v", err)
	}
	if _, err := os.Lstat(linked); !os.IsNotExist(err) {
		t.Fatalf("retired regular link error = %v, want absent", err)
	}
	if contents, err := os.ReadFile(anchor); err != nil || string(contents) != "generation" {
		t.Fatalf("generation anchor = %q, %v", contents, err)
	}
}

func TestQuarantineRuntimePathRejectsSingleLinkAsLinkedAuthority(t *testing.T) {
	root := boundaryRuntimeDirectory(t)
	target := filepath.Join(root, "generation-link")
	if err := os.WriteFile(target, []byte("preserve"), 0o600); err != nil {
		t.Fatal(err)
	}
	expected := runtimePathTestIdentity(t, target)
	directory := runtimePathTestDirectoryDescriptor(t, root)
	defer unix.Close(directory)
	if err := QuarantineRuntimePath(
		directory, filepath.Base(target), expected, RuntimePathLinkedRegular, 0o600,
	); !errors.Is(err, ErrRuntimePathIdentity) {
		t.Fatalf("QuarantineRuntimePath(single link authority) error = %v", err)
	}
	if contents, err := os.ReadFile(target); err != nil || string(contents) != "preserve" {
		t.Fatalf("single-link target = %q, %v", contents, err)
	}
}

func TestQuarantineRuntimeDirectoryPreservesUnexpectedContents(t *testing.T) {
	root := boundaryRuntimeDirectory(t)
	taskPath := filepath.Join(root, "task-runtime-unexpected")
	if err := os.Mkdir(taskPath, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(taskPath, "unexpected"), []byte("preserve"), 0o600); err != nil {
		t.Fatal(err)
	}
	expected := runtimePathTestIdentity(t, taskPath)
	directory := runtimePathTestDirectoryDescriptor(t, root)
	defer unix.Close(directory)
	isolationName := runtimePathQuarantineName(
		filepath.Base(taskPath), expected, RuntimePathDirectory, 0o700,
	)
	err := QuarantineRuntimePath(
		directory, filepath.Base(taskPath), expected, RuntimePathDirectory, 0o700,
	)
	if !errors.Is(err, ErrRuntimePathIdentity) {
		t.Fatalf("QuarantineRuntimePath(non-empty directory) error = %v", err)
	}
	preserved := filepath.Join(root, isolationName, runtimePathIsolationTarget, "unexpected")
	if contents, readErr := os.ReadFile(preserved); readErr != nil || string(contents) != "preserve" {
		t.Fatalf("unexpected isolated content = %q, %v", contents, readErr)
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
	if err := syncRuntimeDirectory(directoryDescriptor); err != nil {
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
	if _, err := os.Lstat(quarantinePath); !os.IsNotExist(err) {
		t.Fatalf("reconciled isolation error = %v, want absent", err)
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
