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
	identity, err := readLinuxProcessIdentity(procRoot)
	if err != nil {
		return ProcessObservation{}, err
	}
	executable, err := os.Readlink(filepath.Join(procRoot, "exe"))
	if os.IsNotExist(err) {
		identity, err = readLinuxProcessIdentity(procRoot)
		if err != nil {
			return ProcessObservation{}, err
		}
		if identity.state != "Z" {
			return ProcessObservation{}, ErrProcessAbsent
		}
		return ProcessObservation{
			PID: pid, StartIdentity: "linux-" + identity.start,
			ProcessGroupIdentity: identity.group, ExecutableLabel: identity.label, Exited: true,
		}, nil
	}
	if err != nil || executable == "" {
		return ProcessObservation{}, errors.New("observe validation process: executable identity is unavailable")
	}
	return ProcessObservation{
		PID: pid, StartIdentity: "linux-" + identity.start,
		ProcessGroupIdentity: identity.group, ExecutableLabel: filepath.Base(executable),
	}, nil
}

type linuxProcessIdentity struct {
	label string
	state string
	group string
	start string
}

func readLinuxProcessIdentity(procRoot string) (linuxProcessIdentity, error) {
	stat, err := os.ReadFile(filepath.Join(procRoot, "stat"))
	if os.IsNotExist(err) {
		return linuxProcessIdentity{}, ErrProcessAbsent
	}
	if err != nil {
		return linuxProcessIdentity{}, errors.New("observe validation process: proc stat is unavailable")
	}
	contents := string(stat)
	opening := strings.IndexByte(contents, '(')
	closing := strings.LastIndexByte(contents, ')')
	if opening < 0 || closing <= opening+1 {
		return linuxProcessIdentity{}, errors.New("observe validation process: proc stat is malformed")
	}
	fields := strings.Fields(contents[closing+1:])
	if len(fields) < 20 || fields[0] == "" || fields[2] == "" || fields[19] == "" {
		return linuxProcessIdentity{}, errors.New("observe validation process: proc identity is incomplete")
	}
	return linuxProcessIdentity{
		label: contents[opening+1 : closing], state: fields[0], group: fields[2], start: fields[19],
	}, nil
}

func expectedExecutableLabel(executable string) string {
	return filepath.Base(executable)
}
