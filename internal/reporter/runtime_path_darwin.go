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

func runtimeDescriptorMountID(descriptor int) (uint64, error) {
	var stat unix.Statfs_t
	if err := unix.Fstatfs(descriptor, &stat); err != nil {
		return 0, err
	}
	return uint64(uint32(stat.Fsid.Val[0]))<<32 | uint64(uint32(stat.Fsid.Val[1])), nil
}
