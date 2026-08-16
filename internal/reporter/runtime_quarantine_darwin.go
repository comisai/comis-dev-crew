//go:build darwin

package reporter

import "golang.org/x/sys/unix"

func renameRuntimePathNoReplace(directoryDescriptor int, from, to string) error {
	return unix.RenameatxNp(directoryDescriptor, from, directoryDescriptor, to, unix.RENAME_EXCL)
}
