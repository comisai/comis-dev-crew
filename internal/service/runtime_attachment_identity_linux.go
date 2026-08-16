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
