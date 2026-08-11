package forge

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

const maximumGitPushOutputBytes = 8192

// GitBranchPusherConfig fixes the executable, remote, and private credential root.
type GitBranchPusherConfig struct {
	GitExecutable          string
	RemoteURL              string
	CredentialDirectory    string
	LocalFixtureRemoteRoot string
}

// GitBranchPusher performs an exact non-force branch push without a shell.
type GitBranchPusher struct {
	config GitBranchPusherConfig
}

// NewGitBranchPusher validates all host-owned paths and the fixed remote.
func NewGitBranchPusher(config GitBranchPusherConfig) (*GitBranchPusher, error) {
	if !absoluteCanonical(config.GitExecutable) || !absoluteCanonical(config.CredentialDirectory) {
		return nil, errors.New("create Git branch pusher: executable and credential paths are invalid")
	}
	executable, err := os.Lstat(config.GitExecutable)
	if err != nil || !executable.Mode().IsRegular() || executable.Mode()&0o111 == 0 {
		return nil, errors.New("create Git branch pusher: executable is unavailable")
	}
	directory, err := os.Lstat(config.CredentialDirectory)
	if err != nil || !directory.IsDir() || directory.Mode()&os.ModeSymlink != 0 || directory.Mode().Perm()&0o077 != 0 {
		return nil, errors.New("create Git branch pusher: credential directory is not owner-private")
	}
	if err := validatePushRemote(config.RemoteURL, config.LocalFixtureRemoteRoot); err != nil {
		return nil, err
	}
	return &GitBranchPusher{config: config}, nil
}

// Push re-verifies the branch, clean worktree, exact head, and remote ref.
func (pusher *GitBranchPusher) Push(ctx context.Context, credential Credential, request BranchPushRequest) error {
	if pusher == nil || ctx == nil || !validPushCredential(credential) {
		return errors.New("push Git branch: pusher, context, and push credential are required")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := validateBranchPushRequest(request); err != nil {
		return err
	}
	baseArguments := []string{"--no-optional-locks", "-C", request.WorktreePath}
	topLevel, err := pusher.singleLine(ctx, nil, append(baseArguments, "rev-parse", "--show-toplevel")...)
	if err != nil || topLevel != request.WorktreePath {
		return errors.New("push Git branch: worktree root differs")
	}
	branch, err := pusher.singleLine(ctx, nil, append(baseArguments, "symbolic-ref", "--quiet", "--short", "HEAD")...)
	if err != nil || branch != request.Branch {
		return errors.New("push Git branch: branch identity differs")
	}
	head, err := pusher.singleLine(ctx, nil, append(baseArguments, "rev-parse", "--verify", "HEAD^{commit}")...)
	if err != nil || head != request.HeadRevision {
		return errors.New("push Git branch: head identity differs")
	}
	status, err := pusher.execute(ctx, nil, append(baseArguments, "status", "--porcelain=v2", "-z", "--untracked-files=all")...)
	if err != nil || len(status) != 0 {
		return errors.New("push Git branch: dirty or untracked work is preserved")
	}
	credentialPath, err := pusher.writeCredential(credential.Secret)
	if err != nil {
		return err
	}
	environment := []string{"GIT_CONFIG_GLOBAL=" + credentialPath}
	_, pushErr := pusher.execute(ctx, environment, append(baseArguments,
		"-c", "core.hooksPath=/dev/null", "push", "--porcelain", "--atomic",
		pusher.config.RemoteURL, "HEAD:refs/heads/"+request.Branch,
	)...)
	var remote string
	var verifyErr error
	if pushErr == nil {
		remote, verifyErr = pusher.singleLine(ctx, environment, append(baseArguments,
			"ls-remote", "--refs", pusher.config.RemoteURL, "refs/heads/"+request.Branch,
		)...)
	}
	removeErr := os.Remove(credentialPath)
	if removeErr != nil {
		return errors.New("push Git branch: transient credential could not be removed")
	}
	if pushErr != nil {
		return errors.New("push Git branch: remote rejected exact head")
	}
	if verifyErr != nil || remote != request.HeadRevision+"\trefs/heads/"+request.Branch {
		return errors.New("push Git branch: remote head could not be verified")
	}
	return nil
}

func (pusher *GitBranchPusher) writeCredential(secret string) (string, error) {
	file, err := os.CreateTemp(pusher.config.CredentialDirectory, "git-credential-*.config")
	if err != nil {
		return "", errors.New("push Git branch: transient credential could not be created")
	}
	path := file.Name()
	remove := true
	defer func() {
		_ = file.Close()
		if remove {
			_ = os.Remove(path)
		}
	}()
	if err := file.Chmod(0o600); err != nil {
		return "", errors.New("push Git branch: transient credential mode could not be set")
	}
	if _, err := fmt.Fprintf(file, "[http]\n\textraHeader = Authorization: Bearer %s\n", secret); err != nil {
		return "", errors.New("push Git branch: transient credential could not be written")
	}
	if err := file.Sync(); err != nil {
		return "", errors.New("push Git branch: transient credential could not be synced")
	}
	if err := file.Close(); err != nil {
		return "", errors.New("push Git branch: transient credential could not be closed")
	}
	remove = false
	return path, nil
}

func (pusher *GitBranchPusher) singleLine(ctx context.Context, extraEnvironment []string, arguments ...string) (string, error) {
	output, err := pusher.execute(ctx, extraEnvironment, arguments...)
	if err != nil {
		return "", err
	}
	value := strings.TrimSuffix(string(output), "\n")
	if value == "" || strings.ContainsAny(value, "\r\n\x00") {
		return "", errors.New("git command returned invalid machine output")
	}
	return value, nil
}

func (pusher *GitBranchPusher) execute(ctx context.Context, extraEnvironment []string, arguments ...string) ([]byte, error) {
	command := exec.CommandContext(ctx, pusher.config.GitExecutable, arguments...)
	globalConfig := "GIT_CONFIG_GLOBAL=/dev/null"
	for _, value := range extraEnvironment {
		if strings.HasPrefix(value, "GIT_CONFIG_GLOBAL=") {
			globalConfig = value
		}
	}
	command.Env = []string{
		globalConfig, "GIT_CONFIG_NOSYSTEM=1", "GIT_OPTIONAL_LOCKS=0",
		"GIT_TERMINAL_PROMPT=0", "LC_ALL=C",
	}
	command.WaitDelay = time.Second
	stdout := &boundedGitBuffer{limit: maximumGitPushOutputBytes}
	stderr := &boundedGitBuffer{limit: maximumGitPushOutputBytes}
	command.Stdout = stdout
	command.Stderr = stderr
	if err := command.Run(); err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, errors.New("git command failed")
	}
	return append([]byte(nil), stdout.buffer.Bytes()...), nil
}

type boundedGitBuffer struct {
	buffer bytes.Buffer
	limit  int
}

func (buffer *boundedGitBuffer) Write(contents []byte) (int, error) {
	remaining := buffer.limit - buffer.buffer.Len()
	if remaining <= 0 || len(contents) > remaining {
		return 0, errors.New("git output exceeds its bound")
	}
	return buffer.buffer.Write(contents)
}

func validateBranchPushRequest(request BranchPushRequest) error {
	if !operationIDPattern.MatchString(request.OperationID) || !absoluteCanonical(request.WorktreePath) ||
		!branchPattern.MatchString(request.Branch) || strings.Contains(request.Branch, "..") ||
		!revisionPattern.MatchString(request.HeadRevision) {
		return errors.New("push Git branch: request is invalid")
	}
	return nil
}

func validatePushRemote(remoteValue, fixtureRoot string) error {
	remote, err := url.Parse(remoteValue)
	if err != nil || remote.User != nil || remote.RawQuery != "" || remote.Fragment != "" {
		return errors.New("create Git branch pusher: remote URL is invalid")
	}
	if remote.Scheme == "https" && remote.Host != "" {
		return nil
	}
	if remote.Scheme != "file" || remote.Host != "" || !absoluteCanonical(remote.Path) || !absoluteCanonical(fixtureRoot) {
		return errors.New("create Git branch pusher: remote URL is invalid")
	}
	relative, err := filepath.Rel(fixtureRoot, remote.Path)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(os.PathSeparator)) {
		return errors.New("create Git branch pusher: fixture remote escapes its approved root")
	}
	return nil
}

func absoluteCanonical(path string) bool {
	return filepath.IsAbs(path) && filepath.Clean(path) == path
}
