//go:build linux

package validation

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

func observeOSProcess(ctx context.Context, pid int) (ProcessObservation, error) {
	if ctx == nil || pid < 1 {
		return ProcessObservation{}, errors.New("observe validation process: context and PID are required")
	}
	if err := ctx.Err(); err != nil {
		return ProcessObservation{}, err
	}
	procRoot := filepath.Join("/proc", strconv.Itoa(pid))
	stat, err := os.ReadFile(filepath.Join(procRoot, "stat"))
	if os.IsNotExist(err) {
		return ProcessObservation{}, ErrProcessAbsent
	}
	if err != nil {
		return ProcessObservation{}, errors.New("observe validation process: proc stat is unavailable")
	}
	closing := strings.LastIndexByte(string(stat), ')')
	if closing < 0 {
		return ProcessObservation{}, errors.New("observe validation process: proc stat is malformed")
	}
	fields := strings.Fields(string(stat[closing+1:]))
	if len(fields) < 20 || fields[2] == "" || fields[19] == "" {
		return ProcessObservation{}, errors.New("observe validation process: proc identity is incomplete")
	}
	executable, err := os.Readlink(filepath.Join(procRoot, "exe"))
	if os.IsNotExist(err) {
		return ProcessObservation{}, ErrProcessAbsent
	}
	if err != nil || executable == "" {
		return ProcessObservation{}, errors.New("observe validation process: executable identity is unavailable")
	}
	return ProcessObservation{
		PID: pid, StartIdentity: "linux-" + fields[19],
		ProcessGroupIdentity: fields[2], ExecutableLabel: filepath.Base(executable),
	}, nil
}

func expectedExecutableLabel(executable string) string {
	return filepath.Base(executable)
}
