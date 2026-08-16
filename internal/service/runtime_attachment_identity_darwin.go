//go:build darwin

package service

import "golang.org/x/sys/unix"

func runtimeAttachmentStatBirthTime(stat unix.Stat_t) (int64, int64) {
	return stat.Btim.Sec, stat.Btim.Nsec
}
