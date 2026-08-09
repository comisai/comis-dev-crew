package bundle

import (
	"fmt"
	"path/filepath"
)

const sourceRepository = "https://github.com/comisai/comis.git"

// OpenPinned verifies both the protocol bundle and its immutable source pin.
func OpenPinned(root string) (PinnedBundle, error) {
	bundle, err := Open(root)
	if err != nil {
		return PinnedBundle{}, err
	}
	contents, err := readRegularFile(filepath.Join(bundle.Root, "provenance.json"))
	if err != nil {
		return PinnedBundle{}, fmt.Errorf("read protocol provenance: %w", err)
	}
	var provenance Provenance
	if err := decodeStrict(contents, &provenance); err != nil {
		return PinnedBundle{}, fmt.Errorf("decode protocol provenance: %w", err)
	}
	if err := VerifyProvenance(bundle.Manifest, provenance); err != nil {
		return PinnedBundle{}, err
	}
	return PinnedBundle{Bundle: bundle, Provenance: provenance}, nil
}

// VerifyProvenance proves that copied bytes and generator identity belong to
// the recorded immutable Comis source.
func VerifyProvenance(manifest Manifest, provenance Provenance) error {
	if provenance.SourceRepository != sourceRepository {
		return fmt.Errorf("protocol provenance names unsupported source repository %q", provenance.SourceRepository)
	}
	if !isLowerHex(provenance.SourceCommit, 40) {
		return fmt.Errorf("protocol provenance source commit is not a full lowercase Git hash")
	}
	if provenance.SourceProtocolPath != SourceProtocolPath {
		return fmt.Errorf("protocol provenance source path is %q", provenance.SourceProtocolPath)
	}
	if provenance.ProtocolID != manifest.ProtocolID || provenance.BundleDigest != manifest.BundleDigest {
		return fmt.Errorf("protocol provenance identity or digest differs from manifest")
	}
	if provenance.Generator != manifest.Generator {
		return fmt.Errorf("protocol provenance generator differs from manifest")
	}
	return nil
}
