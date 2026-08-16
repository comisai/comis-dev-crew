//go:build linux

package reporter

import (
	"os"

	"golang.org/x/sys/unix"
)

type runtimeRemovalPin struct {
	descriptor int
}

func renameRuntimePathNoReplace(directoryDescriptor int, from, to string) error {
	return unix.Renameat2(directoryDescriptor, from, directoryDescriptor, to, unix.RENAME_NOREPLACE)
}

func renameRuntimePathNoReplaceBetween(fromDescriptor int, from string, toDescriptor int, to string) error {
	return unix.Renameat2(fromDescriptor, from, toDescriptor, to, unix.RENAME_NOREPLACE)
}

func exchangeRuntimePaths(directoryDescriptor int, left, right string) error {
	return unix.Renameat2(directoryDescriptor, left, directoryDescriptor, right, unix.RENAME_EXCHANGE)
}

func openRuntimeRemovalPath(
	directoryDescriptor int,
	name, _ string,
	_ int,
	_ RuntimeSocketIdentity,
	_ RuntimePathKind,
	_ os.FileMode,
) (*runtimeRemovalPin, error) {
	descriptor, err := unix.Openat(directoryDescriptor, name, unix.O_PATH|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	return &runtimeRemovalPin{descriptor: descriptor}, nil
}

func statRuntimeRemovalPin(pin *runtimeRemovalPin, stat *unix.Stat_t) error {
	return unix.Fstat(pin.descriptor, stat)
}

func runtimeRemovalPinIdentity(pin *runtimeRemovalPin, stat unix.Stat_t) (RuntimeSocketIdentity, error) {
	identity, err := runtimeSocketStatIdentity(stat)
	if err != nil {
		return RuntimeSocketIdentity{}, err
	}
	var statx unix.Statx_t
	if unix.Statx(pin.descriptor, "", unix.AT_EMPTY_PATH|unix.AT_SYMLINK_NOFOLLOW, unix.STATX_BTIME, &statx) == nil &&
		statx.Mask&unix.STATX_BTIME != 0 {
		identity.BirthSec = statx.Btime.Sec
		identity.BirthNsec = int64(statx.Btime.Nsec)
	}
	return identity, nil
}

func closeRuntimeRemovalPin(pin *runtimeRemovalPin) error {
	return unix.Close(pin.descriptor)
}

func preserveRuntimeRemovalPin(pin *runtimeRemovalPin, _ RuntimePathKind) error {
	return unix.Close(pin.descriptor)
}
