//go:build darwin

package validation

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"path/filepath"

	"golang.org/x/sys/unix"
)

func observeOSProcess(ctx context.Context, pid int) (ProcessObservation, error) {
	if ctx == nil || pid < 1 {
		return ProcessObservation{}, errors.New("observe validation process: context and PID are required")
	}
	if err := ctx.Err(); err != nil {
		return ProcessObservation{}, err
	}
	process, err := unix.SysctlKinfoProc("kern.proc.pid", pid)
	if err != nil {
		if errors.Is(err, unix.ESRCH) {
			return ProcessObservation{}, ErrProcessAbsent
		}
		return ProcessObservation{}, errors.New("observe validation process: kernel query failed")
	}
	if int(process.Proc.P_pid) != pid || process.Proc.P_stat == 0 {
		return ProcessObservation{}, ErrProcessAbsent
	}
	labelBytes := make([]byte, len(process.Proc.P_comm))
	for index, character := range process.Proc.P_comm {
		labelBytes[index] = byte(character)
	}
	labelBytes = bytes.TrimRight(labelBytes, "\x00")
	if len(labelBytes) == 0 || process.Eproc.Pgid < 1 {
		return ProcessObservation{}, errors.New("observe validation process: kernel identity is incomplete")
	}
	return ProcessObservation{
		PID:                  pid,
		StartIdentity:        fmt.Sprintf("darwin-%d-%d", process.Proc.P_starttime.Sec, process.Proc.P_starttime.Usec),
		ProcessGroupIdentity: fmt.Sprintf("%d", process.Eproc.Pgid),
		ExecutableLabel:      string(labelBytes),
	}, nil
}

func expectedExecutableLabel(executable string) string {
	label := filepath.Base(executable)
	if len(label) > len(unix.KinfoProc{}.Proc.P_comm)-1 {
		return label[:len(unix.KinfoProc{}.Proc.P_comm)-1]
	}
	return label
}
