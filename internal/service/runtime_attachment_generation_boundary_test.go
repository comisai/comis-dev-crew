package service

import (
	"context"
	"errors"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

const runtimeGenerationFIFOTestRoot = "DEV_CREW_RUNTIME_GENERATION_FIFO_TEST_ROOT"

func TestPinRuntimeAttachmentGenerationRejectsFIFOWithoutBlocking(t *testing.T) {
	if runtimeRoot := os.Getenv(runtimeGenerationFIFOTestRoot); runtimeRoot != "" {
		assertRuntimeAttachmentGenerationFIFOIsUnproven(t, runtimeRoot)
		return
	}

	runtimeRoot := filepath.Join(shortTempDir(t), "runtime")
	if err := os.Mkdir(runtimeRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	var generationID [16]byte
	generationID[0] = 1
	generationRoot := filepath.Join(runtimeRoot, runtimeAttachmentGenerationName(generationID))
	if err := os.Mkdir(generationRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	anchorPath := filepath.Join(generationRoot, runtimeAttachmentGenerationLink)
	if err := unix.Mkfifo(anchorPath, 0o600); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestPinRuntimeAttachmentGenerationRejectsFIFOWithoutBlocking$")
	command.Env = append(os.Environ(), runtimeGenerationFIFOTestRoot+"="+runtimeRoot)
	output, err := command.CombinedOutput()
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		t.Fatal("runtime attachment generation pin blocked on a FIFO")
	}
	if err != nil {
		t.Fatalf("generation FIFO child error = %v, output = %q", err, output)
	}
	if info, err := os.Lstat(anchorPath); err != nil || info.Mode()&os.ModeNamedPipe == 0 {
		t.Fatalf("generation FIFO was not preserved: %#v, %v", info, err)
	}
}

func TestPinRuntimeAttachmentGenerationClassifiesSocketAsUnproven(t *testing.T) {
	runtimeRoot := filepath.Join(shortTempDir(t), "runtime")
	if err := os.Mkdir(runtimeRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	var generationID [16]byte
	generationID[0] = 2
	generationRoot := filepath.Join(runtimeRoot, runtimeAttachmentGenerationName(generationID))
	if err := os.Mkdir(generationRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	anchorPath := filepath.Join(generationRoot, runtimeAttachmentGenerationLink)
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: anchorPath, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	listener.SetUnlinkOnClose(false)
	t.Cleanup(func() { _ = listener.Close() })
	if err := os.Chmod(anchorPath, 0o600); err != nil {
		t.Fatal(err)
	}

	rootDescriptor, err := unix.Open(runtimeRoot, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		t.Fatal(err)
	}
	generationDescriptor, err := unix.Openat(
		rootDescriptor, runtimeAttachmentGenerationName(generationID),
		unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0,
	)
	if err != nil {
		t.Fatal(err)
	}
	expected, err := runtimeAttachmentDescriptorIdentity(generationDescriptor)
	if closeErr := unix.Close(generationDescriptor); err == nil {
		err = closeErr
	}
	if err != nil {
		t.Fatal(err)
	}
	_, _, _, pinErr := pinRuntimeAttachmentGeneration(rootDescriptor, expected, generationID)
	if closeErr := unix.Close(rootDescriptor); pinErr == nil {
		pinErr = closeErr
	}
	if !errors.Is(pinErr, errRuntimeAttachmentOwnershipUnproven) {
		t.Fatalf("pinRuntimeAttachmentGeneration(socket) error = %v", pinErr)
	}
	if info, err := os.Lstat(anchorPath); err != nil || info.Mode()&os.ModeSocket == 0 {
		t.Fatalf("generation socket was not preserved: %#v, %v", info, err)
	}
}

func assertRuntimeAttachmentGenerationFIFOIsUnproven(t *testing.T, runtimeRoot string) {
	t.Helper()
	var generationID [16]byte
	generationID[0] = 1
	rootDescriptor, err := unix.Open(runtimeRoot, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		t.Fatal(err)
	}
	generationDescriptor, err := unix.Openat(
		rootDescriptor, runtimeAttachmentGenerationName(generationID),
		unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0,
	)
	if err != nil {
		t.Fatal(err)
	}
	expected, err := runtimeAttachmentDescriptorIdentity(generationDescriptor)
	if closeErr := unix.Close(generationDescriptor); err == nil {
		err = closeErr
	}
	if err != nil {
		t.Fatal(err)
	}
	_, _, _, pinErr := pinRuntimeAttachmentGeneration(rootDescriptor, expected, generationID)
	if closeErr := unix.Close(rootDescriptor); pinErr == nil {
		pinErr = closeErr
	}
	if !errors.Is(pinErr, errRuntimeAttachmentOwnershipUnproven) {
		t.Fatalf("pinRuntimeAttachmentGeneration(FIFO) error = %v", pinErr)
	}
}
