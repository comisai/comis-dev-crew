//go:build darwin

package reporter

import "golang.org/x/sys/unix"

func runtimeStatBirthTime(stat unix.Stat_t) (int64, int64) {
	return stat.Btim.Sec, stat.Btim.Nsec
}
