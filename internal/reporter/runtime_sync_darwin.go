//go:build darwin

package reporter

import "golang.org/x/sys/unix"

func syncRuntimeDirectory(descriptor int) error {
	return unix.Fsync(descriptor)
}
