//go:build darwin

package reporter

import (
	"fmt"

	"golang.org/x/sys/unix"
)

func runtimePinnedDirectoryOpenFlags() int {
	return 0x40000000 | unix.O_DIRECTORY | unix.O_NOFOLLOW
}

func runtimePinnedDirectoryPath(_ int, identity runtimePathIdentity) (string, error) {
	return fmt.Sprintf("/.vol/%d/%d", identity.device, identity.inode), nil
}
