package fuzz_test

import (
	"bytes"
	"testing"

	"github.com/comisai/comis-dev-crew/internal/command"
)

func FuzzCommandArguments(f *testing.F) {
	f.Add("--help")
	f.Add("--version")
	f.Add("--unknown")
	f.Fuzz(func(t *testing.T, argument string) {
		if len(argument) > 1024 {
			t.Skip()
		}
		var stdout bytes.Buffer
		var stderr bytes.Buffer
		exitCode := command.Run("devcrew", []string{argument}, &stdout, &stderr)
		if exitCode != 0 && exitCode != 2 {
			t.Fatalf("exit code = %d, want 0 or 2", exitCode)
		}
		if stdout.Len()+stderr.Len() > 2048 {
			t.Fatalf("output size = %d, want at most 2048", stdout.Len()+stderr.Len())
		}
	})
}
