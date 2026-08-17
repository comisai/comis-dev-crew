//go:build linux

package reporter

import (
	"errors"

	"golang.org/x/sys/unix"
)

func runtimeStatBirthTime(unix.Stat_t) (int64, int64) {
	return 0, 0
}

func runtimePinnedStatIdentity(directoryDescriptor int, name string, stat unix.Stat_t) (RuntimeSocketIdentity, error) {
	descriptor, err := unix.Openat(directoryDescriptor, name, unix.O_PATH|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return RuntimeSocketIdentity{}, err
	}
	identity, identityErr := runtimeRemovalPinIdentity(&runtimeRemovalPin{descriptor: descriptor}, stat)
	return identity, errors.Join(identityErr, unix.Close(descriptor))
}
