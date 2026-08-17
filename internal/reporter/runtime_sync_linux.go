//go:build linux

package reporter

import (
	"errors"

	"golang.org/x/sys/unix"
)

func syncRuntimeDirectory(descriptor int) error {
	syncDescriptor, err := unix.Openat(
		descriptor, ".", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0,
	)
	if err != nil {
		return err
	}
	var pinned unix.Stat_t
	var opened unix.Stat_t
	pinnedErr := unix.Fstat(descriptor, &pinned)
	openedErr := unix.Fstat(syncDescriptor, &opened)
	if pinnedErr != nil || openedErr != nil || pinned.Dev != opened.Dev || pinned.Ino != opened.Ino ||
		opened.Mode&unix.S_IFMT != unix.S_IFDIR {
		return errors.Join(unix.ESTALE, pinnedErr, openedErr, unix.Close(syncDescriptor))
	}
	return errors.Join(unix.Fsync(syncDescriptor), unix.Close(syncDescriptor))
}
