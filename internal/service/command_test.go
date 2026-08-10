package service

import (
	"bytes"
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestRunCommand_HelpVersionAndStrictArguments(t *testing.T) {
	tests := []struct {
		name       string
		args       []string
		wantExit   int
		wantOutput string
		wantError  string
	}{
		{name: "help", args: []string{"--help"}, wantExit: 0, wantOutput: "Usage: devcrew-service"},
		{name: "version", args: []string{"--version"}, wantExit: 0, wantOutput: "devcrew-service test-version"},
		{name: "unknown flag", args: []string{"--private-token-value"}, wantExit: 2, wantError: "invalid service arguments"},
		{name: "positional argument", args: []string{"unexpected-private-value"}, wantExit: 2, wantError: "invalid service arguments"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var stdout bytes.Buffer
			var stderr bytes.Buffer
			called := false
			exitCode := RunCommand(context.Background(), test.args, &stdout, &stderr, CommandConfig{
				DefaultDatabasePath: "/private/tmp/default.db",
				DefaultSocketPath:   "/private/tmp/default.sock",
				Version:             "test-version",
				RunService: func(context.Context, Config) error {
					called = true
					return nil
				},
			})
			if exitCode != test.wantExit {
				t.Fatalf("RunCommand() exit = %d, want %d", exitCode, test.wantExit)
			}
			if !strings.Contains(stdout.String(), test.wantOutput) {
				t.Fatalf("stdout = %q, want %q", stdout.String(), test.wantOutput)
			}
			if !strings.Contains(stderr.String(), test.wantError) {
				t.Fatalf("stderr = %q, want %q", stderr.String(), test.wantError)
			}
			if called {
				t.Fatal("RunService called for non-running command")
			}
			for _, privateValue := range []string{"private-token-value", "unexpected-private-value"} {
				if strings.Contains(stdout.String()+stderr.String(), privateValue) {
					t.Fatalf("diagnostic leaked rejected argument %q", privateValue)
				}
			}
		})
	}
}

func TestRunCommand_UsesExplicitPathsAndSafeFailure(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	wantDatabase := "/private/tmp/explicit.db"
	wantSocket := "/private/tmp/explicit.sock"
	privateCause := errors.New("private database and socket path detail")
	called := false
	exitCode := RunCommand(context.Background(), []string{
		"--database", wantDatabase,
		"--socket", wantSocket,
	}, &stdout, &stderr, CommandConfig{
		DefaultDatabasePath: "/private/tmp/default.db",
		DefaultSocketPath:   "/private/tmp/default.sock",
		Version:             "test-version",
		RunService: func(_ context.Context, config Config) error {
			called = true
			if config.DatabasePath != wantDatabase || config.SocketPath != wantSocket {
				t.Fatalf("service config = %#v, want explicit paths", config)
			}
			return privateCause
		},
	})
	if !called {
		t.Fatal("RunService was not called")
	}
	if exitCode != 1 {
		t.Fatalf("RunCommand() exit = %d, want 1", exitCode)
	}
	if strings.Contains(stderr.String(), privateCause.Error()) || strings.Contains(stderr.String(), wantDatabase) || strings.Contains(stderr.String(), wantSocket) {
		t.Fatalf("stderr leaked private service detail: %q", stderr.String())
	}
	if !strings.Contains(stderr.String(), "service stopped with an error") || !strings.Contains(stderr.String(), "inspect local configuration and service health") {
		t.Fatalf("stderr = %q, want safe actionable error", stderr.String())
	}
}

func TestRunCommand_RejectsMissingCompositionDependencies(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if exit := RunCommand(context.Background(), nil, &stdout, &stderr, CommandConfig{}); exit != 2 {
		t.Fatalf("RunCommand(empty config) exit = %d, want 2", exit)
	}
	if !strings.Contains(stderr.String(), "service paths are not configured") {
		t.Fatalf("stderr = %q, want missing-path diagnostic", stderr.String())
	}
}

func TestRunCommand_ComposesInstalledComisFixtureLaneFromExplicitConfiguration(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	var got Config
	exitCode := RunCommand(context.Background(), []string{
		"--database", "/private/state/devcrew.db",
		"--socket", "/private/run/operator.sock",
		"--mcp-socket", "/private/run/mcp.sock",
		"--service-instance", "service-instance-fixture",
		"--git-executable", "/usr/bin/git",
		"--approved-root", "/private/repositories",
		"--repository-id", "product-api",
		"--repository-primary", "/private/repositories/product-api",
		"--worktree-root", "/private/repositories/worktrees",
		"--repository-default-branch", "main",
		"--workspace-root", "/private/repositories/worktrees/managed-run-fixture",
		"--comis-socket", "/private/run/comis-control.sock",
		"--comis-credential-file", "/private/config/comis.credential",
		"--comis-handshake-operation", "handshake-fixture-0001",
		"--preparation-ttl", "15m",
		"--fixture-worker",
		"--fixture-decision", "use the bounded fixture choice",
	}, &stdout, &stderr, CommandConfig{
		RunService: func(_ context.Context, config Config) error {
			got = config
			return nil
		},
	})
	if exitCode != 0 {
		t.Fatalf("RunCommand() exit = %d, stderr=%q", exitCode, stderr.String())
	}
	wantRepository := &RepositoryComposition{
		GitExecutable: "/usr/bin/git", ApprovedRoot: "/private/repositories",
		RepositoryID: "product-api", PrimaryCheckout: "/private/repositories/product-api",
		WorktreeRoot: "/private/repositories/worktrees", DefaultBranch: "main",
		WorkspaceRoot: "/private/repositories/worktrees/managed-run-fixture",
	}
	wantComis := &ComisComposition{
		SocketPath: "/private/run/comis-control.sock", CredentialFile: "/private/config/comis.credential",
		HandshakeOperationID: "handshake-fixture-0001",
	}
	wantFixture := &FixtureComposition{Decision: "use the bounded fixture choice"}
	if got.DatabasePath != "/private/state/devcrew.db" || got.SocketPath != "/private/run/operator.sock" ||
		got.MCPSocketPath != "/private/run/mcp.sock" || got.ServiceInstanceID != "service-instance-fixture" ||
		got.PreparationTTL != 15*time.Minute || !reflect.DeepEqual(got.RepositoryComposition, wantRepository) ||
		!reflect.DeepEqual(got.ComisComposition, wantComis) || !reflect.DeepEqual(got.FixtureComposition, wantFixture) {
		t.Fatalf("installed service config = %#v", got)
	}
}

func TestRunCommand_RejectsPartialInstalledCompositionWithoutLeakingValues(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	privateValue := "/private/run/comis-secret.sock"
	exitCode := RunCommand(context.Background(), []string{
		"--database", "/private/state/devcrew.db", "--socket", "/private/run/operator.sock",
		"--comis-socket", privateValue,
	}, &stdout, &stderr, CommandConfig{})
	if exitCode != 2 || !strings.Contains(stderr.String(), "installed composition is incomplete") {
		t.Fatalf("RunCommand(partial) = %d, stderr=%q", exitCode, stderr.String())
	}
	if strings.Contains(stdout.String()+stderr.String(), privateValue) {
		t.Fatalf("partial-composition diagnostic leaked private value: %q", stderr.String())
	}
}

func TestRunCommand_SuccessAndDiagnosticWriterFailures(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	called := false
	exitCode := RunCommand(context.Background(), nil, &stdout, &stderr, CommandConfig{
		DefaultDatabasePath: "/private/tmp/default.db",
		DefaultSocketPath:   "/private/tmp/default.sock",
		RunService: func(context.Context, Config) error {
			called = true
			return nil
		},
	})
	if exitCode != 0 || !called {
		t.Fatalf("RunCommand(success) = %d, called=%v, want 0 and called", exitCode, called)
	}
	stdout.Reset()
	if exit := RunCommand(context.Background(), []string{"--version"}, &stdout, &stderr, CommandConfig{}); exit != 0 || stdout.String() != "devcrew-service dev\n" {
		t.Fatalf("RunCommand(default version) = %d, %q", exit, stdout.String())
	}
	if exit := writeServiceDiagnostic(nil, "message", 0); exit != 1 {
		t.Fatalf("writeServiceDiagnostic(nil) = %d, want 1", exit)
	}
	if exit := writeServiceDiagnostic(errorWriter{}, "message", 0); exit != 1 {
		t.Fatalf("writeServiceDiagnostic(failing) = %d, want 1", exit)
	}
}

type errorWriter struct{}

func (errorWriter) Write([]byte) (int, error) {
	return 0, errors.New("write failed")
}
