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
	if pinned.Manifest.BundleDigest != "fff96cf5105d9cda9da5dfd2fbc7e9f15242754f63d7f8155cde4ef874d5c52b" {
		t.Fatalf("bundle digest = %q", pinned.Manifest.BundleDigest)
	}
	if pinned.Provenance.SourceCommit != "b0b8065c0b43a29840dcd21fcc15ef37e4b905d4" {
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
