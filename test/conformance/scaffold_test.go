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
	if pinned.Manifest.BundleDigest != "dea251a955a4d68faf402aa6977db1b4544737e43aa1f624f39dc359008f6414" {
		t.Fatalf("bundle digest = %q", pinned.Manifest.BundleDigest)
	}
	if pinned.Provenance.SourceCommit != "4deb33ed59b272d4a84046a20a7f51a615f06039" {
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
