//go:build linux

package reporter

import (
	"os"
	"testing"

	"golang.org/x/sys/unix"
)

func TestPublishRuntimeDirectoryMatchesDescriptorBirthIdentity(t *testing.T) {
	root := boundaryRuntimeDirectory(t)
	directory := runtimePathTestDirectoryDescriptor(t, root)
	if err := unix.Mkdirat(directory, "prepared", 0o700); err != nil {
		t.Fatal(err)
	}
	descriptor, err := unix.Openat(directory, "prepared", unix.O_PATH|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer unix.Close(descriptor)
	var stat unix.Stat_t
	if err := unix.Fstat(descriptor, &stat); err != nil {
		t.Fatal(err)
	}
	expected, err := runtimeSocketStatIdentity(stat)
	if err != nil {
		t.Fatal(err)
	}
	var statx unix.Statx_t
	if err := unix.Statx(descriptor, "", unix.AT_EMPTY_PATH|unix.AT_SYMLINK_NOFOLLOW, unix.STATX_BTIME, &statx); err == nil &&
		statx.Mask&unix.STATX_BTIME != 0 {
		expected.BirthSec = statx.Btime.Sec
		expected.BirthNsec = int64(statx.Btime.Nsec)
	}
	if err := PublishRuntimeDirectory(directory, "prepared", "active", expected, 0o700); err != nil {
		t.Fatalf("PublishRuntimeDirectory(descriptor birth identity) error = %v", err)
	}
	if info, err := os.Lstat(root + "/active"); err != nil || !info.IsDir() {
		t.Fatalf("published runtime directory = %#v, %v", info, err)
	}
}
