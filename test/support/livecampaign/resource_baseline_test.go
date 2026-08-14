package livecampaign

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestResourceBaselineRoundTripBindsCampaignAndRefusesOverwrite(t *testing.T) {
	manifest := validManifest()
	want, err := NewResourceBaseline(manifest, validResourceObservation(manifest).Started)
	if err != nil {
		t.Fatalf("NewResourceBaseline() error = %v", err)
	}
	root := t.TempDir()
	if err := os.Chmod(root, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "resource-baseline.json")
	if err := WriteResourceBaseline(path, want); err != nil {
		t.Fatalf("WriteResourceBaseline() error = %v", err)
	}
	got, err := LoadResourceBaseline(path)
	if err != nil {
		t.Fatalf("LoadResourceBaseline() error = %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("resource baseline = %#v, want %#v", got, want)
	}
	if err := VerifyResourceBaseline(manifest, got); err != nil {
		t.Fatalf("VerifyResourceBaseline() error = %v", err)
	}
	if err := WriteResourceBaseline(path, want); err == nil || !strings.Contains(err.Error(), "exists") {
		t.Fatalf("expected overwrite refusal, got %v", err)
	}
}

func TestLoadResourceBaselineRequiresOwnerPrivateRegularFile(t *testing.T) {
	manifest := validManifest()
	baseline, err := NewResourceBaseline(manifest, validResourceObservation(manifest).Started)
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	if err := os.Chmod(root, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "resource-baseline.json")
	if err := WriteResourceBaseline(path, baseline); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadResourceBaseline(path); err == nil || !strings.Contains(err.Error(), "owner-private") {
		t.Fatalf("expected permission refusal, got %v", err)
	}
}
