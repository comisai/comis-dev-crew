//go:build integration

package integration_test

import (
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestPackagedCommands_ExposeHonestHelpAndVersion(t *testing.T) {
	_, sourceFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve integration test path")
	}
	repositoryRoot := filepath.Clean(filepath.Join(filepath.Dir(sourceFile), "..", ".."))
	for _, name := range []string{"devcrew-service", "devcrew", "devcrew-mcp", "devcrew-report"} {
		name := name
		t.Run(name, func(t *testing.T) {
			binary := filepath.Join(t.TempDir(), name)
			build := exec.Command("go", "build", "-trimpath", "-o", binary, "./cmd/"+name)
			build.Dir = repositoryRoot
			if output, err := build.CombinedOutput(); err != nil {
				t.Fatalf("build %s: %v\n%s", name, err, output)
			}
			for _, argument := range []string{"--help", "--version"} {
				output, err := exec.Command(binary, argument).CombinedOutput()
				if err != nil {
					t.Fatalf("run %s %s: %v\n%s", name, argument, err, output)
				}
				if !strings.Contains(string(output), name) {
					t.Fatalf("%s %s output does not identify command: %q", name, argument, output)
				}
			}
		})
	}
}

func TestPackagedCommands_ExposeInjectedReleaseVersion(t *testing.T) {
	_, sourceFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve integration test path")
	}
	repositoryRoot := filepath.Clean(filepath.Join(filepath.Dir(sourceFile), "..", ".."))
	const version = "v9.8.7-test"
	for _, name := range []string{"devcrew-service", "devcrew", "devcrew-mcp", "devcrew-report"} {
		name := name
		t.Run(name, func(t *testing.T) {
			binary := filepath.Join(t.TempDir(), name)
			build := exec.Command(
				"go", "build", "-trimpath",
				"-ldflags", "-X github.com/comisai/comis-dev-crew/internal/command.Version="+version,
				"-o", binary, "./cmd/"+name,
			)
			build.Dir = repositoryRoot
			if output, err := build.CombinedOutput(); err != nil {
				t.Fatalf("build versioned %s: %v\n%s", name, err, output)
			}
			output, err := exec.Command(binary, "--version").CombinedOutput()
			if err != nil {
				t.Fatalf("run versioned %s: %v\n%s", name, err, output)
			}
			if got, want := string(output), name+" "+version+"\n"; got != want {
				t.Fatalf("versioned %s output = %q, want %q", name, got, want)
			}
		})
	}
}
