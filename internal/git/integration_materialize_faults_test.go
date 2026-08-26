package git

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMaterializeIntegrationWorktreeRefusesUnsafeRootsAndTopologies(t *testing.T) {
	entry := integrationTreeEntry{mode: "100644", objectID: "expected-object", contents: []byte("expected\n")}
	for _, test := range []struct {
		name      string
		worktree  string
		expected  integrationTreeSnapshot
		resulting integrationTreeSnapshot
		want      string
	}{
		{
			name:      "directory transition",
			worktree:  t.TempDir(),
			expected:  integrationTreeSnapshot{"component": entry},
			resulting: integrationTreeSnapshot{"component/nested.txt": entry},
			want:      "materialization directory transition is unsupported",
		},
		{
			name:      "root identity",
			worktree:  string(filepath.Separator),
			expected:  integrationTreeSnapshot{},
			resulting: integrationTreeSnapshot{"component.txt": entry},
			want:      "materialization root identity is invalid",
		},
		{
			name:      "worktree differs",
			worktree:  t.TempDir(),
			expected:  integrationTreeSnapshot{"component.txt": entry},
			resulting: integrationTreeSnapshot{"component.txt": {mode: "100644", objectID: "result-object", contents: []byte("result\n")}},
			want:      "materialization worktree differs",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := materializeIntegrationWorktree(test.worktree, test.expected, test.resulting)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("materializeIntegrationWorktree(%s) error = %v, want %q", test.name, err, test.want)
			}
		})
	}
}

func TestMaterializationRestoresCapturedEntryWhenItsEvidenceDiffers(t *testing.T) {
	parent := t.TempDir()
	worktreePath := filepath.Join(parent, "worktree")
	if err := os.Mkdir(worktreePath, 0o700); err != nil {
		t.Fatal(err)
	}
	name := "component.txt"
	previous := integrationTreeEntry{mode: "100644", objectID: "expected-object", contents: []byte("expected\n")}
	expected := integrationTreeSnapshot{name: previous}
	resulting := integrationTreeSnapshot{}

	// A crashed removal leaves the entry displaced into capture evidence with the
	// target absent. Capture bytes that no longer match the reservation must be
	// restored to the worktree rather than abandoned there.
	recovery := filepath.Join(parent, ".comis-integration-materialization",
		integrationMaterializationRecoveryIdentity(worktreePath, expected, resulting))
	if err := os.MkdirAll(recovery, 0o700); err != nil {
		t.Fatal(err)
	}
	capture := filepath.Join(recovery, materializationEvidenceName("capture", name))
	if err := os.WriteFile(capture, []byte("displaced\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	err := materializeIntegrationWorktree(worktreePath, expected, resulting)
	if err == nil || !strings.Contains(err.Error(), "captured materialization entry differs") {
		t.Fatalf("materializeIntegrationWorktree(displaced capture) error = %v", err)
	}
	contents, readErr := os.ReadFile(filepath.Join(worktreePath, name))
	if readErr != nil || string(contents) != "displaced\n" {
		t.Fatalf("restored entry = %q, %v", contents, readErr)
	}
}

func TestMaterializationRecoveryRejectsUnsafeRecoveryDirectories(t *testing.T) {
	parent := t.TempDir()
	worktreePath := filepath.Join(parent, "worktree")
	if err := os.Mkdir(worktreePath, 0o700); err != nil {
		t.Fatal(err)
	}
	entry := integrationTreeEntry{mode: "100644", objectID: "result-object", contents: []byte("result\n")}
	expected := integrationTreeSnapshot{}
	resulting := integrationTreeSnapshot{"component.txt": entry}
	recoveryParent := filepath.Join(parent, ".comis-integration-materialization")
	recovery := filepath.Join(recoveryParent,
		integrationMaterializationRecoveryIdentity(worktreePath, expected, resulting))
	if err := os.MkdirAll(recovery, 0o755); err != nil {
		t.Fatal(err)
	}

	err := materializeIntegrationWorktree(worktreePath, expected, resulting)
	if err == nil || !strings.Contains(err.Error(), "materialization recovery is invalid") {
		t.Fatalf("materializeIntegrationWorktree(loose recovery mode) error = %v", err)
	}
	if err := os.RemoveAll(recoveryParent); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(recoveryParent, []byte("not a directory\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	err = materializeIntegrationWorktree(worktreePath, expected, resulting)
	if err == nil || !strings.Contains(err.Error(), "materialization recovery is") {
		t.Fatalf("materializeIntegrationWorktree(recovery parent file) error = %v", err)
	}
	if _, statErr := os.Lstat(filepath.Join(worktreePath, "component.txt")); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("entry published despite refused recovery: %v", statErr)
	}
}
