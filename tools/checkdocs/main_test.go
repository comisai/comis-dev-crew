package main

import (
	"os"
	"path/filepath"
	"testing"
)

func writeDoc(t *testing.T, name, contents string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatalf("write fixture %s: %v", name, err)
	}
	return path
}

func TestCheckMarkdown_WhenFrontMatterPrecedesTheHeading_AcceptsTheDocument(t *testing.T) {
	path := writeDoc(t, "SKILL.md", "---\nname: example\nversion: 1.0.0\n---\n\n# Example\n\nBody.\n")

	problems, err := checkMarkdown(path)

	if err != nil {
		t.Fatalf("check markdown: %v", err)
	}
	if len(problems) != 0 {
		t.Errorf("expected no problems, got %v", problems)
	}
}

func TestCheckMarkdown_WhenAMarkerCommentPrecedesTheHeading_AcceptsTheDocument(t *testing.T) {
	path := writeDoc(t, "ROLE.md", "<!-- COMIS-TEMPLATE -->\n# Role\n\nBody.\n")

	problems, err := checkMarkdown(path)

	if err != nil {
		t.Fatalf("check markdown: %v", err)
	}
	if len(problems) != 0 {
		t.Errorf("expected no problems, got %v", problems)
	}
}

func TestCheckMarkdown_WhenNoHeadingFollowsThePreamble_ReportsTheMissingHeading(t *testing.T) {
	path := writeDoc(t, "SKILL.md", "---\nname: example\n---\n\nBody without a heading.\n")

	problems, err := checkMarkdown(path)

	if err != nil {
		t.Fatalf("check markdown: %v", err)
	}
	if len(problems) != 1 {
		t.Fatalf("expected one problem, got %v", problems)
	}
}

func TestCheckMarkdown_WhenAnUnterminatedPreambleHidesTheBody_ReportsTheMissingHeading(t *testing.T) {
	path := writeDoc(t, "SKILL.md", "---\nname: example\n\n# Not reached\n")

	problems, err := checkMarkdown(path)

	if err != nil {
		t.Fatalf("check markdown: %v", err)
	}
	if len(problems) != 1 {
		t.Fatalf("expected one problem, got %v", problems)
	}
}
