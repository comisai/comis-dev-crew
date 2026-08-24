package git

import (
	"bytes"
	"context"
	"errors"
	"strings"

	"github.com/comisai/comis-dev-crew/internal/application"
)

func (registry *Registry) validateIntegrationExecutionPolicy(
	ctx context.Context,
	request application.IntegrationAdapterRequest,
) error {
	for _, worktree := range []string{request.Target.WorktreePath, request.Candidate.WorktreePath} {
		if err := registry.rejectCommandCapableGitConfig(ctx, worktree); err != nil {
			return err
		}
		if err := registry.rejectCommandCapableGitAttributes(ctx, worktree); err != nil {
			return err
		}
	}
	return nil
}

func (registry *Registry) rejectCommandCapableGitConfig(ctx context.Context, worktree string) error {
	output, exitCode, err := executeGit(ctx, registry.gitExecutable,
		"--no-optional-locks", "-C", worktree, "config", "--local", "--name-only", "-z", "--list")
	if err != nil || exitCode != 0 {
		return errors.New("apply integration candidate: repository configuration is unavailable")
	}
	for _, encoded := range bytes.Split(bytes.TrimSuffix(output, []byte{0}), []byte{0}) {
		key := strings.ToLower(string(encoded))
		if commandCapableGitConfigKey(key) {
			return errors.New("apply integration candidate: repository configuration can execute commands")
		}
	}
	return nil
}

func commandCapableGitConfigKey(key string) bool {
	if key == "core.fsmonitor" || key == "core.attributesfile" || key == "core.hookspath" ||
		key == "core.sshcommand" || key == "core.editor" || key == "credential.helper" ||
		key == "interactive.difffilter" || key == "gpg.program" || key == "gpg.ssh.program" ||
		key == "sequence.editor" || strings.HasPrefix(key, "include.") || strings.HasPrefix(key, "includeif.") {
		return true
	}
	parts := strings.Split(key, ".")
	if len(parts) < 3 {
		return false
	}
	last := parts[len(parts)-1]
	switch parts[0] {
	case "merge":
		return last == "driver"
	case "filter":
		return last == "clean" || last == "smudge" || last == "process"
	case "diff":
		return last == "command" || last == "textconv"
	default:
		return false
	}
}

func (registry *Registry) rejectCommandCapableGitAttributes(ctx context.Context, worktree string) error {
	paths, err := runGitBytesWithLimit(ctx, maximumRebasePatchBytes, registry.gitExecutable,
		"--no-optional-locks", "-C", worktree, "ls-files", "-z")
	if err != nil {
		return errors.New("apply integration candidate: repository attributes are unavailable")
	}
	if len(paths) == 0 {
		return nil
	}
	output, exitCode, err := executeGitWithEnvironmentInputAndOutputLimit(
		ctx, registry.gitExecutable, nil, paths, maximumRebasePatchBytes,
		"--no-optional-locks", "-C", worktree, "-c", "core.attributesFile=/dev/null",
		"check-attr", "-z", "--stdin", "merge", "filter",
	)
	if err != nil || exitCode != 0 {
		return errors.New("apply integration candidate: repository attributes are unavailable")
	}
	fields := bytes.Split(bytes.TrimSuffix(output, []byte{0}), []byte{0})
	if len(fields)%3 != 0 {
		return errors.New("apply integration candidate: repository attributes are invalid")
	}
	for index := 0; index < len(fields); index += 3 {
		attribute, value := string(fields[index+1]), string(fields[index+2])
		if attribute == "filter" && value != "unspecified" && value != "unset" {
			return errors.New("apply integration candidate: repository attributes can execute filters")
		}
		if attribute == "merge" && value != "unspecified" && value != "unset" && value != "set" &&
			value != "text" && value != "binary" && value != "union" {
			return errors.New("apply integration candidate: repository attributes select a custom merge driver")
		}
	}
	return nil
}
