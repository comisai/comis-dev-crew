package service

import (
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/unix"
)

func TestRuntimeAttachmentDirectoryEmptyStaysStableAcrossRepeatedReads(t *testing.T) {
	root := shortTempDir(t)
	occupied := filepath.Join(root, "occupied")
	if err := os.Mkdir(occupied, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(occupied, "unexpected"), []byte("content"), 0o600); err != nil {
		t.Fatal(err)
	}
	descriptor, err := unix.Open(occupied, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = unix.Close(descriptor) })
	for attempt := range 3 {
		empty, err := runtimeAttachmentDirectoryEmpty(descriptor)
		if err != nil {
			t.Fatalf("runtimeAttachmentDirectoryEmpty(attempt %d) error = %v", attempt, err)
		}
		if empty {
			t.Fatalf("runtimeAttachmentDirectoryEmpty(attempt %d) reported an occupied directory as empty", attempt)
		}
	}

	vacant := filepath.Join(root, "vacant")
	if err := os.Mkdir(vacant, 0o700); err != nil {
		t.Fatal(err)
	}
	vacantDescriptor, err := unix.Open(vacant, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = unix.Close(vacantDescriptor) })
	for attempt := range 2 {
		empty, err := runtimeAttachmentDirectoryEmpty(vacantDescriptor)
		if err != nil || !empty {
			t.Fatalf("runtimeAttachmentDirectoryEmpty(vacant attempt %d) = %v, %v", attempt, empty, err)
		}
	}
}
