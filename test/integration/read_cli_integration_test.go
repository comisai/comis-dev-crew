//go:build integration

package integration_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/comisai/comis-dev-crew/internal/cli"
	"github.com/comisai/comis-dev-crew/internal/service"
)

func TestReadOnlyCLI_OperatesThroughRealServiceSocket(t *testing.T) {
	root := integrationShortTempDir(t)
	socketPath := filepath.Join(root, "run", "devcrew.sock")
	databasePath := filepath.Join(root, "state", "devcrew.db")
	ready := make(chan struct{})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- service.Run(ctx, service.Config{
			SocketPath: socketPath, DatabasePath: databasePath,
			Ready: func() { close(ready) },
		})
	}()
	select {
	case <-ready:
	case err := <-done:
		t.Fatalf("service exited before readiness: %v", err)
	}
	t.Cleanup(func() {
		cancel()
		if err := <-done; err != nil {
			t.Errorf("service.Run() error = %v", err)
		}
	})

	binary := filepath.Join(root, "devcrew")
	build := exec.Command("go", "build", "-trimpath", "-o", binary, "./cmd/devcrew")
	build.Dir = integrationRepositoryRoot(t)
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build devcrew: %v\n%s", err, output)
	}

	for _, args := range [][]string{
		{"--socket", socketPath, "doctor", "--format", "json"},
		{"--socket", socketPath, "status", "--format", "json"},
		{"--socket", socketPath, "tasks", "list", "--format", "json"},
	} {
		output, err := exec.Command(binary, args...).CombinedOutput()
		if err != nil {
			t.Fatalf("devcrew %v: %v\n%s", args, err, output)
		}
		var projection map[string]any
		if err := json.Unmarshal(output, &projection); err != nil {
			t.Fatalf("devcrew %v output = %q: %v", args, output, err)
		}
		if projection["schemaVersion"] != float64(1) {
			t.Fatalf("devcrew %v schemaVersion = %#v", args, projection["schemaVersion"])
		}
	}

	output, err := exec.Command(binary, "--socket", socketPath, "service", "status").CombinedOutput()
	if err != nil || !strings.Contains(string(output), "healthy") {
		t.Fatalf("devcrew service status = %q, %v", output, err)
	}

	output, err = exec.Command(binary, "--socket", socketPath, "task", "operation", "op-missing").CombinedOutput()
	var exitError *exec.ExitError
	if !errors.As(err, &exitError) || exitError.ExitCode() != cli.ExitRejected {
		t.Fatalf("missing operation error = %v, output=%q, want exit %d", err, output, cli.ExitRejected)
	}
	if !strings.Contains(string(output), "not_found") || strings.Contains(string(output), databasePath) {
		t.Fatalf("missing operation diagnostic = %q, want safe not-found", output)
	}
}

func integrationRepositoryRoot(t *testing.T) string {
	t.Helper()
	_, sourceFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve integration source path")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(sourceFile), "..", ".."))
}

func integrationShortTempDir(t *testing.T) string {
	t.Helper()
	directory, err := os.MkdirTemp("/tmp", "dci-")
	if err != nil {
		t.Fatalf("create integration temporary directory: %v", err)
	}
	t.Cleanup(func() {
		if err := os.RemoveAll(directory); err != nil {
			t.Errorf("remove integration temporary directory: %v", err)
		}
	})
	resolved, err := filepath.EvalSymlinks(directory)
	if err != nil {
		t.Fatalf("resolve integration temporary directory: %v", err)
	}
	return resolved
}
