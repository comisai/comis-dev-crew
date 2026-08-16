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
