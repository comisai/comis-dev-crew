//go:build darwin

package service

import "golang.org/x/sys/unix"

func runtimeAttachmentStatBirthTime(stat unix.Stat_t) (int64, int64) {
	return stat.Btim.Sec, stat.Btim.Nsec
}

func runtimeAttachmentDescriptorBirthTime(descriptor int) (int64, int64) {
	var stat unix.Stat_t
	if unix.Fstat(descriptor, &stat) != nil {
		return 0, 0
	}
	return runtimeAttachmentStatBirthTime(stat)
}

func runtimeAttachmentPathBirthTime(string) (int64, int64) {
	return 0, 0
}

func runtimeAttachmentChildBirthTime(int, string) (int64, int64) {
	return 0, 0
}

func runtimeAttachmentDescriptorMountID(descriptor int) (uint64, error) {
	var stat unix.Statfs_t
	if err := unix.Fstatfs(descriptor, &stat); err != nil {
		return 0, err
	}
	mountID := uint64(uint32(stat.Fsid.Val[0]))<<32 | uint64(uint32(stat.Fsid.Val[1]))
	if mountID == 0 {
		return 0, unix.ESTALE
	}
	return mountID, nil
}
