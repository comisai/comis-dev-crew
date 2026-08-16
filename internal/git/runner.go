package git

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

const maximumGitOutputBytes = 8192

var errGitOutputTooLarge = errors.New("git output exceeded the configured bound")
var errGitInfrastructure = errors.New("git execution infrastructure is unavailable")

type gitChildFailureKind uint8

const (
	gitChildRepositoryFailure gitChildFailureKind = iota + 1
	gitChildInfrastructureFailure
)

type boundedBuffer struct {
	buffer bytes.Buffer
	limit  int
}

func (destination *boundedBuffer) Write(contents []byte) (int, error) {
	remaining := destination.limit - destination.buffer.Len()
	if remaining <= 0 {
		return 0, errGitOutputTooLarge
	}
	if len(contents) > remaining {
		_, _ = destination.buffer.Write(contents[:remaining])
		return remaining, errGitOutputTooLarge
	}
	return destination.buffer.Write(contents)
}

func runGit(ctx context.Context, executable string, arguments ...string) (string, error) {
	output, exitCode, err := executeGit(ctx, executable, arguments...)
	if err != nil {
		return "", err
	}
	if exitCode != 0 {
		return "", errors.New("git inspection command failed")
	}
	result := strings.TrimSuffix(string(output), "\n")
	if result == "" || strings.ContainsAny(result, "\r\n\x00") {
		return "", errors.New("git inspection returned an invalid single-line result")
	}
	return result, nil
}

func runGitBytes(ctx context.Context, executable string, arguments ...string) ([]byte, error) {
	output, exitCode, err := executeGit(ctx, executable, arguments...)
	if err != nil {
		return nil, err
	}
	if exitCode != 0 {
		return nil, errors.New("git machine command failed")
	}
	return output, nil
}

func gitPredicate(ctx context.Context, executable string, arguments ...string) (bool, error) {
	_, exitCode, err := executeGit(ctx, executable, arguments...)
	if err != nil {
		return false, err
	}
	switch exitCode {
	case 0:
		return true, nil
	case 1:
		return false, nil
	default:
		return false, errors.New("git predicate command failed")
	}
}

func executeGit(ctx context.Context, executable string, arguments ...string) ([]byte, int, error) {
	if ctx == nil {
		return nil, -1, errors.New("git command context is required")
	}
	if err := ctx.Err(); err != nil {
		return nil, -1, err
	}
	command := exec.CommandContext(ctx, executable, arguments...)
	command.Env = []string{
		"GIT_CONFIG_GLOBAL=/dev/null",
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_OPTIONAL_LOCKS=0",
		"LC_ALL=C",
	}
	command.WaitDelay = time.Second
	stdout := &boundedBuffer{limit: maximumGitOutputBytes}
	stderr := &boundedBuffer{limit: maximumGitOutputBytes}
	command.Stdout = stdout
	command.Stderr = stderr
	if err := command.Run(); err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, -1, ctxErr
		}
		if errors.Is(err, errGitOutputTooLarge) {
			return nil, -1, errGitOutputTooLarge
		}
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			if classifyGitChildFailure(exit.ExitCode(), stderr.buffer.Bytes()) == gitChildInfrastructureFailure {
				return nil, -1, fmt.Errorf("git command failed after launch: %w", errGitInfrastructure)
			}
			return append([]byte(nil), stdout.buffer.Bytes()...), exit.ExitCode(), nil
		}
		return nil, -1, fmt.Errorf("git command execution failed: %w", errGitInfrastructure)
	}
	return append([]byte(nil), stdout.buffer.Bytes()...), 0, nil
}

func classifyGitChildFailure(exitCode int, stderr []byte) gitChildFailureKind {
	if exitCode < 0 {
		return gitChildInfrastructureFailure
	}
	diagnostic := strings.ToLower(strings.TrimSpace(string(stderr)))
	for _, marker := range []string{
		"permission denied",
		"operation not permitted",
		"input/output error",
		"i/o error",
		"read-only file system",
		"no space left on device",
		"disk quota exceeded",
		"too many open files",
		"cannot allocate memory",
		"out of memory",
		"resource temporarily unavailable",
		"stale file handle",
		"device not configured",
		"bad file descriptor",
		"broken pipe",
		"interrupted system call",
		"timed out",
	} {
		if strings.Contains(diagnostic, marker) {
			return gitChildInfrastructureFailure
		}
	}
	return gitChildRepositoryFailure
}
