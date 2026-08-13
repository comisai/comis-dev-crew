package forge

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// SSHTransportConfig is the private fixed environment passed by GitBranchPusher
// when the service executable is acting as Git's GIT_SSH transport.
type SSHTransportConfig struct {
	Executable     string
	KeyFile        string
	KnownHostsFile string
	ExpectedHost   string
	RemotePath     string
	GitProtocol    string
}

// RunSSHTransport executes the reviewed OpenSSH binary without a shell and
// returns a process exit code suitable for Git's GIT_SSH contract.
func RunSSHTransport(
	ctx context.Context,
	arguments []string,
	stdin io.Reader,
	stdout io.Writer,
	stderr io.Writer,
	config SSHTransportConfig,
) int {
	if ctx == nil || stdin == nil || stdout == nil || stderr == nil || !validSSHTransportConfig(config) ||
		!validGitSSHArguments(arguments, config.ExpectedHost, config.RemotePath) {
		return 2
	}
	if err := ctx.Err(); err != nil {
		return 1
	}
	options := []string{
		"-F", "/dev/null",
		"-o", "BatchMode=yes",
		"-o", "PasswordAuthentication=no",
		"-o", "KbdInteractiveAuthentication=no",
		"-o", "IdentitiesOnly=yes",
		"-o", "IdentityAgent=none",
		"-o", "StrictHostKeyChecking=yes",
		"-o", "UserKnownHostsFile=" + config.KnownHostsFile,
		"-i", config.KeyFile,
	}
	command := exec.CommandContext(ctx, config.Executable, append(options, arguments...)...)
	command.Env = []string{"LC_ALL=C"}
	if config.GitProtocol != "" {
		command.Env = append(command.Env, "GIT_PROTOCOL="+config.GitProtocol)
	}
	command.Stdin = stdin
	command.Stdout = stdout
	command.Stderr = stderr
	command.WaitDelay = time.Second
	if err := command.Run(); err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) && exit.ExitCode() > 0 {
			return exit.ExitCode()
		}
		return 1
	}
	return 0
}

func validSSHTransportConfig(config SSHTransportConfig) bool {
	if !canonicalExecutable(config.Executable, "") || !canonicalPrivateKey(config.KeyFile) ||
		!canonicalKnownHosts(config.KnownHostsFile) || config.ExpectedHost == "" ||
		strings.ContainsAny(config.ExpectedHost, "\x00\r\n/@:") ||
		!validSSHRemotePath(config.RemotePath) {
		return false
	}
	return config.GitProtocol == "" || config.GitProtocol == "version=2"
}

func canonicalPrivateKey(path string) bool {
	if !absoluteCanonical(path) {
		return false
	}
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 || info.Size() < 1 || info.Size() > 4096 {
		return false
	}
	resolved, err := filepath.EvalSymlinks(path)
	return err == nil && resolved == path
}

func validGitSSHArguments(arguments []string, host, remotePath string) bool {
	if len(arguments) == 4 && arguments[0] == "-o" && arguments[1] == "SendEnv=GIT_PROTOCOL" {
		arguments = arguments[2:]
	}
	if len(arguments) != 2 || arguments[0] != "git@"+host {
		return false
	}
	return arguments[1] == "git-receive-pack '"+remotePath+"'" ||
		arguments[1] == "git-upload-pack '"+remotePath+"'"
}

func validSSHRemotePath(path string) bool {
	parts := strings.Split(strings.TrimPrefix(path, "/"), "/")
	return strings.HasPrefix(path, "/") && len(parts) == 2 && githubNamePattern.MatchString(parts[0]) &&
		strings.HasSuffix(parts[1], ".git") && githubNamePattern.MatchString(strings.TrimSuffix(parts[1], ".git"))
}
