package cli

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

func TestCLI_AcceptedTaskCommandsAppearInPublicHelp(t *testing.T) {
	commands := []struct {
		verb string
		args []string
	}{
		{verb: "show", args: []string{"task", "show", "task-0001"}},
		{verb: "explain", args: []string{"task", "explain", "task-0001"}},
		{verb: "diff", args: []string{"task", "diff", "task-0001"}},
		{verb: "logs", args: []string{"task", "logs", "task-0001"}},
		{verb: "launch-plan", args: []string{"task", "launch-plan", "task-0001"}},
		{verb: "operation", args: []string{"task", "operation", "operation-0001"}},
		{verb: "prepare", args: []string{"task", "prepare", "--input", "-"}},
		{verb: "reconcile", args: []string{"task", "reconcile", "task-0001", "--action", "validate-clean-candidate"}},
		{verb: "handback", args: []string{"task", "handback", "task-0001", "--action", "validate-developer-work"}},
		{verb: "pause", args: []string{"task", "pause", "task-0001"}},
		{verb: "cancel", args: []string{"task", "cancel", "task-0001"}},
		{verb: "resume", args: []string{"task", "resume", "task-0001"}},
		{verb: "verify", args: []string{"task", "verify", "task-0001"}},
		{verb: "attest", args: []string{"task", "attest", "task-0001", "--finding", "no_open_decisions"}},
		{verb: "promote", args: []string{"task", "promote", "task-0001", "--input", "-"}},
		{verb: "replace", args: []string{"task", "replace", "task-0001", "--worker", "fixture-worker"}},
		{verb: "steer", args: []string{"task", "steer", "task-0001", "--input", "-"}},
		{verb: "merge", args: []string{"task", "merge", "task-0001"}},
		{verb: "cleanup", args: []string{"task", "cleanup", "task-0001"}},
		{verb: "discard", args: []string{"task", "discard", "task-0001", "--yes"}},
	}
	var help bytes.Buffer
	if code := Run(context.Background(), []string{"--help"}, &help, &help, Config{}); code != ExitSuccess {
		t.Fatalf("Run(--help) = %d", code)
	}
	for _, command := range commands {
		t.Run(command.verb, func(t *testing.T) {
			if _, err := parseCommand(command.args, "/private/tmp/devcrew.sock"); err != nil {
				t.Fatalf("parseCommand(%v) error = %v", command.args, err)
			}
			if !strings.Contains(help.String(), "task "+command.verb) {
				t.Fatalf("public help omits accepted task command %q", command.verb)
			}
		})
	}
}
