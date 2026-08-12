package forge

import (
	"bytes"
	"context"
	"encoding/base64"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestSSHTransportExecutesOnlyPinnedGitRemoteCommand(t *testing.T) {
	root := canonicalForgeTempDir(t)
	sshExecutable, err := exec.LookPath("echo")
	if err != nil {
		t.Skip("echo executable is unavailable")
	}
	sshExecutable, err = filepath.EvalSymlinks(sshExecutable)
	if err != nil {
		t.Fatal(err)
	}
	keyFile := filepath.Join(root, "deploy-key")
	knownHosts := filepath.Join(root, "known-hosts")
	if err := os.WriteFile(keyFile, []byte("fixture-key"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(knownHosts, []byte("github.com fixture-host-key\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	config := SSHTransportConfig{
		Executable: sshExecutable, KeyFile: keyFile, KnownHostsFile: knownHosts,
		ExpectedHost: "github.com", RemotePath: "/fixture-owner/fixture-repository.git", GitProtocol: "version=2",
	}
	arguments := []string{
		"-o", "SendEnv=GIT_PROTOCOL", "git@github.com",
		"git-receive-pack '/fixture-owner/fixture-repository.git'",
	}
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if exit := RunSSHTransport(context.Background(), arguments, strings.NewReader(""), &stdout, &stderr, config); exit != 0 {
		t.Fatalf("RunSSHTransport() exit = %d, stderr=%q", exit, stderr.String())
	}
	for _, fixed := range []string{
		"-F /dev/null", "BatchMode=yes", "IdentityAgent=none", "StrictHostKeyChecking=yes",
		"UserKnownHostsFile=" + knownHosts, "-i " + keyFile, "git@github.com",
		"git-receive-pack '/fixture-owner/fixture-repository.git'",
	} {
		if !strings.Contains(stdout.String(), fixed) {
			t.Fatalf("SSH argv %q does not contain %q", stdout.String(), fixed)
		}
	}

	for _, invalid := range [][]string{
		{"git@other.example", "git-receive-pack '/fixture-owner/fixture-repository.git'"},
		{"-F", "/tmp/unreviewed", "git@github.com", "git-receive-pack '/fixture-owner/fixture-repository.git'"},
		{"git@github.com", "rm -rf /"},
	} {
		if exit := RunSSHTransport(context.Background(), invalid, strings.NewReader(""), &stdout, &stderr, config); exit != 2 {
			t.Fatalf("RunSSHTransport(%q) exit = %d, want 2", invalid, exit)
		}
	}
}

func TestSSHTransportRelaysGitProtocolInputToPinnedProcess(t *testing.T) {
	root := canonicalForgeTempDir(t)
	sshExecutable := filepath.Join(root, "ssh-fixture")
	if err := os.WriteFile(sshExecutable, []byte("#!/bin/sh\nexec /bin/cat\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	keyFile := filepath.Join(root, "deploy-key")
	knownHosts := filepath.Join(root, "known-hosts")
	if err := os.WriteFile(keyFile, []byte("fixture-key"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(knownHosts, []byte("github.com fixture-host-key\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	config := SSHTransportConfig{
		Executable: sshExecutable, KeyFile: keyFile, KnownHostsFile: knownHosts,
		ExpectedHost: "github.com", RemotePath: "/fixture-owner/fixture-repository.git",
	}
	arguments := []string{"git@github.com", "git-receive-pack '/fixture-owner/fixture-repository.git'"}
	payload := []byte("git-pack-protocol-fixture")
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if exit := RunSSHTransport(context.Background(), arguments, bytes.NewReader(payload), &stdout, &stderr, config); exit != 0 {
		t.Fatalf("RunSSHTransport() exit = %d, stderr=%q", exit, stderr.String())
	}
	if !bytes.Equal(stdout.Bytes(), payload) {
		t.Fatalf("RunSSHTransport() stdout = %q, want relayed protocol input", stdout.Bytes())
	}
}

func TestGitBranchPusherMaterializesBoundedDeployKeyOnlyForOperation(t *testing.T) {
	root := canonicalForgeTempDir(t)
	credentials := filepath.Join(root, "credentials")
	if err := os.Mkdir(credentials, 0o700); err != nil {
		t.Fatal(err)
	}
	transport := copiedForgeExecutable(t, root, "true", "devcrew-service")
	sshExecutable := copiedForgeExecutable(t, root, "true", "ssh")
	knownHosts := filepath.Join(root, "known-hosts")
	if err := os.WriteFile(knownHosts, []byte("github.com fixture-host-key\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	gitExecutable, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git executable is unavailable")
	}
	gitExecutable, err = filepath.EvalSymlinks(gitExecutable)
	if err != nil {
		t.Fatal(err)
	}
	pusher, err := NewGitBranchPusher(GitBranchPusherConfig{
		GitExecutable: gitExecutable, CredentialDirectory: credentials,
		RemoteURL:              "ssh://git@github.com/fixture-owner/fixture-repository.git",
		SSHTransportExecutable: transport, SSHExecutable: sshExecutable, SSHKnownHostsFile: knownHosts,
	})
	if err != nil {
		t.Fatalf("NewGitBranchPusher(SSH) error = %v", err)
	}
	privateKey := "-----BEGIN OPENSSH PRIVATE KEY-----\nfixture\n-----END OPENSSH PRIVATE KEY-----\n"
	path, environment, err := pusher.prepareCredential(base64.StdEncoding.EncodeToString([]byte(privateKey)))
	if err != nil {
		t.Fatalf("prepareCredential(SSH) error = %v", err)
	}
	info, err := os.Lstat(path)
	if err != nil || info.Mode().Perm() != 0o600 || info.Size() != int64(len(privateKey)) {
		t.Fatalf("transient deploy key = %#v, %v", info, err)
	}
	joined := strings.Join(environment, "\n")
	for _, binding := range []string{
		"GIT_SSH=" + transport, "DEV_CREW_SSH_EXECUTABLE=" + sshExecutable,
		"DEV_CREW_SSH_KEY_FILE=" + path, "DEV_CREW_SSH_HOST=github.com",
		"DEV_CREW_SSH_REMOTE_PATH=/fixture-owner/fixture-repository.git",
	} {
		if !strings.Contains(joined, binding) {
			t.Fatalf("SSH environment %q does not contain %q", joined, binding)
		}
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if _, _, err := pusher.prepareCredential("not-base64"); err == nil {
		t.Fatal("prepareCredential(invalid SSH key) error = nil")
	}
	if _, err := pusher.execute(context.Background(), environment, "--version"); err != nil {
		t.Fatalf("execute(SSH environment) error = %v", err)
	}
	if _, err := pusher.execute(context.Background(), []string{"UNREVIEWED=value"}, "--version"); err == nil {
		t.Fatal("execute(unreviewed environment) error = nil")
	}
}

func TestSSHTransportRejectsUnsafeFilesEnvironmentAndProcessFailure(t *testing.T) {
	root := canonicalForgeTempDir(t)
	keyFile := filepath.Join(root, "deploy-key")
	knownHosts := filepath.Join(root, "known-hosts")
	if err := os.WriteFile(keyFile, []byte("fixture-key"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(knownHosts, []byte("github.com fixture-host-key\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	falseExecutable, err := exec.LookPath("false")
	if err != nil {
		t.Skip("false executable is unavailable")
	}
	falseExecutable, err = filepath.EvalSymlinks(falseExecutable)
	if err != nil {
		t.Fatal(err)
	}
	config := SSHTransportConfig{
		Executable: falseExecutable, KeyFile: keyFile, KnownHostsFile: knownHosts,
		ExpectedHost: "github.com", RemotePath: "/fixture-owner/fixture-repository.git",
	}
	arguments := []string{"git@github.com", "git-upload-pack '/fixture-owner/fixture-repository.git'"}
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if exit := RunSSHTransport(context.Background(), arguments, strings.NewReader(""), &stdout, &stderr, config); exit != 1 {
		t.Fatalf("RunSSHTransport(failed process) exit = %d, want 1", exit)
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if exit := RunSSHTransport(cancelled, arguments, strings.NewReader(""), &stdout, &stderr, config); exit != 1 {
		t.Fatalf("RunSSHTransport(cancelled) exit = %d, want 1", exit)
	}
	unsafe := config
	unsafe.GitProtocol = "version=3"
	if exit := RunSSHTransport(context.Background(), arguments, strings.NewReader(""), &stdout, &stderr, unsafe); exit != 2 {
		t.Fatalf("RunSSHTransport(unreviewed protocol) exit = %d, want 2", exit)
	}
	if exit := RunSSHTransport(context.Background(), arguments, strings.NewReader(""), nil, &stderr, config); exit != 2 {
		t.Fatalf("RunSSHTransport(nil output) exit = %d, want 2", exit)
	}
	if err := os.Chmod(keyFile, 0o644); err != nil {
		t.Fatal(err)
	}
	if exit := RunSSHTransport(context.Background(), arguments, strings.NewReader(""), &stdout, &stderr, config); exit != 2 {
		t.Fatalf("RunSSHTransport(public key mode) exit = %d, want 2", exit)
	}
	for _, value := range []string{
		"GIT_SSH=/usr/bin/ssh", "GIT_SSH_VARIANT=ssh", "DEV_CREW_SSH_TRANSPORT=1",
		"DEV_CREW_SSH_EXECUTABLE=/usr/bin/ssh", "DEV_CREW_SSH_KEY_FILE=/private/key",
		"DEV_CREW_SSH_KNOWN_HOSTS_FILE=/private/known", "DEV_CREW_SSH_HOST=github.com",
		"DEV_CREW_SSH_REMOTE_PATH=/owner/repository.git",
	} {
		if !allowedGitEnvironment(value) {
			t.Fatalf("allowedGitEnvironment(%q) = false", value)
		}
	}
	for _, value := range []string{"", "UNKNOWN=value", "GIT_SSH=", "GIT_SSH=/bad\npath"} {
		if allowedGitEnvironment(value) {
			t.Fatalf("allowedGitEnvironment(%q) = true", value)
		}
	}
}

func canonicalForgeTempDir(t *testing.T) string {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return root
}

func copiedForgeExecutable(t *testing.T, root, sourceName, targetName string) string {
	t.Helper()
	source, err := exec.LookPath(sourceName)
	if err != nil {
		t.Skipf("%s executable is unavailable", sourceName)
	}
	source, err = filepath.EvalSymlinks(source)
	if err != nil {
		t.Fatal(err)
	}
	contents, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(root, targetName)
	if err := os.WriteFile(target, contents, 0o700); err != nil {
		t.Fatal(err)
	}
	return target
}
