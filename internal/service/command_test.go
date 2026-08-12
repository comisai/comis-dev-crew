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
	privateCause := errors.New("run candidate supervisor: validate task candidate: pull-request truth is unavailable: private database and socket path detail")
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
	if !strings.Contains(stderr.String(), "Failure class: candidate_pull_request_truth") {
		t.Fatalf("stderr = %q, want safe candidate failure class", stderr.String())
	}
}

func TestRunCommand_ReportsSafeCandidateFailureCauseWithoutPrivateDetails(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	privateDetail := "private worktree path detail"
	exitCode := RunCommand(context.Background(), nil, &stdout, &stderr, CommandConfig{
		DefaultDatabasePath: "/private/tmp/default.db",
		DefaultSocketPath:   "/private/tmp/default.sock",
		RunService: func(context.Context, Config) error {
			return errors.New("run candidate supervisor: validate task candidate: Git evidence is unavailable: " + privateDetail)
		},
	})
	if exitCode != 1 || !strings.Contains(stderr.String(), "Failure cause: candidate_git_evidence_unavailable") {
		t.Fatalf("RunCommand(candidate failure) = %d, stderr=%q", exitCode, stderr.String())
	}
	if strings.Contains(stdout.String()+stderr.String(), privateDetail) {
		t.Fatalf("candidate diagnostic leaked private detail: %q", stderr.String())
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

func TestServiceFailureClassUsesSafeStableCategories(t *testing.T) {
	tests := []struct {
		message string
		want    string
	}{
		{"run candidate supervisor: validate task candidate: pull-request truth is unavailable", "candidate_pull_request_truth"},
		{"run candidate supervisor: candidate evidence was not accepted", "candidate_evidence_rejected"},
		{"run candidate supervisor: durable task queue is unavailable", "candidate_supervision"},
		{"run service validation recovery: unavailable", "validation_process_recovery"},
		{"run service startup reconciliation: unavailable", "startup_reconciliation"},
		{"run service local endpoint: unavailable", "operator_endpoint"},
		{"run service MCP endpoint: unavailable", "mcp_endpoint"},
		{"run service store: unavailable", "state_store"},
		{"unclassified private detail", "service_runtime"},
	}
	for _, test := range tests {
		if got := serviceFailureClass(errors.New(test.message)); got != test.want {
			t.Fatalf("serviceFailureClass() = %q, want %q", got, test.want)
		}
	}
}

func TestRunCommand_ComposesInstalledLaneWithExplicitDeterministicFixture(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	var got Config
	root := shortTempDir(t)
	candidateConfigPath := root + "/candidate.json"
	writeCandidateConfig(t, candidateConfigPath, `{
  "programs":[{"id":"repo-check","executable":"/usr/bin/true"}],
  "profiles":[{"id":"required","localChecks":[{"id":"unit","programId":"repo-check","arguments":[{"kind":"literal","value":"--version"}],"timeout":"2m","required":true}],"forgeChecks":[{"name":"ci/unit","required":true}],"evidenceTtl":"24h"}],
  "maxOutputBytes":65536,"pollInterval":"250ms",
  "forge":{"apiBaseUrl":"https://api.github.com","owner":"comisai","repository":"product-api","remoteUrl":"https://github.com/comisai/product-api.git","readCredentialFile":"/private/config/forge-read.credential","pushCredentialFile":"/private/config/forge-push.credential","credentialDirectory":"/private/run/forge-credentials"}
}`, 0o600)
	exitCode := RunCommand(context.Background(), []string{
		"--database", "/private/state/devcrew.db",
		"--socket", "/private/run/operator.sock",
		"--mcp-socket", "/private/run/mcp.sock",
		"--runtime-root", "/private/run/tasks",
		"--service-instance", "service-instance-fixture",
		"--git-executable", "/usr/bin/git",
		"--approved-root", "/private/repositories",
		"--repository-id", "product-api",
		"--repository-primary", "/private/repositories/product-api",
		"--worktree-root", "/private/repositories/worktrees",
		"--repository-default-branch", "main",
		"--comis-socket", "/private/run/comis-control.sock",
		"--comis-credential-file", "/private/config/comis.credential",
		"--comis-handshake-operation", "handshake-fixture-0001",
		"--preparation-ttl", "15m",
		"--codex-profile", "codex-reviewed",
		"--codex-executable", "/opt/codex/bin/codex",
		"--codex-version", "codex-cli 0.147.0",
		"--codex-model", "gpt-5.5-codex",
		"--codex-effort", "high",
		"--codex-terminal-allow-entry", "codex-confined",
		"--codex-network", "restricted",
		"--codex-concurrency", "2",
		"--claude-profile", "claude-reviewed",
		"--claude-executable", "/opt/claude/bin/claude",
		"--claude-version", "2.1.224 (Claude Code)",
		"--claude-model", "claude-opus-4-6",
		"--claude-effort", "high",
		"--claude-terminal-allow-entry", "claude-confined",
		"--claude-network", "restricted",
		"--claude-concurrency", "2",
		"--claude-config-directory", "/private/config/claude",
		"--candidate-config", candidateConfigPath,
		"--fixture-worker",
		"--fixture-decision", "use the bounded fixture choice",
		"--fixture-artifact", "report.md",
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
	}
	wantComis := &ComisComposition{
		SocketPath: "/private/run/comis-control.sock", CredentialFile: "/private/config/comis.credential",
		HandshakeOperationID: "handshake-fixture-0001",
	}
	wantCodex := &CodexComposition{
		ProfileID: "codex-reviewed", Executable: "/opt/codex/bin/codex",
		ExpectedVersion: "codex-cli 0.147.0", Model: "gpt-5.5-codex", Effort: "high",
		TerminalAllowEntryID: "codex-confined", Network: "restricted", ConcurrencyLimit: 2,
	}
	if got.DatabasePath != "/private/state/devcrew.db" || got.SocketPath != "/private/run/operator.sock" ||
		got.MCPSocketPath != "/private/run/mcp.sock" || got.RuntimeRoot != "/private/run/tasks" || got.ServiceInstanceID != "service-instance-fixture" ||
		got.PreparationTTL != 15*time.Minute || !reflect.DeepEqual(got.RepositoryComposition, wantRepository) ||
		!reflect.DeepEqual(got.ComisComposition, wantComis) || !reflect.DeepEqual(got.CodexComposition, wantCodex) ||
		got.FixtureComposition == nil || got.FixtureComposition.Decision != "use the bounded fixture choice" {
		t.Fatalf("installed service config = %#v", got)
	}
	claude := reflect.ValueOf(got).FieldByName("ClaudeComposition")
	if !claude.IsValid() || claude.IsNil() {
		t.Fatalf("Claude composition is unavailable: %#v", got)
	}
	claude = claude.Elem()
	if claude.FieldByName("ProfileID").String() != "claude-reviewed" ||
		claude.FieldByName("Executable").String() != "/opt/claude/bin/claude" ||
		claude.FieldByName("ExpectedVersion").String() != "2.1.224 (Claude Code)" ||
		claude.FieldByName("ConfigDirectory").String() != "/private/config/claude" {
		t.Fatalf("Claude composition = %#v", claude.Interface())
	}
	artifact := reflect.ValueOf(*got.FixtureComposition).FieldByName("ArtifactRelativePath")
	if !artifact.IsValid() || artifact.String() != "report.md" {
		t.Fatalf("fixture artifact configuration = %#v", got.FixtureComposition)
	}
	if got.ValidationComposition == nil || got.ForgeComposition == nil ||
		got.ValidationComposition.MaxOutputBytes != 64<<10 || got.ValidationComposition.PollInterval != 250*time.Millisecond ||
		got.ForgeComposition.Repository != "product-api" {
		t.Fatalf("candidate service config = %#v, %#v", got.ValidationComposition, got.ForgeComposition)
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
	stdout.Reset()
	stderr.Reset()
	exitCode = RunCommand(context.Background(), []string{
		"--database", "/private/state/devcrew.db", "--socket", "/private/run/operator.sock",
		"--mcp-socket", "/private/run/mcp.sock", "--runtime-root", "/private/run/tasks",
		"--service-instance", "service-instance-reviewed", "--git-executable", "/usr/bin/git",
		"--approved-root", "/private/repositories", "--repository-id", "product-api",
		"--repository-primary", "/private/repositories/product-api", "--worktree-root", "/private/repositories/worktrees",
		"--repository-default-branch", "main", "--comis-socket", "/private/run/comis.sock",
		"--comis-credential-file", "/private/config/comis.credential", "--comis-handshake-operation", "handshake-reviewed",
		"--codex-profile", "codex-reviewed", "--codex-executable", "/opt/codex/bin/codex",
		"--codex-version", "codex-cli 0.147.0", "--codex-model", "gpt-5.5-codex",
		"--codex-terminal-allow-entry", "codex-confined", "--codex-network", "restricted", "--codex-concurrency", "2",
	}, &stdout, &stderr, CommandConfig{})
	if exitCode != 2 || !strings.Contains(stderr.String(), "installed composition is incomplete") {
		t.Fatalf("RunCommand(missing effort) = %d, stderr=%q", exitCode, stderr.String())
	}
	stdout.Reset()
	stderr.Reset()
	exitCode = RunCommand(context.Background(), []string{
		"--database", "/private/state/devcrew.db", "--socket", "/private/run/operator.sock",
		"--fixture-worker",
	}, &stdout, &stderr, CommandConfig{})
	if exitCode != 2 || !strings.Contains(stderr.String(), "deterministic fixture composition is incomplete") {
		t.Fatalf("RunCommand(partial fixture) = %d, stderr=%q", exitCode, stderr.String())
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
