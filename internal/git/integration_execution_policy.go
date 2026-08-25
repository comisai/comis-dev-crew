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
	local, err := registry.integrationGitConfigKeys(ctx, worktree, "--local")
	if err != nil {
		return err
	}
	worktreeConfig := false
	for _, key := range local {
		if key == "extensions.worktreeconfig" {
			worktreeConfig = true
		}
		if commandCapableGitConfigKey(key) {
			return errors.New("apply integration candidate: repository configuration can execute commands")
		}
	}
	if worktreeConfig {
		keys, err := registry.integrationGitConfigKeys(ctx, worktree, "--worktree")
		if err != nil {
			return err
		}
		for _, key := range keys {
			if commandCapableGitConfigKey(key) {
				return errors.New("apply integration candidate: repository configuration can execute commands")
			}
		}
	}
	return nil
}

func (registry *Registry) integrationGitConfigKeys(
	ctx context.Context,
	worktree string,
	scope string,
) ([]string, error) {
	output, exitCode, err := executeGit(ctx, registry.gitExecutable,
		"--no-optional-locks", "-C", worktree, "config", "--no-includes", scope,
		"--name-only", "-z", "--list")
	if err != nil || exitCode != 0 {
		return nil, errors.New("apply integration candidate: repository configuration is unavailable")
	}
	if len(output) == 0 {
		return nil, nil
	}
	encoded := bytes.Split(bytes.TrimSuffix(output, []byte{0}), []byte{0})
	keys := make([]string, 0, len(encoded))
	for _, value := range encoded {
		key := strings.ToLower(string(value))
		if key == "" || strings.ContainsAny(key, "\x00\r\n\t ") {
			return nil, errors.New("apply integration candidate: repository configuration is invalid")
		}
		keys = append(keys, key)
	}
	return keys, nil
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
		"check-attr", "-z", "--stdin", "merge", "filter", "diff",
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
		if attribute == "diff" && value != "unspecified" && value != "unset" && value != "set" {
			return errors.New("apply integration candidate: repository attributes select a custom diff driver")
		}
	}
	return nil
}
