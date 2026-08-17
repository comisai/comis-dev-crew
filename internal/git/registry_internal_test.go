package git

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestPathValidation_RejectsMissingSpecialSymlinkAndControlPaths(t *testing.T) {
	root := internalCanonicalTempDir(t)
	regular := filepath.Join(root, "regular")
	if err := os.WriteFile(regular, nil, 0o600); err != nil {
		t.Fatalf("write regular fixture: %v", err)
	}
	linked := filepath.Join(root, "linked")
	if err := os.Symlink(root, linked); err != nil {
		t.Fatalf("create symlink fixture: %v", err)
	}
	tests := []struct {
		name string
		path string
	}{
		{name: "empty", path: ""},
		{name: "relative", path: "relative"},
		{name: "control", path: root + "\n"},
		{name: "too long", path: string(filepath.Separator) + strings.Repeat("x", maximumConfiguredPathBytes+1)},
		{name: "missing", path: filepath.Join(root, "missing")},
		{name: "regular file", path: regular},
		{name: "symlink", path: linked},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := validateCanonicalDirectory(test.path); err == nil {
				t.Fatal("validateCanonicalDirectory() error = nil")
			}
		})
	}
}

func TestGitExecutableValidation_RequiresCanonicalRegularExecutable(t *testing.T) {
	executable := internalGitExecutable(t)
	if err := validateGitExecutable(executable); err != nil {
		t.Fatalf("validateGitExecutable(valid) error = %v", err)
	}
	root := internalCanonicalTempDir(t)
	nonExecutable := filepath.Join(root, "not-executable")
	if err := os.WriteFile(nonExecutable, nil, 0o600); err != nil {
		t.Fatalf("write non-executable fixture: %v", err)
	}
	linked := filepath.Join(root, "linked-executable")
	if err := os.Symlink(executable, linked); err != nil {
		t.Fatalf("link executable fixture: %v", err)
	}
	for _, path := range []string{nonExecutable, linked, root, filepath.Join(root, "missing")} {
		if err := validateGitExecutable(path); err == nil {
			t.Fatalf("validateGitExecutable(%q) error = nil", filepath.Base(path))
		}
	}
}

func TestGitInspectionRunner_IsBoundedCancellableAndContentFreeOnFailure(t *testing.T) {
	executable := internalGitExecutable(t)
	output, err := runGit(context.Background(), executable, "--version")
	if err != nil || !strings.HasPrefix(output, "git version ") {
		t.Fatalf("runGit(--version) = %q, %v", output, err)
	}
	if _, err := runGit(context.Background(), executable, "not-a-real-inspection-command"); err == nil {
		t.Fatal("runGit(invalid) error = nil")
	}
	if _, err := runGit(context.Background(), "/not/an/executable"); err == nil {
		t.Fatal("runGit(unavailable executable) error = nil")
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := runGit(cancelled, executable, "--version"); !errors.Is(err, context.Canceled) {
		t.Fatalf("runGit(cancelled) error = %v, want context.Canceled", err)
	}
	//lint:ignore SA1012 The private boundary test proves nil cannot reach os/exec.
	if _, err := runGit(nil, executable, "--version"); err == nil {
		t.Fatal("runGit(nil) error = nil")
	}
	if output, err := runGitBytes(context.Background(), "/bin/sh", "-c", `printf 'machine-output\n'`); err != nil || string(output) != "machine-output\n" {
		t.Fatalf("runGitBytes(success) = %q, %v", output, err)
	}
	if _, err := runGitBytes(context.Background(), "/bin/sh", "-c", `exit 2`); err == nil {
		t.Fatal("runGitBytes(failure) error = nil")
	}
	if _, err := runGitBytes(context.Background(), "/not/an/executable"); err == nil {
		t.Fatal("runGitBytes(unavailable executable) error = nil")
	}
	if matched, err := gitPredicate(context.Background(), "/bin/sh", "-c", `exit 0`); err != nil || !matched {
		t.Fatalf("gitPredicate(true) = %t, %v", matched, err)
	}
	if matched, err := gitPredicate(context.Background(), "/bin/sh", "-c", `exit 1`); err != nil || matched {
		t.Fatalf("gitPredicate(false) = %t, %v", matched, err)
	}
	if _, err := gitPredicate(context.Background(), "/bin/sh", "-c", `exit 2`); err == nil {
		t.Fatal("gitPredicate(unexpected exit) error = nil")
	}
	if _, err := gitPredicate(context.Background(), "/not/an/executable"); err == nil {
		t.Fatal("gitPredicate(unavailable executable) error = nil")
	}

	buffer := &boundedBuffer{limit: 4}
	if written, err := buffer.Write([]byte("1234")); err != nil || written != 4 {
		t.Fatalf("bounded write = %d, %v", written, err)
	}
	if written, err := buffer.Write([]byte("5")); !errors.Is(err, errGitOutputTooLarge) || written != 0 {
		t.Fatalf("full bounded write = %d, %v", written, err)
	}
	partial := &boundedBuffer{limit: 3}
	if written, err := partial.Write([]byte("12345")); !errors.Is(err, errGitOutputTooLarge) || written != 3 {
		t.Fatalf("partial bounded write = %d, %v", written, err)
	}
}

func TestWorkspaceGitRunner_PropagatesEnvironmentAndNormalizesCommandOutcomes(t *testing.T) {
	environment := gitWorkspaceEnvironment{
		gitDir:      "/example/git-dir",
		gitWorkTree: "/example/work-tree",
		gitIndex:    "/example/git-index",
	}
	output, err := runGitInWorkspace(
		context.Background(),
		"/bin/sh",
		environment,
		"-c",
		`printf '%s' "$GIT_DIR|$GIT_WORK_TREE|$GIT_INDEX_FILE"`,
	)
	if err != nil || output != "/example/git-dir|/example/work-tree|/example/git-index" {
		t.Fatalf("runGitInWorkspace(environment) = %q, %v", output, err)
	}
	bytesOutput, err := runGitBytesInWorkspace(
		context.Background(),
		"/bin/sh",
		environment,
		"-c",
		`printf 'machine-output\n'`,
	)
	if err != nil || string(bytesOutput) != "machine-output\n" {
		t.Fatalf("runGitBytesInWorkspace(success) = %q, %v", bytesOutput, err)
	}

	for _, command := range []string{`exit 2`, `printf 'first\nsecond\n'`, `printf ''`} {
		if _, err := runGitInWorkspace(context.Background(), "/bin/sh", environment, "-c", command); err == nil {
			t.Fatalf("runGitInWorkspace(%q) error = nil", command)
		}
	}
	if _, err := runGitInWorkspace(context.Background(), "/not/an/executable", environment); err == nil {
		t.Fatal("runGitInWorkspace(unavailable executable) error = nil")
	}
	if _, err := runGitBytesInWorkspace(context.Background(), "/bin/sh", environment, "-c", `exit 2`); err == nil {
		t.Fatal("runGitBytesInWorkspace(failure) error = nil")
	}
	if _, err := runGitBytesInWorkspace(context.Background(), "/not/an/executable", environment); err == nil {
		t.Fatal("runGitBytesInWorkspace(unavailable executable) error = nil")
	}
	if _, err := runGitBytesInWorkspace(context.Background(), "/bin/sh", environment, "-c", `printf '%*s' 8193 ''`); !errors.Is(err, errGitOutputTooLarge) {
		t.Fatalf("runGitBytesInWorkspace(oversized output) error = %v", err)
	}

	if matched, err := gitPredicateInWorkspace(context.Background(), "/bin/sh", environment, "-c", `exit 0`); err != nil || !matched {
		t.Fatalf("gitPredicateInWorkspace(true) = %t, %v", matched, err)
	}
	if matched, err := gitPredicateInWorkspace(context.Background(), "/bin/sh", environment, "-c", `exit 1`); err != nil || matched {
		t.Fatalf("gitPredicateInWorkspace(false) = %t, %v", matched, err)
	}
	if _, err := gitPredicateInWorkspace(context.Background(), "/bin/sh", environment, "-c", `exit 2`); err == nil {
		t.Fatal("gitPredicateInWorkspace(unexpected exit) error = nil")
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := gitPredicateInWorkspace(cancelled, "/bin/sh", environment, "-c", `exit 0`); !errors.Is(err, context.Canceled) {
		t.Fatalf("gitPredicateInWorkspace(cancelled) error = %v, want context.Canceled", err)
	}
}

func TestGitMarkersAndIdentity_RejectWrongArtifactKinds(t *testing.T) {
	root := internalCanonicalTempDir(t)
	if err := validatePrimaryMarker(root); err == nil {
		t.Fatal("validatePrimaryMarker(missing) error = nil")
	}
	marker := filepath.Join(root, ".git")
	if err := os.WriteFile(marker, nil, 0o600); err != nil {
		t.Fatalf("write marker fixture: %v", err)
	}
	if err := validatePrimaryMarker(root); err == nil {
		t.Fatal("validatePrimaryMarker(file) error = nil")
	}
	if err := validateWorktreeMarker(root); err != nil {
		t.Fatalf("validateWorktreeMarker(file) error = %v", err)
	}
	if err := os.Remove(marker); err != nil {
		t.Fatalf("remove marker fixture: %v", err)
	}
	if err := os.Mkdir(marker, 0o700); err != nil {
		t.Fatalf("create marker directory: %v", err)
	}
	if err := validateWorktreeMarker(root); err == nil {
		t.Fatal("validateWorktreeMarker(directory) error = nil")
	}
	if identity, err := commonDirectoryIdentity(marker); err != nil || len(identity) != 64 {
		t.Fatalf("commonDirectoryIdentity() = %q, %v", identity, err)
	}
	if _, err := commonDirectoryIdentity(filepath.Join(root, "missing")); err == nil ||
		!errors.Is(err, errFilesystemInfrastructure) {
		t.Fatalf("commonDirectoryIdentity(missing) error = %v", err)
	}
}

func TestRegistryResolve_RejectsUnavailableRegistry(t *testing.T) {
	var registry *Registry
	if _, err := registry.Resolve("product-api"); err == nil {
		t.Fatal("Resolve() error = nil for unavailable registry")
	}
}

func internalGitExecutable(t *testing.T) string {
	t.Helper()
	executable, err := exec.LookPath("git")
	if err != nil {
		t.Fatalf("find Git executable: %v", err)
	}
	executable, err = filepath.EvalSymlinks(executable)
	if err != nil {
		t.Fatalf("canonicalize Git executable: %v", err)
	}
	return executable
}

func internalCanonicalTempDir(t *testing.T) string {
	t.Helper()
	path, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("canonicalize temporary directory: %v", err)
	}
	return path
}
