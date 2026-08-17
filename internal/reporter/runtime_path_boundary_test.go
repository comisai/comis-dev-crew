package reporter

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func TestMountedRuntimeClientRejectsUnusableCallAuthority(t *testing.T) {
	mountDirectory := mountedRuntimeTestDirectory(t)
	const targetName = "attachment-0123456789abcdef0123456789abcdef.sock"
	socketPath := filepath.Join(mountDirectory, targetName)
	if _, err := newMountedRuntimeClient(
		socketPath, targetName, mountDirectory, boundaryRuntimeRelayIdentity(t), 0,
	); err == nil {
		t.Fatal("mounted runtime client accepted a non-positive timeout")
	}
	if _, err := newMountedRuntimeClient(
		socketPath, targetName, mountDirectory, boundaryRuntimeRelayIdentity(t), time.Hour,
	); err == nil {
		t.Fatal("mounted runtime client accepted an unbounded timeout")
	}
	if _, err := newMountedRuntimeClient(
		socketPath, targetName, mountDirectory, "not-a-relay-identity", time.Second,
	); err == nil {
		t.Fatal("mounted runtime client accepted an unparsable relay identity")
	}
}

func TestRuntimeSourceClientRejectsUnreadableAndPermissiveSockets(t *testing.T) {
	root := boundaryRuntimeDirectory(t)
	blocking := filepath.Join(root, "blocking")
	if err := os.WriteFile(blocking, []byte("not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := NewRuntimeClient(filepath.Join(blocking, "attachment.sock"), boundaryRuntimeRelayIdentity(t), time.Second)
	if err == nil || !strings.Contains(err.Error(), "socket identity is unavailable") {
		t.Fatalf("NewRuntimeClient(unreadable socket path) error = %v", err)
	}

	permissive := filepath.Join(root, "attachment.sock")
	listenBoundarySocket(t, permissive, 0o660)
	_, err = NewRuntimeClient(permissive, boundaryRuntimeRelayIdentity(t), time.Second)
	if err == nil || !strings.Contains(err.Error(), "require 0600") {
		t.Fatalf("NewRuntimeClient(group-readable socket) error = %v", err)
	}
}

func TestPinnedRuntimeSocketIdentityReportsUnavailableTargets(t *testing.T) {
	mountDirectory := mountedRuntimeTestDirectory(t)
	pinned, err := pinRuntimeMountDirectory(mountDirectory)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = pinned.close() }()
	_, err = pinnedRuntimeSocketIdentity(pinned, strings.Repeat("n", 400))
	if err == nil || !strings.Contains(err.Error(), "socket identity is unavailable") {
		t.Fatalf("pinnedRuntimeSocketIdentity(overlong target) error = %v", err)
	}
	if _, err := pinnedRuntimeSocketIdentity(pinned, "absent-attachment"); err == nil ||
		!strings.Contains(err.Error(), "socket does not exist") {
		t.Fatalf("pinnedRuntimeSocketIdentity(absent target) error = %v", err)
	}
}

func TestRuntimeDirectoryIdentityRequiresSupportedMountIdentity(t *testing.T) {
	mountDirectory := mountedRuntimeTestDirectory(t)
	descriptor := runtimePathTestDirectoryDescriptor(t, mountDirectory)
	if _, err := runtimeDirectoryDescriptorIdentity(descriptor, false); err != nil {
		t.Fatalf("runtimeDirectoryDescriptorIdentity(node identity) error = %v", err)
	}
	identity, err := runtimeDirectoryDescriptorIdentity(descriptor, true)
	if runtimeMountIdentitySupported() {
		if err != nil || identity.mountID == 0 {
			t.Fatalf("runtimeDirectoryDescriptorIdentity(mount identity) = %+v, %v", identity, err)
		}
		return
	}
	if err == nil {
		t.Fatal("unsupported platform reported a mount identity")
	}
}

func TestPinnedRuntimeMountReportsReleaseAndDriftFailures(t *testing.T) {
	mountDirectory := mountedRuntimeTestDirectory(t)
	pinned, err := pinRuntimeMountDirectory(mountDirectory)
	if err != nil {
		t.Fatal(err)
	}
	if !pinned.unchanged() {
		t.Fatal("freshly pinned mount reported drift")
	}
	pinned.identities[len(pinned.identities)-1].inode++
	if pinned.unchanged() {
		t.Fatal("pinned mount ignored a changed directory identity")
	}
	if err := pinned.close(); err != nil {
		t.Fatal(err)
	}

	stale, err := unix.Open(mountDirectory, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := unix.Close(stale); err != nil {
		t.Fatal(err)
	}
	released := &pinnedRuntimeMount{descriptors: []int{stale}}
	sentinel := os.ErrPermission
	if err := closePinnedRuntimeMount(released, sentinel); err == nil || err == sentinel {
		t.Fatalf("closePinnedRuntimeMount(released descriptor) error = %v", err)
	}
}
