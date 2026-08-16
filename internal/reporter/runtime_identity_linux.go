//go:build linux

package reporter

import "golang.org/x/sys/unix"

func runtimeStatBirthTime(unix.Stat_t) (int64, int64) {
	return 0, 0
}
