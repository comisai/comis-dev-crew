//go:build linux

package service

import "golang.org/x/sys/unix"

func runtimeAttachmentStatBirthTime(unix.Stat_t) (int64, int64) {
	return 0, 0
}
