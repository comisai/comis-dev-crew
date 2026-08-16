package reporter

import (
	"errors"
	"net"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/unix"
)

func TestQuarantineRuntimePathRestoresReplacementRacedAfterPin(t *testing.T) {
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
	current, statErr := os.Lstat(socketPath)
	if statErr != nil || replacementInfo == nil || !os.SameFile(current, replacementInfo) {
		t.Fatalf("raced replacement mapping = %#v, %v", current, statErr)
	}
	_ = original.Close()
	if replacement != nil {
		_ = replacement.Close()
	}
}

func TestPublishRuntimePathRestoresRacedSourceMapping(t *testing.T) {
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
	contents, readErr := os.ReadFile(temporary)
	if readErr != nil || string(contents) != "replacement" {
		t.Fatalf("restored source = %q, %v", contents, readErr)
	}
	if _, err := os.Lstat(destination); !os.IsNotExist(err) {
		t.Fatalf("destination after failed publication error = %v", err)
	}
}

func TestReplaceRuntimePathRestoresBothRacedMappings(t *testing.T) {
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
	if temporaryErr != nil || destinationErr != nil || string(temporaryContents) != "replacement" ||
		string(destinationContents) != "prior" {
		t.Fatalf("restored mappings = %q/%q, %v/%v", temporaryContents, destinationContents, temporaryErr, destinationErr)
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
