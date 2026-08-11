package forge

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestGitBranchPusher_PushesExactHeadAndRemovesCredentialMaterial(t *testing.T) {
	gitExecutable, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git executable is unavailable")
	}
	gitExecutable, err = filepath.EvalSymlinks(gitExecutable)
	if err != nil {
		t.Fatalf("resolve git executable: %v", err)
	}
	root := t.TempDir()
	primary := filepath.Join(root, "primary")
	remote := filepath.Join(root, "remote.git")
	credentials := filepath.Join(root, "credentials")
	for _, path := range []string{primary, credentials} {
		if err := os.Mkdir(path, 0o700); err != nil {
			t.Fatalf("create fixture directory: %v", err)
		}
	}
	runFixtureGit(t, gitExecutable, root, "init", "--bare", "--initial-branch=main", remote)
	runFixtureGit(t, gitExecutable, primary, "init", "--initial-branch=main")
	runFixtureGit(t, gitExecutable, primary, "config", "user.email", "fixture@example.com")
	runFixtureGit(t, gitExecutable, primary, "config", "user.name", "Fixture")
	if err := os.WriteFile(filepath.Join(primary, "README.md"), []byte("fixture\n"), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	runFixtureGit(t, gitExecutable, primary, "add", "README.md")
	runFixtureGit(t, gitExecutable, primary, "commit", "-m", "fixture")
	runFixtureGit(t, gitExecutable, primary, "checkout", "-b", "devcrew/task-fixture")
	head := fixtureGitOutput(t, gitExecutable, primary, "rev-parse", "HEAD")
	pusher, err := NewGitBranchPusher(GitBranchPusherConfig{
		GitExecutable: gitExecutable, RemoteURL: "file://" + remote,
		CredentialDirectory: credentials, LocalFixtureRemoteRoot: root,
	})
	if err != nil {
		t.Fatalf("NewGitBranchPusher() error = %v", err)
	}
	request := BranchPushRequest{
		OperationID: "deliver-fixture", WorktreePath: primary,
		Branch: "devcrew/task-fixture", HeadRevision: head,
	}
	credential := Credential{Kind: CredentialPush, Secret: "push-token", Scopes: []CredentialScope{ScopeContentsWrite}}
	if err := pusher.Push(context.Background(), credential, request); err != nil {
		t.Fatalf("Push() error = %v", err)
	}
	if err := pusher.Push(context.Background(), credential, request); err != nil {
		t.Fatalf("Push(replay) error = %v", err)
	}
	remoteHead := fixtureGitOutput(t, gitExecutable, primary, "ls-remote", "file://"+remote, "refs/heads/devcrew/task-fixture")
	if !strings.HasPrefix(remoteHead, head+"\t") {
		t.Fatalf("remote head = %q, want %q", remoteHead, head)
	}
	entries, err := os.ReadDir(credentials)
	if err != nil {
		t.Fatalf("read credential directory: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("credential files remain: %#v", entries)
	}
}

func TestGitBranchPusher_RefusesDirtyWrongHeadAndEscapedRemote(t *testing.T) {
	if _, err := NewGitBranchPusher(GitBranchPusherConfig{}); err == nil {
		t.Fatal("NewGitBranchPusher(empty) error = nil")
	}
	gitExecutable, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git executable is unavailable")
	}
	gitExecutable, _ = filepath.EvalSymlinks(gitExecutable)
	root := t.TempDir()
	credentials := filepath.Join(root, "credentials")
	if err := os.Mkdir(credentials, 0o700); err != nil {
		t.Fatalf("create credentials: %v", err)
	}
	if _, err := NewGitBranchPusher(GitBranchPusherConfig{
		GitExecutable: gitExecutable, RemoteURL: "file:///outside/remote.git",
		CredentialDirectory: credentials, LocalFixtureRemoteRoot: root,
	}); err == nil {
		t.Fatal("NewGitBranchPusher(escaped remote) error = nil")
	}
}

func runFixtureGit(t *testing.T, executable, directory string, arguments ...string) {
	t.Helper()
	command := exec.Command(executable, arguments...)
	command.Dir = directory
	command.Env = []string{"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1", "LC_ALL=C"}
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v: %s", arguments, err, output)
	}
}

func fixtureGitOutput(t *testing.T, executable, directory string, arguments ...string) string {
	t.Helper()
	command := exec.Command(executable, arguments...)
	command.Dir = directory
	command.Env = []string{"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1", "LC_ALL=C"}
	output, err := command.Output()
	if err != nil {
		t.Fatalf("git %v: %v", arguments, err)
	}
	return strings.TrimSpace(string(output))
}
