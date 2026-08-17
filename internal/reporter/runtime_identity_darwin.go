//go:build darwin

package reporter

import "golang.org/x/sys/unix"

func runtimeStatBirthTime(stat unix.Stat_t) (int64, int64) {
	return stat.Btim.Sec, stat.Btim.Nsec
}

func runtimePinnedStatIdentity(_ int, _ string, stat unix.Stat_t) (RuntimeSocketIdentity, error) {
	return runtimeSocketStatIdentity(stat)
}
