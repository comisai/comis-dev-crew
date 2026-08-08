package command_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/comisai/comis-dev-crew/internal/command"
)

func TestRun_HelpDescribesScaffoldWithoutClaimingServiceBehavior(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	exitCode := command.Run("devcrew", []string{"--help"}, &stdout, &stderr)

	if exitCode != 0 {
		t.Fatalf("help exit code = %d, want 0", exitCode)
	}
	if !strings.Contains(stdout.String(), "Pre-release scaffold") {
		t.Fatalf("help output does not disclose scaffold status: %q", stdout.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("help stderr = %q, want empty", stderr.String())
	}
}

func TestRun_VersionIdentifiesRequestedCompositionRoot(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	exitCode := command.Run("devcrew-service", []string{"--version"}, &stdout, &stderr)

	if exitCode != 0 {
		t.Fatalf("version exit code = %d, want 0", exitCode)
	}
	if got, want := stdout.String(), "devcrew-service dev\n"; got != want {
		t.Fatalf("version output = %q, want %q", got, want)
	}
}

func TestRun_UnknownArgumentReturnsUsageFailureWithoutSideEffects(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	exitCode := command.Run("devcrew-report", []string{"--unknown"}, &stdout, &stderr)

	if exitCode != 2 {
		t.Fatalf("unknown argument exit code = %d, want 2", exitCode)
	}
	if stdout.Len() != 0 {
		t.Fatalf("unknown argument stdout = %q, want empty", stdout.String())
	}
	if !strings.Contains(stderr.String(), "unknown argument") {
		t.Fatalf("unknown argument stderr = %q", stderr.String())
	}
}

func TestRun_UnknownArgumentDoesNotEchoUntrustedTerminalContent(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	untrusted := "\x1b]8;;https://example.com\x07credential-shaped-input\x1b]8;;\x07"

	exitCode := command.Run("devcrew", []string{untrusted}, &stdout, &stderr)

	if exitCode != 2 {
		t.Fatalf("unknown argument exit code = %d, want 2", exitCode)
	}
	if strings.Contains(stderr.String(), untrusted) || strings.Contains(stderr.String(), "credential-shaped-input") {
		t.Fatalf("stderr echoed untrusted argument content: %q", stderr.String())
	}
}
