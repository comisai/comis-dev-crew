package forge

import (
	"bytes"
	"context"
	"encoding/base64"
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
	SSHTransportExecutable string
	SSHExecutable          string
	SSHKnownHostsFile      string
}

// GitBranchPusher performs an exact non-force branch push without a shell.
type GitBranchPusher struct {
	config        GitBranchPusherConfig
	sshHost       string
	sshRemotePath string
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
	remote, err := validatePushRemoteURL(config.RemoteURL, config.LocalFixtureRemoteRoot)
	if err != nil {
		return nil, err
	}
	sshHost := ""
	sshRemotePath := ""
	if remote.Scheme == "ssh" {
		if !canonicalExecutable(config.SSHTransportExecutable, "") || !canonicalExecutable(config.SSHExecutable, "ssh") ||
			!canonicalKnownHosts(config.SSHKnownHostsFile) {
			return nil, errors.New("create Git branch pusher: SSH transport is unavailable or unsafe")
		}
		sshHost = remote.Hostname()
		sshRemotePath = remote.Path
	} else if config.SSHTransportExecutable != "" || config.SSHExecutable != "" || config.SSHKnownHostsFile != "" {
		return nil, errors.New("create Git branch pusher: SSH transport differs from the remote")
	}
	return &GitBranchPusher{config: config, sshHost: sshHost, sshRemotePath: sshRemotePath}, nil
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
	credentialPath, environment, err := pusher.prepareCredential(credential.Secret)
	if err != nil {
		return err
	}
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

func (pusher *GitBranchPusher) prepareCredential(secret string) (string, []string, error) {
	if pusher.sshHost == "" {
		path, err := pusher.writeCredential(secret)
		return path, []string{"GIT_CONFIG_GLOBAL=" + path}, err
	}
	path, err := pusher.writeSSHCredential(secret)
	if err != nil {
		return "", nil, err
	}
	return path, []string{
		"GIT_CONFIG_GLOBAL=/dev/null",
		"GIT_SSH=" + pusher.config.SSHTransportExecutable,
		"GIT_SSH_VARIANT=ssh",
		"DEV_CREW_SSH_TRANSPORT=1",
		"DEV_CREW_SSH_EXECUTABLE=" + pusher.config.SSHExecutable,
		"DEV_CREW_SSH_KEY_FILE=" + path,
		"DEV_CREW_SSH_KNOWN_HOSTS_FILE=" + pusher.config.SSHKnownHostsFile,
		"DEV_CREW_SSH_HOST=" + pusher.sshHost,
		"DEV_CREW_SSH_REMOTE_PATH=" + pusher.sshRemotePath,
	}, nil
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

func (pusher *GitBranchPusher) writeSSHCredential(secret string) (string, error) {
	contents, err := base64.StdEncoding.DecodeString(secret)
	privateKeyHeader := "-----BEGIN OPENSSH " + "PRIVATE KEY-----\n"
	if err != nil || len(contents) == 0 || len(contents) > 4096 ||
		!bytes.HasPrefix(contents, []byte(privateKeyHeader)) ||
		!bytes.HasSuffix(contents, []byte("-----END OPENSSH PRIVATE KEY-----\n")) || bytes.ContainsRune(contents, '\x00') {
		return "", errors.New("push Git branch: SSH deploy key is invalid")
	}
	file, err := os.CreateTemp(pusher.config.CredentialDirectory, "git-deploy-key-*.key")
	if err != nil {
		return "", errors.New("push Git branch: transient SSH key could not be created")
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
		return "", errors.New("push Git branch: transient SSH key mode could not be set")
	}
	if _, err := file.Write(contents); err != nil || file.Sync() != nil || file.Close() != nil {
		return "", errors.New("push Git branch: transient SSH key could not be persisted")
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
	for _, value := range extraEnvironment {
		if strings.HasPrefix(value, "GIT_CONFIG_GLOBAL=") {
			continue
		}
		if !allowedGitEnvironment(value) {
			return nil, errors.New("git command environment is invalid")
		}
		command.Env = append(command.Env, value)
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
	_, err := validatePushRemoteURL(remoteValue, fixtureRoot)
	return err
}

func validatePushRemoteURL(remoteValue, fixtureRoot string) (*url.URL, error) {
	remote, err := url.Parse(remoteValue)
	if err != nil || remote.RawQuery != "" || remote.Fragment != "" {
		return nil, errors.New("create Git branch pusher: remote URL is invalid")
	}
	if remote.Scheme == "https" && remote.Host != "" && remote.User == nil {
		return remote, nil
	}
	if remote.Scheme == "ssh" && validSSHRemote(remote) {
		return remote, nil
	}
	if remote.Scheme != "file" || remote.Host != "" || remote.User != nil || !absoluteCanonical(remote.Path) || !absoluteCanonical(fixtureRoot) {
		return nil, errors.New("create Git branch pusher: remote URL is invalid")
	}
	relative, err := filepath.Rel(fixtureRoot, remote.Path)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(os.PathSeparator)) {
		return nil, errors.New("create Git branch pusher: fixture remote escapes its approved root")
	}
	return remote, nil
}

func validSSHRemote(remote *url.URL) bool {
	if remote == nil || remote.User == nil || remote.User.Username() != "git" {
		return false
	}
	if password, present := remote.User.Password(); present || password != "" || remote.Hostname() == "" || remote.Port() != "" {
		return false
	}
	parts := strings.Split(strings.TrimPrefix(remote.Path, "/"), "/")
	return len(parts) == 2 && githubNamePattern.MatchString(parts[0]) &&
		strings.HasSuffix(parts[1], ".git") && githubNamePattern.MatchString(strings.TrimSuffix(parts[1], ".git"))
}

func canonicalExecutable(path, requiredBase string) bool {
	if !absoluteCanonical(path) || requiredBase != "" && filepath.Base(path) != requiredBase {
		return false
	}
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&0o111 == 0 || info.Mode()&os.ModeSymlink != 0 {
		return false
	}
	resolved, err := filepath.EvalSymlinks(path)
	return err == nil && resolved == path
}

func canonicalKnownHosts(path string) bool {
	if !absoluteCanonical(path) {
		return false
	}
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 ||
		info.Mode().Perm()&0o022 != 0 || info.Size() < 1 || info.Size() > 1<<20 {
		return false
	}
	resolved, err := filepath.EvalSymlinks(path)
	return err == nil && resolved == path
}

func allowedGitEnvironment(value string) bool {
	for _, prefix := range []string{
		"GIT_SSH=", "GIT_SSH_VARIANT=", "DEV_CREW_SSH_TRANSPORT=", "DEV_CREW_SSH_EXECUTABLE=",
		"DEV_CREW_SSH_KEY_FILE=", "DEV_CREW_SSH_KNOWN_HOSTS_FILE=", "DEV_CREW_SSH_HOST=",
		"DEV_CREW_SSH_REMOTE_PATH=",
	} {
		if strings.HasPrefix(value, prefix) && len(value) > len(prefix) && !strings.ContainsAny(value, "\x00\r\n") {
			return true
		}
	}
	return false
}

func absoluteCanonical(path string) bool {
	return filepath.IsAbs(path) && filepath.Clean(path) == path
}
