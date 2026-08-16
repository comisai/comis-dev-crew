//go:build linux

package reporter

import (
	"fmt"

	"golang.org/x/sys/unix"
)

func runtimePinnedDirectoryOpenFlags() int {
	return unix.O_PATH | unix.O_DIRECTORY | unix.O_NOFOLLOW
}

func runtimeMountIdentitySupported() bool {
	return true
}

func runtimePinnedDirectoryPath(descriptor int, _ runtimePathIdentity) (string, error) {
	return fmt.Sprintf("/proc/self/fd/%d", descriptor), nil
}

func runtimeDescriptorMountID(descriptor int) (uint64, error) {
	var stat unix.Statx_t
	if err := unix.Statx(descriptor, "", unix.AT_EMPTY_PATH|unix.AT_SYMLINK_NOFOLLOW, unix.STATX_MNT_ID, &stat); err != nil ||
		stat.Mask&unix.STATX_MNT_ID == 0 {
		return 0, fmt.Errorf("runtime mount identity is unavailable")
	}
	return stat.Mnt_id, nil
}

func runtimePinnedSocketIdentity(directoryDescriptor int, name string) (runtimePathIdentity, error) {
	descriptor, err := unix.Openat(directoryDescriptor, name, unix.O_PATH|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return runtimePathIdentity{}, err
	}
	identity, identityErr := runtimeDescriptorIdentity(descriptor)
	closeErr := unix.Close(descriptor)
	if identityErr != nil {
		return runtimePathIdentity{}, identityErr
	}
	if closeErr != nil {
		return runtimePathIdentity{}, closeErr
	}
	return identity, nil
}
