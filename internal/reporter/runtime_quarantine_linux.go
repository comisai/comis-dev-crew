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

func exchangeRuntimePaths(directoryDescriptor int, left, right string) error {
	return unix.Renameat2(directoryDescriptor, left, directoryDescriptor, right, unix.RENAME_EXCHANGE)
}

func openRuntimeRemovalPath(
	directoryDescriptor int,
	name string,
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

func closeRuntimeRemovalPin(pin *runtimeRemovalPin) error {
	return unix.Close(pin.descriptor)
}
