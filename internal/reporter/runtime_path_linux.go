//go:build linux

package reporter

import (
	"fmt"

	"golang.org/x/sys/unix"
)

func runtimePinnedDirectoryOpenFlags() int {
	return unix.O_PATH | unix.O_DIRECTORY | unix.O_NOFOLLOW
}

func runtimePinnedDirectoryPath(descriptor int, _ runtimePathIdentity) (string, error) {
	return fmt.Sprintf("/proc/self/fd/%d", descriptor), nil
}
