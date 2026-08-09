package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCheckAcceptsExactPinAndGeneratedOutput(t *testing.T) {
	root := filepath.Join("..", "..", "protocol", "comis")
	generated := filepath.Join("..", "..", "internal", "comiswire", "protocol.gen.go")
	if err := check(root, generated); err != nil {
		t.Fatalf("check exact protocol and generation: %v", err)
	}
}

func TestCheckRejectsGeneratedByteDrift(t *testing.T) {
	root := filepath.Join("..", "..", "protocol", "comis")
	generated := filepath.Join(t.TempDir(), "protocol.gen.go")
	if err := os.WriteFile(generated, []byte("// drift\n"), 0o644); err != nil {
		t.Fatalf("write drifted generation: %v", err)
	}
	if err := check(root, generated); err == nil {
		t.Fatal("expected generated drift rejection")
	}
}

func TestCheckRegeneratesInsideTemporaryTreeAndCleansIt(t *testing.T) {
	root := filepath.Join("..", "..", "protocol", "comis")
	generated := filepath.Join("..", "..", "internal", "comiswire", "protocol.gen.go")
	temporaryParent := t.TempDir()
	if err := checkWithTemporaryParent(root, generated, temporaryParent); err != nil {
		t.Fatalf("check with temporary regeneration tree: %v", err)
	}
	entries, err := os.ReadDir(temporaryParent)
	if err != nil {
		t.Fatalf("read temporary regeneration parent: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("temporary regeneration tree was not removed: %v", entries)
	}
}
