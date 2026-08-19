package service

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/comisai/comis-dev-crew/internal/application"
)

func runSurfacingCommand(t *testing.T, args []string) (int, Config, bool, string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	var captured Config
	called := false
	exitCode := RunCommand(context.Background(), append([]string{
		"--database", "/private/tmp/surfacing.db", "--socket", "/private/tmp/surfacing.sock",
	}, args...), &stdout, &stderr, CommandConfig{
		Version: "test-version",
		RunService: func(_ context.Context, config Config) error {
			called = true
			captured = config
			return nil
		},
	})
	return exitCode, captured, called, stderr.String()
}

// How often an unanswered question comes back is a deployment posture, so it is
// configured rather than compiled in. A deployment that says nothing runs the
// reviewed default, which is stated in one place so the flag help and the
// service can never describe different cadences.
func TestRunCommand_ConfiguresTheDecisionResurfacingCadence(t *testing.T) {
	exitCode, config, called, stderr := runSurfacingCommand(t, nil)
	if exitCode != 0 || !called {
		t.Fatalf("RunCommand() exit = %d, called = %t, stderr = %q", exitCode, called, stderr)
	}
	if config.DecisionSurfacing != application.DefaultDecisionSurfacingPolicy {
		t.Fatalf("default cadence = %#v, want %#v", config.DecisionSurfacing, application.DefaultDecisionSurfacingPolicy)
	}

	exitCode, config, called, stderr = runSurfacingCommand(t, []string{
		"--decision-resurface-initial", "15m", "--decision-resurface-maximum", "2h",
	})
	if exitCode != 0 || !called {
		t.Fatalf("RunCommand(explicit cadence) exit = %d, called = %t, stderr = %q", exitCode, called, stderr)
	}
	want := application.DecisionSurfacingPolicy{Initial: 15 * time.Minute, Maximum: 2 * time.Hour}
	if config.DecisionSurfacing != want {
		t.Fatalf("configured cadence = %#v, want %#v", config.DecisionSurfacing, want)
	}
}

// A cadence that could never re-surface sensibly is refused before the service
// starts. Rounding it into the default would leave a deployment running at a
// rate it did not ask for and never told it was ignored.
func TestRunCommand_RefusesAnIncoherentDecisionResurfacingCadence(t *testing.T) {
	for name, args := range map[string][]string{
		"maximum below initial": {"--decision-resurface-initial", "2h", "--decision-resurface-maximum", "15m"},
		"no initial":            {"--decision-resurface-initial", "0"},
		"negative maximum":      {"--decision-resurface-maximum", "-1h"},
	} {
		t.Run(name, func(t *testing.T) {
			exitCode, _, called, stderr := runSurfacingCommand(t, args)
			if exitCode != 2 {
				t.Fatalf("RunCommand() exit = %d, want 2", exitCode)
			}
			if called {
				t.Fatal("an incoherent cadence started the service")
			}
			if !strings.Contains(stderr, "decision re-surfacing cadence") {
				t.Fatalf("stderr = %q, want it to name the cadence", stderr)
			}
			if !strings.Contains(stderr, "Hint:") {
				t.Fatalf("stderr = %q, want an actionable hint", stderr)
			}
		})
	}
}

// The help text is the only place an operator learns the knob exists.
func TestRunCommand_DocumentsTheDecisionResurfacingCadence(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if exitCode := RunCommand(context.Background(), []string{"--help"}, &stdout, &stderr, CommandConfig{}); exitCode != 0 {
		t.Fatalf("RunCommand(--help) exit = %d", exitCode)
	}
	for _, flag := range []string{"--decision-resurface-initial", "--decision-resurface-maximum"} {
		if !strings.Contains(stdout.String(), flag) {
			t.Errorf("help text omits %s", flag)
		}
	}
}
