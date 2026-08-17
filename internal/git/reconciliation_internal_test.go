package git

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLeasePrivateConfigAcceptsBoundedIdentityAndRejectsMalformedValues(t *testing.T) {
	expectedCore := "[core]\n\trepositoryformatversion = 0\n"
	tests := []struct {
		name          string
		configuration string
		wantError     bool
	}{
		{name: "core only", configuration: expectedCore},
		{name: "bounded identity", configuration: expectedCore + "[user]\n\tname = DevCrew Fixture\n\temail = fixture@example.invalid\n"},
		{name: "unsupported section", configuration: expectedCore + "[uploadpack]\n\tallowTipSHA1InWant = true\n", wantError: true},
		{name: "empty user section", configuration: expectedCore + "[user]\n", wantError: true},
		{name: "unindented identity", configuration: expectedCore + "[user]\nname = DevCrew Fixture\n", wantError: true},
		{name: "unknown identity key", configuration: expectedCore + "[user]\n\tsigningkey = example\n", wantError: true},
		{name: "empty identity value", configuration: expectedCore + "[user]\n\tname = \n", wantError: true},
		{name: "oversized identity value", configuration: expectedCore + "[user]\n\tname = " + strings.Repeat("x", 513) + "\n", wantError: true},
		{name: "control character identity", configuration: expectedCore + "[user]\n\tname = invalid\x00value\n", wantError: true},
		{name: "duplicate identity key", configuration: expectedCore + "[user]\n\tname = First\n\tname = Second\n", wantError: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config")
			if err := os.WriteFile(path, []byte(test.configuration), 0o600); err != nil {
				t.Fatal(err)
			}
			err := validateLeasePrivateConfig(path, expectedCore)
			if (err != nil) != test.wantError {
				t.Fatalf("validateLeasePrivateConfig() error = %v, wantError %t", err, test.wantError)
			}
		})
	}
}

func TestGitControlFileHelpersRejectUnsafePathsAndCopyDrift(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "source")
	target := filepath.Join(root, "target")
	if err := compareOptionalGitControlFile(source, target); err != nil {
		t.Fatalf("compare absent optional files: %v", err)
	}
	if err := os.WriteFile(source, []byte("source\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := compareOptionalGitControlFile(source, target); err == nil {
		t.Fatal("compare source-only optional file error = nil")
	}
	if err := os.WriteFile(target, []byte("source\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := compareOptionalGitControlFile(source, target); err != nil {
		t.Fatalf("compare matching optional files: %v", err)
	}
	if err := os.WriteFile(target, []byte("different\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := compareOptionalGitControlFile(source, target); err == nil {
		t.Fatal("compare different optional files error = nil")
	}
	if _, err := readGitControlFile(source, 1); err == nil {
		t.Fatal("read oversized control file error = nil")
	}
	if err := os.Remove(target); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(source, target); err != nil {
		t.Fatal(err)
	}
	if _, err := regularFileExists(target); err == nil {
		t.Fatal("inspect symbolic-link control file error = nil")
	}
	if _, err := readGitControlFile(target, maximumGitControlBytes); err == nil {
		t.Fatal("read symbolic-link control file error = nil")
	}
	if exists, err := regularFileExists(filepath.Join(root, "missing")); err != nil || exists {
		t.Fatalf("inspect missing control file = %t, %v", exists, err)
	}
}
