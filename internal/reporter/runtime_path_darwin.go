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

func runtimePinnedSocketIdentity(directoryDescriptor int, name string) (runtimePathIdentity, error) {
	var before unix.Stat_t
	if err := unix.Fstatat(directoryDescriptor, name, &before, unix.AT_SYMLINK_NOFOLLOW); err != nil {
		return runtimePathIdentity{}, err
	}
	var after unix.Stat_t
	if err := unix.Fstatat(directoryDescriptor, name, &after, unix.AT_SYMLINK_NOFOLLOW); err != nil {
		return runtimePathIdentity{}, err
	}
	identity := runtimeStatIdentity(before)
	if !identity.sameObject(runtimeStatIdentity(after)) {
		return runtimePathIdentity{}, unix.ESTALE
	}
	return identity, nil
}
