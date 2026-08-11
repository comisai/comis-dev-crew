package bundle

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const maxProtocolFileBytes = 2 * 1024 * 1024

// Open verifies a manifest, its artifact inventory, every artifact hash, and
// the language-neutral aggregate digest.
func Open(root string) (Bundle, error) {
	canonicalRoot, err := canonicalDirectory(root)
	if err != nil {
		return Bundle{}, err
	}
	manifestBytes, err := readRegularFile(filepath.Join(canonicalRoot, "manifest.json"))
	if err != nil {
		return Bundle{}, fmt.Errorf("read manifest: %w", err)
	}
	var manifest Manifest
	if err := decodeStrict(manifestBytes, &manifest); err != nil {
		return Bundle{}, fmt.Errorf("decode manifest: %w", err)
	}
	if err := validateManifest(manifest); err != nil {
		return Bundle{}, err
	}
	if err := verifyArtifacts(canonicalRoot, manifest); err != nil {
		return Bundle{}, err
	}
	return Bundle{Root: canonicalRoot, Manifest: manifest}, nil
}

// CalculateBundleDigest applies the Comis path-NUL-hash-newline algorithm.
func CalculateBundleDigest(artifacts []Artifact) (string, error) {
	ordered := append([]Artifact(nil), artifacts...)
	sort.Slice(ordered, func(left, right int) bool {
		return ordered[left].Path < ordered[right].Path
	})
	digest := sha256.New()
	for _, artifact := range ordered {
		if err := validateArtifact(artifact); err != nil {
			return "", err
		}
		_, _ = digest.Write([]byte(artifact.Path))
		_, _ = digest.Write([]byte{0})
		_, _ = digest.Write([]byte(artifact.SHA256))
		_, _ = digest.Write([]byte{'\n'})
	}
	return hex.EncodeToString(digest.Sum(nil)), nil
}

func validateManifest(manifest Manifest) error {
	if manifest.ProtocolID == "" || len(manifest.ProtocolID) > 128 {
		return fmt.Errorf("manifest protocol identifier is empty or oversized")
	}
	if manifest.BundleDigestAlgorithm != DigestAlgorithm {
		return fmt.Errorf("unsupported bundle digest algorithm %q", manifest.BundleDigestAlgorithm)
	}
	if !isLowerHex(manifest.BundleDigest, 64) {
		return fmt.Errorf("manifest bundle digest is not lowercase SHA-256")
	}
	if len(manifest.Artifacts) == 0 {
		return fmt.Errorf("manifest artifact inventory is empty")
	}
	if manifest.Generator.Command == "" || manifest.Generator.Package == "" || manifest.Generator.Version == "" {
		return fmt.Errorf("manifest generator identity is incomplete")
	}
	if manifest.FixtureDigestToken == "" || manifest.MCPMeta.CallContextKey == "" || manifest.MCPMeta.ManagedRunResultKey == "" {
		return fmt.Errorf("manifest metadata contract is incomplete")
	}
	if err := validateLimits(manifest.Limits); err != nil {
		return err
	}
	if err := validateErrors(manifest); err != nil {
		return err
	}
	return validateMethods(manifest)
}

func validateLimits(limits Limits) error {
	values := []int{
		limits.MaxEvidenceBytes,
		limits.MaxInFlightRequests,
		limits.MaxLineBytes,
		limits.MaxReportBytes,
		limits.MaxRequestBytes,
		limits.MaxResponseBytes,
		limits.ReportRetentionDays,
	}
	for _, value := range values {
		if value <= 0 {
			return fmt.Errorf("manifest limit must be positive")
		}
	}
	if limits.MaxReportBytes > limits.MaxRequestBytes || limits.MaxRequestBytes > limits.MaxLineBytes {
		return fmt.Errorf("manifest request limits are contradictory")
	}
	if limits.MaxResponseBytes > limits.MaxLineBytes {
		return fmt.Errorf("manifest response limit exceeds line limit")
	}
	return nil
}

func validateErrors(manifest Manifest) error {
	if len(manifest.ErrorKinds) == 0 || len(manifest.ErrorKinds) != len(manifest.Errors) {
		return fmt.Errorf("manifest error catalog is incomplete")
	}
	seenKinds := make(map[string]struct{}, len(manifest.ErrorKinds))
	for _, kind := range manifest.ErrorKinds {
		if kind == "" {
			return fmt.Errorf("manifest error kind is empty")
		}
		if _, exists := seenKinds[kind]; exists {
			return fmt.Errorf("duplicate manifest error kind %q", kind)
		}
		seenKinds[kind] = struct{}{}
	}
	seenCodes := make(map[int]struct{}, len(manifest.Errors))
	for index, definition := range manifest.Errors {
		if definition.Kind != manifest.ErrorKinds[index] {
			return fmt.Errorf("error definition order differs from closed error-kind order")
		}
		if _, exists := seenCodes[definition.Code]; exists {
			return fmt.Errorf("duplicate manifest error code %d", definition.Code)
		}
		seenCodes[definition.Code] = struct{}{}
	}
	return nil
}

func validateMethods(manifest Manifest) error {
	if len(manifest.Methods) == 0 || len(manifest.Methods) != len(manifest.MethodCatalog) {
		return fmt.Errorf("manifest method catalog is incomplete")
	}
	artifacts := make(map[string]struct{}, len(manifest.Artifacts))
	for _, artifact := range manifest.Artifacts {
		artifacts[artifact.Path] = struct{}{}
	}
	catalog := make(map[string]Method, len(manifest.MethodCatalog))
	for _, method := range manifest.MethodCatalog {
		if method.Name == "" {
			return fmt.Errorf("manifest method catalog has an empty name")
		}
		if _, exists := catalog[method.Name]; exists {
			return fmt.Errorf("duplicate manifest method %q", method.Name)
		}
		catalog[method.Name] = method
	}
	seen := make(map[string]struct{}, len(manifest.Methods))
	for _, name := range manifest.Methods {
		method, exists := catalog[name]
		if name == "" || !exists {
			return fmt.Errorf("manifest method %q is absent from its catalog", name)
		}
		if _, exists := seen[name]; exists {
			return fmt.Errorf("duplicate manifest method %q", name)
		}
		seen[name] = struct{}{}
		if !oneOf(method.CallerClass, "both", "capability-service", "comis-daemon") {
			return fmt.Errorf("method %q has unknown caller class %q", name, method.CallerClass)
		}
		if !oneOf(method.Classification, "mutation", "read") {
			return fmt.Errorf("method %q has unknown classification %q", name, method.Classification)
		}
		if !oneOf(method.Direction, "bidirectional", "comis-to-service", "service-to-comis") {
			return fmt.Errorf("method %q has unknown direction %q", name, method.Direction)
		}
		if method.RequiredServiceScope != nil && !oneOf(*method.RequiredServiceScope, "evidence", "health", "report", "workspace_lease") {
			return fmt.Errorf("method %q has unknown service scope %q", name, *method.RequiredServiceScope)
		}
		if !method.OperationIDRequired || method.MaxRequestBytes != manifest.Limits.MaxRequestBytes || method.MaxResponseBytes != manifest.Limits.MaxResponseBytes || len(method.SemanticInvariants) == 0 {
			return fmt.Errorf("method %q has incomplete bounds or invariants", name)
		}
		invariants := make(map[string]struct{}, len(method.SemanticInvariants))
		for _, invariant := range method.SemanticInvariants {
			if invariant == "" {
				return fmt.Errorf("method %q has an empty semantic invariant", name)
			}
			if _, exists := invariants[invariant]; exists {
				return fmt.Errorf("method %q repeats semantic invariant %q", name, invariant)
			}
			invariants[invariant] = struct{}{}
		}
		if !strings.HasPrefix(method.RequestSchema, "schemas/") || !strings.HasSuffix(method.RequestSchema, ".schema.json") {
			return fmt.Errorf("method %q request schema path is not a schema artifact", name)
		}
		if !strings.HasPrefix(method.ResponseSchema, "schemas/") || !strings.HasSuffix(method.ResponseSchema, ".schema.json") {
			return fmt.Errorf("method %q response schema path is not a schema artifact", name)
		}
		if _, exists := artifacts[method.RequestSchema]; !exists {
			return fmt.Errorf("method %q request schema is not inventoried", name)
		}
		if _, exists := artifacts[method.ResponseSchema]; !exists {
			return fmt.Errorf("method %q response schema is not inventoried", name)
		}
	}
	return nil
}

func verifyArtifacts(root string, manifest Manifest) error {
	expected := make(map[string]struct{}, len(manifest.Artifacts))
	previous := ""
	for _, artifact := range manifest.Artifacts {
		if err := validateArtifact(artifact); err != nil {
			return err
		}
		if previous != "" && artifact.Path <= previous {
			return fmt.Errorf("manifest artifacts are not strictly lexically ordered")
		}
		previous = artifact.Path
		expected[artifact.Path] = struct{}{}
		contents, err := readRegularFile(filepath.Join(root, filepath.FromSlash(artifact.Path)))
		if err != nil {
			return fmt.Errorf("read artifact %q: %w", artifact.Path, err)
		}
		sum := sha256.Sum256(contents)
		if hex.EncodeToString(sum[:]) != artifact.SHA256 {
			return fmt.Errorf("artifact %q SHA-256 does not match manifest", artifact.Path)
		}
	}
	actual, err := protocolJSONInventory(root)
	if err != nil {
		return err
	}
	if len(actual) != len(expected) {
		return fmt.Errorf("protocol JSON inventory has %d artifacts, manifest has %d", len(actual), len(expected))
	}
	for _, path := range actual {
		if _, exists := expected[path]; !exists {
			return fmt.Errorf("unlisted protocol artifact %q", path)
		}
	}
	digest, err := CalculateBundleDigest(manifest.Artifacts)
	if err != nil {
		return err
	}
	if digest != manifest.BundleDigest {
		return fmt.Errorf("bundle digest %q does not match calculated %q", manifest.BundleDigest, digest)
	}
	return nil
}

func validateArtifact(artifact Artifact) error {
	if !filepath.IsLocal(filepath.FromSlash(artifact.Path)) || filepath.ToSlash(filepath.Clean(filepath.FromSlash(artifact.Path))) != artifact.Path {
		return fmt.Errorf("artifact path %q is not normalized and local", artifact.Path)
	}
	if !(strings.HasPrefix(artifact.Path, "fixtures/") || strings.HasPrefix(artifact.Path, "schemas/")) || !strings.HasSuffix(artifact.Path, ".json") {
		return fmt.Errorf("artifact path %q is outside the protocol inventory", artifact.Path)
	}
	if !isLowerHex(artifact.SHA256, 64) {
		return fmt.Errorf("artifact %q hash is not lowercase SHA-256", artifact.Path)
	}
	return nil
}

func protocolJSONInventory(root string) ([]string, error) {
	var inventory []string
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("protocol tree contains symlink %q", path)
		}
		if entry.IsDir() || filepath.Ext(path) != ".json" {
			return nil
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		relative = filepath.ToSlash(relative)
		if relative != "manifest.json" && relative != "provenance.json" {
			inventory = append(inventory, relative)
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("inspect protocol inventory: %w", err)
	}
	sort.Strings(inventory)
	return inventory, nil
}

func canonicalDirectory(root string) (string, error) {
	absolute, err := filepath.Abs(root)
	if err != nil {
		return "", fmt.Errorf("resolve protocol root: %w", err)
	}
	canonical, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return "", fmt.Errorf("canonicalize protocol root: %w", err)
	}
	info, err := os.Stat(canonical)
	if err != nil {
		return "", fmt.Errorf("inspect protocol root: %w", err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("protocol root is not a directory")
	}
	return canonical, nil
}

func readRegularFile(path string) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("file is not regular")
	}
	if info.Size() > maxProtocolFileBytes {
		return nil, fmt.Errorf("file exceeds %d bytes", maxProtocolFileBytes)
	}
	return os.ReadFile(path)
}

func isLowerHex(value string, length int) bool {
	if len(value) != length {
		return false
	}
	for _, character := range value {
		if !((character >= '0' && character <= '9') || (character >= 'a' && character <= 'f')) {
			return false
		}
	}
	return true
}

func oneOf(value string, allowed ...string) bool {
	for _, candidate := range allowed {
		if value == candidate {
			return true
		}
	}
	return false
}
