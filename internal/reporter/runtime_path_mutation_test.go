package reporter

import (
	"errors"
	"net"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/unix"
)

func TestQuarantineRuntimePathPreservesExactTargetOnRollbackFailure(t *testing.T) {
	root := boundaryRuntimeDirectory(t)
	taskPath := filepath.Join(root, "task-runtime-exact")
	if err := os.Mkdir(taskPath, 0o700); err != nil {
		t.Fatal(err)
	}
	expected := runtimePathTestIdentity(t, taskPath)
	directory := runtimePathTestDirectoryDescriptor(t, root)
	err := quarantineRuntimePath(
		directory, filepath.Base(taskPath), expected, RuntimePathDirectory, 0o700,
		func(RuntimeSocketIdentity) error { return errors.New("force quarantine rollback") },
	)
	if !errors.Is(err, ErrRuntimePathIdentity) {
		t.Fatalf("quarantineRuntimePath(exact rollback) error = %v", err)
	}
	isolationName := runtimePathQuarantineName(filepath.Base(taskPath), expected, RuntimePathDirectory, 0o700)
	isolationTarget := filepath.Join(root, isolationName, runtimePathIsolationTarget)
	current, statErr := os.Lstat(isolationTarget)
	if statErr != nil || !current.IsDir() {
		t.Fatalf("isolated exact target = %#v, %v", current, statErr)
	}
	if _, statErr := os.Lstat(taskPath); !os.IsNotExist(statErr) {
		t.Fatalf("authoritative task path error = %v, want absent", statErr)
	}
}

func TestQuarantineRuntimePathDoesNotRestoreReplacementFromIsolation(t *testing.T) {
	root := boundaryRuntimeDirectory(t)
	taskPath := filepath.Join(root, "task-runtime")
	if err := os.Mkdir(taskPath, 0o700); err != nil {
		t.Fatal(err)
	}
	expected := runtimePathTestIdentity(t, taskPath)
	directory := runtimePathTestDirectoryDescriptor(t, root)
	isolationName := runtimePathQuarantineName(filepath.Base(taskPath), expected, RuntimePathDirectory, 0o700)
	isolationRoot := filepath.Join(root, isolationName)
	isolationTarget := filepath.Join(isolationRoot, runtimePathIsolationTarget)
	preservedOriginal := filepath.Join(isolationRoot, "preserved-original")
	var replacementInfo os.FileInfo
	err := quarantineRuntimePath(
		directory, filepath.Base(taskPath), expected, RuntimePathDirectory, 0o700,
		func(RuntimeSocketIdentity) error {
			if err := os.Rename(isolationTarget, preservedOriginal); err != nil {
				return err
			}
			if err := os.Mkdir(isolationTarget, 0o700); err != nil {
				return err
			}
			var err error
			replacementInfo, err = os.Lstat(isolationTarget)
			if err != nil {
				return err
			}
			return errors.New("force quarantine rollback")
		},
	)
	if !errors.Is(err, ErrRuntimePathIdentity) {
		t.Fatalf("quarantineRuntimePath(ambiguous rollback) error = %v", err)
	}
	current, statErr := os.Lstat(isolationTarget)
	if statErr != nil || replacementInfo == nil || !os.SameFile(current, replacementInfo) {
		t.Fatalf("isolated replacement mapping = %#v, %v", current, statErr)
	}
	if current, statErr := os.Lstat(preservedOriginal); statErr != nil || !current.IsDir() {
		t.Fatalf("preserved original mapping = %#v, %v", current, statErr)
	}
	if _, statErr := os.Lstat(taskPath); !os.IsNotExist(statErr) {
		t.Fatalf("authoritative task path error = %v, want absent", statErr)
	}
}

func TestQuarantineRuntimePathPreservesReplacementRacedAfterPin(t *testing.T) {
	root := boundaryRuntimeDirectory(t)
	socketPath := filepath.Join(root, "attachment.sock")
	savedPath := filepath.Join(root, "attachment.saved")
	original := listenRuntimeQuarantineSocket(t, socketPath)
	expected := runtimePathTestIdentity(t, socketPath)
	directory := runtimePathTestDirectoryDescriptor(t, root)
	var replacement *net.UnixListener
	var replacementInfo os.FileInfo
	err := quarantineRuntimePathWithHooks(
		directory, filepath.Base(socketPath), expected, RuntimePathSocket, 0o600,
		runtimePathMutationHooks{afterPin: func() error {
			if err := os.Rename(socketPath, savedPath); err != nil {
				return err
			}
			replacement = listenRuntimeQuarantineSocket(t, socketPath)
			var err error
			replacementInfo, err = os.Lstat(socketPath)
			return err
		}}, nil,
	)
	if !errors.Is(err, ErrRuntimePathIdentity) {
		t.Fatalf("quarantineRuntimePathWithHooks(raced replacement) error = %v", err)
	}
	isolationName := runtimePathQuarantineName(filepath.Base(socketPath), expected, RuntimePathSocket, 0o600)
	current, statErr := os.Lstat(filepath.Join(root, isolationName, runtimePathIsolationTarget))
	if statErr != nil || replacementInfo == nil || !os.SameFile(current, replacementInfo) {
		t.Fatalf("isolated replacement mapping = %#v, %v", current, statErr)
	}
	if _, statErr := os.Lstat(socketPath); !os.IsNotExist(statErr) {
		t.Fatalf("authoritative socket path error = %v, want absent", statErr)
	}
	_ = original.Close()
	if replacement != nil {
		_ = replacement.Close()
	}
}

func TestPublishRuntimePathPreservesRacedMappings(t *testing.T) {
	root := boundaryRuntimeDirectory(t)
	temporary := filepath.Join(root, "record.new")
	destination := filepath.Join(root, "record")
	saved := filepath.Join(root, "record.saved")
	if err := os.WriteFile(temporary, []byte("prepared"), 0o600); err != nil {
		t.Fatal(err)
	}
	expected := runtimePathTestIdentity(t, temporary)
	directory := runtimePathTestDirectoryDescriptor(t, root)
	err := publishRuntimePath(
		directory, filepath.Base(temporary), filepath.Base(destination), expected, 0o600,
		func() error {
			if err := os.Rename(temporary, saved); err != nil {
				return err
			}
			return os.WriteFile(temporary, []byte("replacement"), 0o600)
		},
	)
	if !errors.Is(err, ErrRuntimePathIdentity) {
		t.Fatalf("publishRuntimePath(raced source) error = %v", err)
	}
	contents, readErr := os.ReadFile(destination)
	if readErr != nil || string(contents) != "replacement" {
		t.Fatalf("preserved destination = %q, %v", contents, readErr)
	}
	if _, err := os.Lstat(temporary); !os.IsNotExist(err) {
		t.Fatalf("source after ambiguous publication error = %v", err)
	}
	if contents, err := os.ReadFile(saved); err != nil || string(contents) != "prepared" {
		t.Fatalf("preserved prepared source = %q, %v", contents, err)
	}
}

func TestReplaceRuntimePathPreservesAmbiguousExchangedMappings(t *testing.T) {
	root := boundaryRuntimeDirectory(t)
	temporary := filepath.Join(root, "record.new")
	destination := filepath.Join(root, "record")
	saved := filepath.Join(root, "record.saved")
	if err := os.WriteFile(temporary, []byte("prepared"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(destination, []byte("prior"), 0o600); err != nil {
		t.Fatal(err)
	}
	temporaryIdentity := runtimePathTestIdentity(t, temporary)
	destinationIdentity := runtimePathTestIdentity(t, destination)
	directory := runtimePathTestDirectoryDescriptor(t, root)
	err := replaceRuntimePath(
		directory, filepath.Base(temporary), filepath.Base(destination),
		temporaryIdentity, destinationIdentity, 0o600,
		func() error {
			if err := os.Rename(temporary, saved); err != nil {
				return err
			}
			return os.WriteFile(temporary, []byte("replacement"), 0o600)
		},
	)
	if !errors.Is(err, ErrRuntimePathIdentity) {
		t.Fatalf("replaceRuntimePath(raced source) error = %v", err)
	}
	temporaryContents, temporaryErr := os.ReadFile(temporary)
	destinationContents, destinationErr := os.ReadFile(destination)
	if temporaryErr != nil || destinationErr != nil || string(temporaryContents) != "prior" ||
		string(destinationContents) != "replacement" {
		t.Fatalf("preserved mappings = %q/%q, %v/%v", temporaryContents, destinationContents, temporaryErr, destinationErr)
	}
	if contents, err := os.ReadFile(saved); err != nil || string(contents) != "prepared" {
		t.Fatalf("preserved prepared source = %q, %v", contents, err)
	}
}

func TestMovedRuntimePathFailurePreservesExactMapping(t *testing.T) {
	root := boundaryRuntimeDirectory(t)
	temporary := filepath.Join(root, "record.new")
	destination := filepath.Join(root, "record")
	if err := os.WriteFile(temporary, []byte("prepared"), 0o600); err != nil {
		t.Fatal(err)
	}
	expected := runtimePathTestIdentity(t, temporary)
	directory := runtimePathTestDirectoryDescriptor(t, root)
	targetDescriptor, err := pinExpectedRuntimePath(
		directory, filepath.Base(temporary), expected, RuntimePathRegular, 0o600,
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := renameRuntimePathNoReplace(directory, filepath.Base(temporary), filepath.Base(destination)); err != nil {
		t.Fatal(err)
	}
	err = preserveMovedRuntimePathFailure(
		directory, targetDescriptor, RuntimePathRegular, errors.New("force publication rollback"),
	)
	if !errors.Is(err, ErrRuntimePathIdentity) {
		t.Fatalf("preserveMovedRuntimePathFailure(exact mapping) error = %v", err)
	}
	if contents, err := os.ReadFile(destination); err != nil || string(contents) != "prepared" {
		t.Fatalf("preserved destination = %q, %v", contents, err)
	}
	if _, err := os.Lstat(temporary); !os.IsNotExist(err) {
		t.Fatalf("temporary after failed rollback error = %v", err)
	}
}

func TestRuntimePathExchangeFailurePreservesExactMappings(t *testing.T) {
	root := boundaryRuntimeDirectory(t)
	temporary := filepath.Join(root, "record.new")
	destination := filepath.Join(root, "record")
	if err := os.WriteFile(temporary, []byte("prepared"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(destination, []byte("prior"), 0o600); err != nil {
		t.Fatal(err)
	}
	directory := runtimePathTestDirectoryDescriptor(t, root)
	temporaryDescriptor, err := pinExpectedRuntimePath(
		directory, filepath.Base(temporary), runtimePathTestIdentity(t, temporary), RuntimePathRegular, 0o600,
	)
	if err != nil {
		t.Fatal(err)
	}
	destinationDescriptor, err := pinExpectedRuntimePath(
		directory, filepath.Base(destination), runtimePathTestIdentity(t, destination), RuntimePathRegular, 0o600,
	)
	if err != nil {
		_ = closeRuntimeRemovalPin(temporaryDescriptor)
		t.Fatal(err)
	}
	if err := exchangeRuntimePaths(directory, filepath.Base(temporary), filepath.Base(destination)); err != nil {
		_ = closeRuntimeRemovalPin(temporaryDescriptor)
		_ = closeRuntimeRemovalPin(destinationDescriptor)
		t.Fatal(err)
	}
	err = preserveRuntimePathExchangeFailure(
		directory, temporaryDescriptor, destinationDescriptor, errors.New("force exchange rollback"),
	)
	if !errors.Is(err, ErrRuntimePathIdentity) {
		t.Fatalf("preserveRuntimePathExchangeFailure(exact mappings) error = %v", err)
	}
	temporaryContents, temporaryErr := os.ReadFile(temporary)
	destinationContents, destinationErr := os.ReadFile(destination)
	if temporaryErr != nil || destinationErr != nil || string(temporaryContents) != "prior" ||
		string(destinationContents) != "prepared" {
		t.Fatalf("preserved exchange = %q/%q, %v/%v", temporaryContents, destinationContents, temporaryErr, destinationErr)
	}
}

func runtimePathTestDirectoryDescriptor(t *testing.T, path string) int {
	t.Helper()
	descriptor, err := unix.Open(path, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = unix.Close(descriptor) })
	return descriptor
}

func runtimePathTestIdentity(t *testing.T, path string) RuntimeSocketIdentity {
	t.Helper()
	var stat unix.Stat_t
	if err := unix.Lstat(path, &stat); err != nil {
		t.Fatal(err)
	}
	identity, err := runtimeSocketStatIdentity(stat)
	if err != nil {
		t.Fatal(err)
	}
	return identity
}
