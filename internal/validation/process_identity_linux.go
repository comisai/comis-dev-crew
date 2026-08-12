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
		ProcessGroupIdentity: identity.group, ExecutableLabel: identity.label,
	}, nil
}

func observeOSExecutableLabel(ctx context.Context, label string) (bool, error) {
	if ctx == nil || !processIdentityPattern.MatchString(label) {
		return false, errors.New("scan validation processes: context and executable label are required")
	}
	if err := ctx.Err(); err != nil {
		return false, err
	}
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return false, errors.New("scan validation processes: proc is unavailable")
	}
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return false, err
		}
		pid, parseErr := strconv.Atoi(entry.Name())
		if parseErr != nil || pid < 1 {
			continue
		}
		identity, identityErr := readLinuxProcessIdentity(filepath.Join("/proc", entry.Name()))
		if errors.Is(identityErr, ErrProcessAbsent) {
			continue
		}
		if identityErr != nil {
			return false, errors.New("scan validation processes: process identity is unavailable")
		}
		if identity.label == label && identity.state != "Z" {
			return true, nil
		}
	}
	return false, nil
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
