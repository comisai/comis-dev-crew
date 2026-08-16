//go:build linux

package reporter

import "golang.org/x/sys/unix"

func renameRuntimePathNoReplace(directoryDescriptor int, from, to string) error {
	return unix.Renameat2(directoryDescriptor, from, directoryDescriptor, to, unix.RENAME_NOREPLACE)
}
