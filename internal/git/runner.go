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
	buffer     bytes.Buffer
	limit      int
	overflowed bool
}

func (destination *boundedBuffer) Write(contents []byte) (int, error) {
	remaining := destination.limit - destination.buffer.Len()
	if remaining <= 0 {
		destination.overflowed = true
		return 0, errGitOutputTooLarge
	}
	if len(contents) > remaining {
		_, _ = destination.buffer.Write(contents[:remaining])
		destination.overflowed = true
		return remaining, errGitOutputTooLarge
	}
	return destination.buffer.Write(contents)
}

func runGit(ctx context.Context, executable string, arguments ...string) (string, error) {
	output, exitCode, err := executeHermeticGit(ctx, executable, arguments...)
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
	return runGitBytesWithLimit(ctx, maximumGitOutputBytes, executable, arguments...)
}

func runGitBytesWithLimit(
	ctx context.Context,
	outputLimit int,
	executable string,
	arguments ...string,
) ([]byte, error) {
	return runGitBytesWithInputAndLimit(ctx, nil, outputLimit, executable, arguments...)
}

func runGitBytesWithInputAndLimit(
	ctx context.Context,
	input []byte,
	outputLimit int,
	executable string,
	arguments ...string,
) ([]byte, error) {
	output, exitCode, err := executeHermeticGitWithEnvironmentInputAndOutputLimit(
		ctx, executable, nil, input, outputLimit, arguments...,
	)
	if err != nil {
		return nil, err
	}
	if exitCode != 0 {
		// The status is a number, not child output, so it names the failure class
		// without carrying paths or worker text into an error string.
		return nil, fmt.Errorf("git machine command failed with exit status %d", exitCode)
	}
	return output, nil
}

func gitPredicate(ctx context.Context, executable string, arguments ...string) (bool, error) {
	_, exitCode, err := executeHermeticGit(ctx, executable, arguments...)
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
	return executeChildWithEnvironmentInputAndOutputLimit(
		ctx, executable, nil, nil, maximumGitOutputBytes, false, arguments...,
	)
}

func executeHermeticGit(ctx context.Context, executable string, arguments ...string) ([]byte, int, error) {
	return executeGitWithEnvironment(ctx, executable, nil, arguments...)
}

type gitWorkspaceEnvironment struct {
	gitDir                      string
	gitWorkTree                 string
	gitIndex                    string
	gitObjectDirectory          string
	gitAlternateObjectDirectory string
}

func runGitInWorkspace(
	ctx context.Context,
	executable string,
	environment gitWorkspaceEnvironment,
	arguments ...string,
) (string, error) {
	output, exitCode, err := executeGitWithEnvironment(ctx, executable, &environment, arguments...)
	if err != nil {
		return "", err
	}
	if exitCode != 0 {
		return "", errors.New("git workspace inspection command failed")
	}
	result := strings.TrimSuffix(string(output), "\n")
	if result == "" || strings.ContainsAny(result, "\r\n\x00") {
		return "", errors.New("git workspace inspection returned an invalid single-line result")
	}
	return result, nil
}

func runGitBytesInWorkspace(
	ctx context.Context,
	executable string,
	environment gitWorkspaceEnvironment,
	arguments ...string,
) ([]byte, error) {
	output, exitCode, err := executeGitWithEnvironment(ctx, executable, &environment, arguments...)
	if err != nil {
		return nil, err
	}
	if exitCode != 0 {
		return nil, errors.New("git workspace machine command failed")
	}
	return output, nil
}

func runGitBytesInWorkspaceWithLimit(
	ctx context.Context,
	executable string,
	environment gitWorkspaceEnvironment,
	limit int,
	arguments ...string,
) ([]byte, error) {
	return runGitBytesInWorkspaceWithInputAndLimit(ctx, executable, environment, nil, limit, arguments...)
}

func runGitBytesInWorkspaceWithInputAndLimit(
	ctx context.Context,
	executable string,
	environment gitWorkspaceEnvironment,
	input []byte,
	limit int,
	arguments ...string,
) ([]byte, error) {
	output, exitCode, err := executeGitWithEnvironmentInputAndOutputLimit(
		ctx, executable, &environment, input, limit, arguments...,
	)
	if err != nil {
		return nil, err
	}
	if exitCode != 0 {
		return nil, errors.New("git workspace bounded machine command failed")
	}
	return output, nil
}

func gitPredicateInWorkspace(
	ctx context.Context,
	executable string,
	environment gitWorkspaceEnvironment,
	arguments ...string,
) (bool, error) {
	_, exitCode, err := executeGitWithEnvironment(ctx, executable, &environment, arguments...)
	if err != nil {
		return false, err
	}
	switch exitCode {
	case 0:
		return true, nil
	case 1:
		return false, nil
	default:
		return false, errors.New("git workspace predicate command failed")
	}
}

func executeGitWithEnvironment(
	ctx context.Context,
	executable string,
	workspace *gitWorkspaceEnvironment,
	arguments ...string,
) ([]byte, int, error) {
	return executeGitWithEnvironmentAndOutputLimit(
		ctx, executable, workspace, maximumGitOutputBytes, arguments...,
	)
}

func executeGitWithEnvironmentAndOutputLimit(
	ctx context.Context,
	executable string,
	workspace *gitWorkspaceEnvironment,
	outputLimit int,
	arguments ...string,
) ([]byte, int, error) {
	return executeHermeticGitWithEnvironmentInputAndOutputLimit(
		ctx, executable, workspace, nil, outputLimit, arguments...,
	)
}

func executeGitWithEnvironmentInputAndOutputLimit(
	ctx context.Context,
	executable string,
	workspace *gitWorkspaceEnvironment,
	input []byte,
	outputLimit int,
	arguments ...string,
) ([]byte, int, error) {
	return executeChildWithEnvironmentInputAndOutputLimit(
		ctx, executable, workspace, input, outputLimit, true, arguments...,
	)
}

func executeHermeticGitWithEnvironmentInputAndOutputLimit(
	ctx context.Context,
	executable string,
	workspace *gitWorkspaceEnvironment,
	input []byte,
	outputLimit int,
	arguments ...string,
) ([]byte, int, error) {
	return executeChildWithEnvironmentInputAndOutputLimit(
		ctx, executable, workspace, input, outputLimit, true, arguments...,
	)
}

func executeChildWithEnvironmentInputAndOutputLimit(
	ctx context.Context,
	executable string,
	workspace *gitWorkspaceEnvironment,
	input []byte,
	outputLimit int,
	hermeticGit bool,
	arguments ...string,
) ([]byte, int, error) {
	if ctx == nil {
		return nil, -1, errors.New("git command context is required")
	}
	if err := ctx.Err(); err != nil {
		return nil, -1, err
	}
	commandArguments := arguments
	if hermeticGit {
		commandArguments = hermeticGitArguments(arguments)
	}
	command := exec.CommandContext(ctx, executable, commandArguments...)
	command.Env = []string{
		"GIT_CONFIG_GLOBAL=/dev/null",
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_NO_REPLACE_OBJECTS=1",
		"GIT_OPTIONAL_LOCKS=0",
		"LC_ALL=C",
	}
	if workspace != nil {
		command.Env = append(command.Env,
			"GIT_DIR="+workspace.gitDir,
			"GIT_WORK_TREE="+workspace.gitWorkTree,
			"GIT_INDEX_FILE="+workspace.gitIndex,
		)
		if workspace.gitObjectDirectory != "" {
			command.Env = append(command.Env,
				"GIT_OBJECT_DIRECTORY="+workspace.gitObjectDirectory,
				"GIT_ALTERNATE_OBJECT_DIRECTORIES="+workspace.gitAlternateObjectDirectory,
			)
		}
	}
	command.WaitDelay = time.Second
	if input != nil {
		command.Stdin = bytes.NewReader(input)
	}
	stdout := &boundedBuffer{limit: outputLimit}
	stderr := &boundedBuffer{limit: maximumGitOutputBytes}
	command.Stdout = stdout
	command.Stderr = stderr
	if err := command.Run(); err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, -1, ctxErr
		}
		// An overflowing read stops this reader, which closes the child's pipe and
		// usually kills it. The resulting exit status describes that consequence,
		// not the command, so the bound is the authoritative answer and is checked
		// before any exit status is believed.
		if errors.Is(err, errGitOutputTooLarge) || stdout.overflowed {
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

func hermeticGitArguments(arguments []string) []string {
	configuration := []string{
		"-c", "core.fsmonitor=false",
		"-c", "core.hooksPath=/dev/null",
		"-c", "core.attributesFile=/dev/null",
		"-c", "core.editor=/usr/bin/false",
		"-c", "sequence.editor=/usr/bin/false",
		"-c", "core.sshCommand=/usr/bin/false",
		"-c", "credential.helper=",
		"-c", "diff.external=",
		"-c", "interactive.diffFilter=",
		"-c", "commit.gpgSign=false",
		"-c", "tag.gpgSign=false",
		"-c", "gpg.program=/usr/bin/false",
		"-c", "gpg.ssh.program=/usr/bin/false",
		"-c", "gc.auto=0",
		"-c", "maintenance.auto=false",
	}
	return append(configuration, arguments...)
}

func classifyGitChildFailure(exitCode int, stderr []byte) gitChildFailureKind {
	if exitCode < 0 || exitCode == 126 || exitCode == 127 {
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
