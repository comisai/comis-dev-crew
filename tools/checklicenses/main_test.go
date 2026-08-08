package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestClassifyLicense_RecognizesMPLVersion2(t *testing.T) {
	directory := t.TempDir()
	licensePath := filepath.Join(directory, "LICENSE")
	contents := "Copyright Example\nMozilla Public License, version 2.0\n"
	if err := os.WriteFile(licensePath, []byte(contents), 0o600); err != nil {
		t.Fatalf("write license fixture: %v", err)
	}

	licenseClass, gotPath, err := classifyLicense(directory)
	if err != nil {
		t.Fatalf("classifyLicense() error = %v", err)
	}
	if licenseClass != "MPL-2.0" {
		t.Fatalf("classifyLicense() class = %q, want MPL-2.0", licenseClass)
	}
	if gotPath != licensePath {
		t.Fatalf("classifyLicense() path = %q, want %q", gotPath, licensePath)
	}
}
