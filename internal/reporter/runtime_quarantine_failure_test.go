package reporter

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestQuarantineRuntimePathReleasesPinWhenPreconditionFails(t *testing.T) {
	root := boundaryRuntimeDirectory(t)
	target := filepath.Join(root, "record")
	if err := os.WriteFile(target, []byte("retained"), 0o600); err != nil {
		t.Fatal(err)
	}
	expected := runtimePathTestIdentity(t, target)
	directory := runtimePathTestDirectoryDescriptor(t, root)
	refusal := errors.New("quarantine precondition is unmet")
	err := quarantineRuntimePathWithHooks(
		directory, "record", expected, RuntimePathRegular, 0o600,
		runtimePathMutationHooks{afterPin: func() error { return refusal }}, nil,
	)
	if !errors.Is(err, refusal) {
		t.Fatalf("quarantineRuntimePathWithHooks(refused precondition) error = %v", err)
	}
	if contents, readErr := os.ReadFile(target); readErr != nil || string(contents) != "retained" {
		t.Fatalf("preserved target after refused quarantine = %q, %v", contents, readErr)
	}
}

func TestQuarantineRuntimePathReportsTargetRemovedAfterPin(t *testing.T) {
	root := boundaryRuntimeDirectory(t)
	target := filepath.Join(root, "record")
	if err := os.WriteFile(target, []byte("vanishing"), 0o600); err != nil {
		t.Fatal(err)
	}
	expected := runtimePathTestIdentity(t, target)
	directory := runtimePathTestDirectoryDescriptor(t, root)
	err := quarantineRuntimePathWithHooks(
		directory, "record", expected, RuntimePathRegular, 0o600,
		runtimePathMutationHooks{afterPin: func() error { return os.Remove(target) }}, nil,
	)
	if !errors.Is(err, ErrRuntimePathMissing) {
		t.Fatalf("quarantineRuntimePathWithHooks(removed target) error = %v", err)
	}
}

func TestRuntimePathIsolationRejectsUnsafeExistingDirectory(t *testing.T) {
	root := boundaryRuntimeDirectory(t)
	target := filepath.Join(root, "record")
	if err := os.WriteFile(target, []byte("retained"), 0o600); err != nil {
		t.Fatal(err)
	}
	expected := runtimePathTestIdentity(t, target)
	isolationName := runtimePathQuarantineName("record", expected, RuntimePathRegular, 0o600)
	if err := os.Mkdir(filepath.Join(root, isolationName), 0o755); err != nil {
		t.Fatal(err)
	}
	directory := runtimePathTestDirectoryDescriptor(t, root)
	err := QuarantineRuntimePath(directory, "record", expected, RuntimePathRegular, 0o600)
	if !errors.Is(err, ErrRuntimePathIdentity) {
		t.Fatalf("QuarantineRuntimePath(group-readable isolation) error = %v", err)
	}
	if contents, readErr := os.ReadFile(target); readErr != nil || string(contents) != "retained" {
		t.Fatalf("preserved target after refused isolation = %q, %v", contents, readErr)
	}
}
