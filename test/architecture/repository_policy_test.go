package architecture_test

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func repositoryRoot(t *testing.T) string {
	t.Helper()
	_, sourceFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve repository-policy test source path")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(sourceFile), "..", ".."))
}

func readRepositoryFile(t *testing.T, name string) string {
	t.Helper()
	contents, err := os.ReadFile(filepath.Join(repositoryRoot(t), name))
	if err != nil {
		t.Fatalf("read required repository file %s: %v", name, err)
	}
	return string(contents)
}

func TestRepositoryPolicy_AuthoritativeInstructionsRemainPresent(t *testing.T) {
	agents := readRepositoryFile(t, "AGENTS.md")
	requiredSections := []string{
		"## Architecture and authority",
		"## Go engineering",
		"## Security",
		"## Testing and verification",
		"## Commits and external actions",
	}
	for _, section := range requiredSections {
		if !strings.Contains(agents, section) {
			t.Errorf("AGENTS.md is missing required section %q", section)
		}
	}
	if !strings.Contains(agents, "Never add a `Co-Authored-By:` trailer") {
		t.Error("AGENTS.md must permanently prohibit Co-Authored-By trailers")
	}
}

func TestRepositoryPolicy_ClaudeNotesRemainSubordinate(t *testing.T) {
	claude := readRepositoryFile(t, "CLAUDE.md")
	required := "Read and follow `AGENTS.md` before making changes. `AGENTS.md` is the authoritative repository protocol and wins every conflict."
	if !strings.Contains(claude, required) {
		t.Fatalf("CLAUDE.md must retain the AGENTS.md precedence declaration")
	}
}
