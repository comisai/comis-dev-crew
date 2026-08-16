//go:build darwin

package reporter

import (
	"fmt"

	"golang.org/x/sys/unix"
)

func runtimePinnedDirectoryOpenFlags() int {
	return 0x40000000 | unix.O_DIRECTORY | unix.O_NOFOLLOW
}

func runtimeMountIdentitySupported() bool {
	return false
}

func runtimePinnedDirectoryPath(_ int, identity runtimePathIdentity) (string, error) {
	return fmt.Sprintf("/.vol/%d/%d", identity.device, identity.inode), nil
}

func runtimeDescriptorMountID(_ int) (uint64, error) {
	return 0, unix.ENOTSUP
}

func runtimePinnedSocketIdentity(_ int, _ string) (runtimePathIdentity, error) {
	return runtimePathIdentity{}, unix.ENOTSUP
}
