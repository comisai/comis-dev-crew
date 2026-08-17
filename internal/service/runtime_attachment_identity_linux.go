//go:build linux

package service

import "golang.org/x/sys/unix"

func runtimeAttachmentStatBirthTime(unix.Stat_t) (int64, int64) {
	return 0, 0
}

func runtimeAttachmentDescriptorBirthTime(descriptor int) (int64, int64) {
	var stat unix.Statx_t
	if unix.Statx(descriptor, "", unix.AT_EMPTY_PATH|unix.AT_SYMLINK_NOFOLLOW, unix.STATX_BTIME, &stat) != nil ||
		stat.Mask&unix.STATX_BTIME == 0 {
		return 0, 0
	}
	return stat.Btime.Sec, int64(stat.Btime.Nsec)
}

func runtimeAttachmentPathBirthTime(path string) (int64, int64) {
	var stat unix.Statx_t
	if unix.Statx(unix.AT_FDCWD, path, unix.AT_SYMLINK_NOFOLLOW, unix.STATX_BTIME, &stat) != nil ||
		stat.Mask&unix.STATX_BTIME == 0 {
		return 0, 0
	}
	return stat.Btime.Sec, int64(stat.Btime.Nsec)
}

func runtimeAttachmentChildBirthTime(directoryDescriptor int, name string) (int64, int64) {
	var stat unix.Statx_t
	if unix.Statx(directoryDescriptor, name, unix.AT_SYMLINK_NOFOLLOW, unix.STATX_BTIME, &stat) != nil ||
		stat.Mask&unix.STATX_BTIME == 0 {
		return 0, 0
	}
	return stat.Btime.Sec, int64(stat.Btime.Nsec)
}

func runtimeAttachmentDescriptorMountID(descriptor int) (uint64, error) {
	var stat unix.Statx_t
	if err := unix.Statx(descriptor, "", unix.AT_EMPTY_PATH|unix.AT_SYMLINK_NOFOLLOW, unix.STATX_MNT_ID, &stat); err != nil ||
		stat.Mask&unix.STATX_MNT_ID == 0 || stat.Mnt_id == 0 {
		return 0, unix.ESTALE
	}
	return stat.Mnt_id, nil
}
