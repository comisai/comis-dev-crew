package git

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestMaterializationRetryRetiresCrashRecoveryEvidence(t *testing.T) {
	root := t.TempDir()
	name := "component.txt"
	if err := os.WriteFile(filepath.Join(root, name), []byte("expected\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	expected := integrationTreeSnapshot{name: {
		mode: "100644", objectID: "expected-object", contents: []byte("expected\n"),
	}}
	resulting := integrationTreeSnapshot{name: {
		mode: "100644", objectID: "result-object", contents: []byte("result\n"),
	}}
	crashed := false
	func() {
		defer func() { crashed = recover() != nil }()
		_ = materializeIntegrationWorktreeAtBoundary(root, expected, resulting,
			func(boundary string, _ string) {
				if boundary == "before-recovery-retirement" {
					panic("simulated crash")
				}
			})
	}()
	if !crashed {
		t.Fatal("materialization retirement crash boundary was not reached")
	}
	if err := materializeIntegrationWorktree(root, expected, resulting); err != nil {
		t.Fatalf("materializeIntegrationWorktree(retry) error = %v", err)
	}
	recovery := filepath.Join(filepath.Dir(root), ".comis-integration-materialization",
		integrationMaterializationRecoveryIdentity(root, expected, resulting))
	if _, err := os.Lstat(recovery); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("materialization recovery remains after retry: %v", err)
	}
}

func TestMaterializationParentPublicationFaultsRefuseSuccess(t *testing.T) {
	for _, faultDirectory := range []string{"a", filepath.Join("a", "b")} {
		t.Run(faultDirectory, func(t *testing.T) {
			root := t.TempDir()
			resultName := filepath.ToSlash(filepath.Join("a", "b", "component.txt"))
			resulting := integrationTreeSnapshot{resultName: {
				mode: "100644", objectID: "result-object", contents: []byte("result\n"),
			}}
			invoked := false
			err := materializeIntegrationWorktreeAtBoundary(root, integrationTreeSnapshot{}, resulting,
				func(boundary string, name string) {
					if boundary != "parent-durable" || filepath.Clean(name) != filepath.Clean(faultDirectory) {
						return
					}
					invoked = true
					if err := os.Remove(filepath.Join(root, faultDirectory)); err != nil {
						t.Fatal(err)
					}
				})
			if !invoked || err == nil {
				t.Fatalf("materialization parent fault invoked = %t, error = %v", invoked, err)
			}
			if _, statErr := os.Lstat(filepath.Join(root, resultName)); !errors.Is(statErr, os.ErrNotExist) {
				t.Fatalf("result was published after parent fault: %v", statErr)
			}
		})
	}
}
