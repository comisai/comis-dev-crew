package conformance_test

import (
	"path/filepath"
	"runtime"
	"sort"
	"testing"

	"github.com/comisai/comis-dev-crew/internal/comiswire/bundle"
)

func TestProtocolFoundationPinsExactComisBundleAndCorpus(t *testing.T) {
	_, sourceFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve conformance test path")
	}
	protocolRoot := filepath.Clean(filepath.Join(filepath.Dir(sourceFile), "..", "..", "protocol", "comis"))
	pinned, err := bundle.OpenPinned(protocolRoot)
	if err != nil {
		t.Fatalf("open pinned Comis protocol bundle: %v", err)
	}
	if pinned.Manifest.ProtocolID != "comis.capability-service/1" {
		t.Fatalf("protocol identifier = %q", pinned.Manifest.ProtocolID)
	}
	if pinned.Manifest.BundleDigest != "e87e69511ea9e01ea2383cd211f9946233fdbe1ce8edf016e76ce55eae683297" {
		t.Fatalf("bundle digest = %q", pinned.Manifest.BundleDigest)
	}
	if pinned.Provenance.SourceCommit != "5a7e2e9a610b6d25602c11c89c7a5d2817f02373" {
		t.Fatalf("source commit = %q", pinned.Provenance.SourceCommit)
	}
	var fixtureClasses []string
	for _, artifact := range pinned.Manifest.Artifacts {
		if filepath.Dir(filepath.FromSlash(artifact.Path)) == "fixtures" {
			fixtureClasses = append(fixtureClasses, filepath.Base(artifact.Path[:len(artifact.Path)-len(filepath.Ext(artifact.Path))]))
		}
	}
	sort.Strings(fixtureClasses)
	want := []string{"altered-replay", "boundary-size", "digest-mismatch", "invalid", "unknown-field", "valid", "version-mismatch"}
	if len(fixtureClasses) != len(want) {
		t.Fatalf("fixture classes = %v, want %v", fixtureClasses, want)
	}
	for index := range want {
		if fixtureClasses[index] != want[index] {
			t.Fatalf("fixture classes = %v, want %v", fixtureClasses, want)
		}
	}
}
