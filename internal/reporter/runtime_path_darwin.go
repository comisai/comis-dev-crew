//go:build darwin

package reporter

import (
	"fmt"
	"path/filepath"

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

func runtimePinnedSocketIdentity(directoryDescriptor int, name string) (runtimePathIdentity, error) {
	directoryIdentity, err := runtimeDescriptorIdentity(directoryDescriptor)
	if err != nil {
		return runtimePathIdentity{}, err
	}
	directory, err := runtimePinnedDirectoryPath(directoryDescriptor, directoryIdentity)
	if err != nil {
		return runtimePathIdentity{}, err
	}
	var before unix.Stat_t
	if err := unix.Fstatat(directoryDescriptor, name, &before, unix.AT_SYMLINK_NOFOLLOW); err != nil {
		return runtimePathIdentity{}, err
	}
	var filesystem unix.Statfs_t
	if err := unix.Statfs(filepath.Join(directory, name), &filesystem); err != nil {
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
	identity.mountID = uint64(uint32(filesystem.Fsid.Val[0]))<<32 | uint64(uint32(filesystem.Fsid.Val[1]))
	return identity, nil
}
