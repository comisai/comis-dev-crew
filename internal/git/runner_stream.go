package git

import (
	"bufio"
	"context"
	"errors"
	"io"
	"os/exec"
	"time"
)

const maximumGitMachineRecordBytes = 1200

func streamHermeticGitNULRecords(
	ctx context.Context,
	executable string,
	workspace *gitWorkspaceEnvironment,
	maximumRecords int,
	arguments []string,
	consume func([]byte) error,
) error {
	if ctx == nil || maximumRecords < 1 || consume == nil {
		return errors.New("git streaming command boundary is invalid")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	childContext, cancel := context.WithCancel(ctx)
	defer cancel()
	command := exec.CommandContext(childContext, executable, hermeticGitArguments(arguments)...)
	command.Env = hermeticGitEnvironment(workspace)
	command.WaitDelay = time.Second
	stderr := &boundedBuffer{limit: maximumGitOutputBytes}
	command.Stderr = stderr
	stdout, err := command.StdoutPipe()
	if err != nil {
		return errors.New("git streaming command pipe is unavailable")
	}
	if err := command.Start(); err != nil {
		return errors.New("git streaming command could not start")
	}
	reader := bufio.NewReaderSize(stdout, maximumGitMachineRecordBytes+1)
	records := 0
	var streamErr error
	for streamErr == nil {
		record, readErr := reader.ReadSlice(0)
		switch {
		case readErr == nil:
			records++
			if records > maximumRecords || len(record) < 2 || len(record) > maximumGitMachineRecordBytes {
				streamErr = errors.New("git streaming record exceeds its bound")
				break
			}
			streamErr = consume(record[:len(record)-1])
		case errors.Is(readErr, bufio.ErrBufferFull):
			streamErr = errors.New("git streaming record exceeds its bound")
		case errors.Is(readErr, io.EOF) && len(record) == 0:
			streamErr = io.EOF
		case errors.Is(readErr, io.EOF):
			streamErr = errors.New("git streaming response has trailing data")
		default:
			streamErr = errors.New("git streaming response is unavailable")
		}
	}
	if !errors.Is(streamErr, io.EOF) {
		cancel()
	}
	waitErr := command.Wait()
	if ctxErr := ctx.Err(); ctxErr != nil {
		return ctxErr
	}
	if !errors.Is(streamErr, io.EOF) {
		return streamErr
	}
	if waitErr != nil {
		var exit *exec.ExitError
		if errors.As(waitErr, &exit) &&
			classifyGitChildFailure(exit.ExitCode(), stderr.buffer.Bytes()) == gitChildRepositoryFailure {
			return errors.New("git streaming machine command failed")
		}
		return errors.New("git streaming command execution failed")
	}
	return nil
}
