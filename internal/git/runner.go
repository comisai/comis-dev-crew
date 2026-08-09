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
	if ctx == nil {
		return "", errors.New("git command context is required")
	}
	if err := ctx.Err(); err != nil {
		return "", err
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
			return "", ctxErr
		}
		if errors.Is(err, errGitOutputTooLarge) {
			return "", errGitOutputTooLarge
		}
		return "", fmt.Errorf("git inspection command failed: %w", err)
	}
	output := strings.TrimSuffix(stdout.buffer.String(), "\n")
	if output == "" || strings.ContainsAny(output, "\r\n\x00") {
		return "", errors.New("git inspection returned an invalid single-line result")
	}
	return output, nil
}
