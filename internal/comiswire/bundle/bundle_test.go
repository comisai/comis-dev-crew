package bundle

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

func TestCalculateBundleDigestMatchesComisRecordEncoding(t *testing.T) {
	artifacts := []Artifact{
		{Path: "schemas/b.json", SHA256: strings.Repeat("b", 64)},
		{Path: "fixtures/a.json", SHA256: strings.Repeat("a", 64)},
	}

	got, err := CalculateBundleDigest(artifacts)
	if err != nil {
		t.Fatalf("calculate bundle digest: %v", err)
	}
	const want = "ca27e0cc39edf96b9b333af9a143daa9752161194a0ccc3781e7bd59d46f5cf2"
	if got != want {
		t.Fatalf("bundle digest = %q, want %q", got, want)
	}
}

func TestOpenVerifiesArtifactsInventoryAndManifestDigest(t *testing.T) {
	root, expected := writeFixtureBundle(t)

	bundle, err := Open(root)
	if err != nil {
		t.Fatalf("open verified bundle: %v", err)
	}
	if bundle.Manifest.ProtocolID != "comis.capability-service/1" {
		t.Fatalf("protocol ID = %q", bundle.Manifest.ProtocolID)
	}
	if bundle.Manifest.BundleDigest != expected {
		t.Fatalf("bundle digest = %q, want %q", bundle.Manifest.BundleDigest, expected)
	}
	if len(bundle.Manifest.Artifacts) != 3 {
		t.Fatalf("artifact count = %d, want 3", len(bundle.Manifest.Artifacts))
	}
}

func TestOpenRejectsProtocolArtifactDriftAndUnlistedJSON(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(t *testing.T, root string)
	}{
		{
			name: "changed artifact bytes",
			mutate: func(t *testing.T, root string) {
				writeFile(t, filepath.Join(root, "fixtures", "valid.json"), []byte("{\"changed\":true}\n"))
			},
		},
		{
			name: "unlisted JSON artifact",
			mutate: func(t *testing.T, root string) {
				writeFile(t, filepath.Join(root, "fixtures", "unlisted.json"), []byte("{}\n"))
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root, _ := writeFixtureBundle(t)
			test.mutate(t, root)
			if _, err := Open(root); err == nil {
				t.Fatal("expected bundle verification failure")
			}
		})
	}
}

func TestManifestRejectsOpenEndedMethodAndErrorCatalogs(t *testing.T) {
	root, _ := writeFixtureBundle(t)
	verified, err := Open(root)
	if err != nil {
		t.Fatalf("open fixture bundle: %v", err)
	}
	tests := []struct {
		name   string
		mutate func(*Manifest)
	}{
		{
			name: "unknown caller class",
			mutate: func(manifest *Manifest) {
				manifest.MethodCatalog[0].CallerClass = "operator"
			},
		},
		{
			name: "unknown classification",
			mutate: func(manifest *Manifest) {
				manifest.MethodCatalog[0].Classification = "write"
			},
		},
		{
			name: "unknown direction",
			mutate: func(manifest *Manifest) {
				manifest.MethodCatalog[0].Direction = "sideways"
			},
		},
		{
			name: "request schema is not a schema artifact",
			mutate: func(manifest *Manifest) {
				manifest.MethodCatalog[0].RequestSchema = "fixtures/valid.json"
			},
		},
		{
			name: "empty semantic invariant",
			mutate: func(manifest *Manifest) {
				manifest.MethodCatalog[0].SemanticInvariants = []string{""}
			},
		},
		{
			name: "error kind order differs from definitions",
			mutate: func(manifest *Manifest) {
				manifest.ErrorKinds = []string{"invalid_request", "internal_error"}
				manifest.Errors = []ErrorDefinition{
					{Code: -32603, Kind: "internal_error", Retryable: true},
					{Code: -32600, Kind: "invalid_request", Retryable: false},
				}
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			manifest := cloneManifest(verified.Manifest)
			test.mutate(&manifest)
			if err := validateManifest(manifest); err == nil {
				t.Fatal("expected closed catalog validation failure")
			}
		})
	}
}

func TestManifestRejectsIncompleteIdentityLimitsAndCatalogEntries(t *testing.T) {
	root, _ := writeFixtureBundle(t)
	verified, err := Open(root)
	if err != nil {
		t.Fatalf("open fixture bundle: %v", err)
	}
	tests := []struct {
		name   string
		mutate func(*Manifest)
	}{
		{name: "empty protocol identifier", mutate: func(manifest *Manifest) { manifest.ProtocolID = "" }},
		{name: "unknown digest algorithm", mutate: func(manifest *Manifest) { manifest.BundleDigestAlgorithm = "other" }},
		{name: "malformed bundle digest", mutate: func(manifest *Manifest) { manifest.BundleDigest = "ABC" }},
		{name: "empty artifacts", mutate: func(manifest *Manifest) { manifest.Artifacts = nil }},
		{name: "incomplete generator", mutate: func(manifest *Manifest) { manifest.Generator.Command = "" }},
		{name: "incomplete MCP metadata", mutate: func(manifest *Manifest) { manifest.MCPMeta.CallContextKey = "" }},
		{name: "nonpositive limit", mutate: func(manifest *Manifest) { manifest.Limits.MaxLineBytes = 0 }},
		{name: "report exceeds request", mutate: func(manifest *Manifest) { manifest.Limits.MaxReportBytes = manifest.Limits.MaxRequestBytes + 1 }},
		{name: "response exceeds line", mutate: func(manifest *Manifest) { manifest.Limits.MaxResponseBytes = manifest.Limits.MaxLineBytes + 1 }},
		{name: "empty error catalog", mutate: func(manifest *Manifest) { manifest.ErrorKinds = nil; manifest.Errors = nil }},
		{name: "duplicate error kind", mutate: func(manifest *Manifest) {
			manifest.ErrorKinds = []string{"invalid_request", "invalid_request"}
			manifest.Errors = append(manifest.Errors, manifest.Errors[0])
		}},
		{name: "unknown error definition", mutate: func(manifest *Manifest) { manifest.Errors[0].Kind = "unknown" }},
		{name: "method count mismatch", mutate: func(manifest *Manifest) { manifest.Methods = nil }},
		{name: "method name mismatch", mutate: func(manifest *Manifest) { manifest.MethodCatalog[0].Name = "other" }},
		{name: "operation identity optional", mutate: func(manifest *Manifest) { manifest.MethodCatalog[0].OperationIDRequired = false }},
		{name: "missing request schema", mutate: func(manifest *Manifest) { manifest.MethodCatalog[0].RequestSchema = "schemas/missing.json" }},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			manifest := cloneManifest(verified.Manifest)
			test.mutate(&manifest)
			if err := validateManifest(manifest); err == nil {
				t.Fatal("expected incomplete manifest rejection")
			}
		})
	}
}

func TestOpenRejectsMalformedDuplicateAndTrailingManifestJSON(t *testing.T) {
	tests := []struct {
		name   string
		mutate func([]byte) []byte
	}{
		{
			name: "unknown manifest field",
			mutate: func(contents []byte) []byte {
				return []byte(strings.Replace(string(contents), "\n}", ",\n  \"unknown\": true\n}", 1))
			},
		},
		{
			name: "duplicate protocol identifier",
			mutate: func(contents []byte) []byte {
				return []byte(strings.Replace(string(contents), "\n}", ",\n  \"protocolId\": \"duplicate\"\n}", 1))
			},
		},
		{
			name:   "trailing JSON",
			mutate: func(contents []byte) []byte { return append(contents, []byte("{}\n")...) },
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root, _ := writeFixtureBundle(t)
			path := filepath.Join(root, "manifest.json")
			contents, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read manifest fixture: %v", err)
			}
			writeFile(t, path, test.mutate(contents))
			if _, err := Open(root); err == nil {
				t.Fatal("expected strict JSON rejection")
			}
		})
	}
}

func TestOpenPinnedRequiresExactProvenance(t *testing.T) {
	root, digest := writeFixtureBundle(t)
	pin := fmt.Sprintf(`{
  "sourceRepository": "https://github.com/comisai/comis.git",
  "sourceCommit": "%s",
  "sourceProtocolPath": "packages/capability-service-sdk/protocol",
  "protocolId": "comis.capability-service/1",
  "bundleDigest": "%s",
  "generator": {
    "command": "pnpm capability-protocol:generate",
    "package": "@comis/capability-service-sdk",
    "version": "1.0.59"
  }
}
`, strings.Repeat("c", 40), digest)
	writeFile(t, filepath.Join(root, "provenance.json"), []byte(pin))

	pinned, err := OpenPinned(root)
	if err != nil {
		t.Fatalf("open pinned bundle: %v", err)
	}
	if pinned.Provenance.SourceCommit != strings.Repeat("c", 40) {
		t.Fatalf("source commit = %q", pinned.Provenance.SourceCommit)
	}

	changed := strings.Replace(pin, digest, strings.Repeat("d", 64), 1)
	writeFile(t, filepath.Join(root, "provenance.json"), []byte(changed))
	if _, err := OpenPinned(root); err == nil {
		t.Fatal("expected provenance mismatch rejection")
	}
}

func TestVerifyProvenanceRejectsEveryAuthorityMismatch(t *testing.T) {
	root, digest := writeFixtureBundle(t)
	verified, err := Open(root)
	if err != nil {
		t.Fatalf("open fixture bundle: %v", err)
	}
	valid := Provenance{
		SourceRepository:   "https://github.com/comisai/comis.git",
		SourceCommit:       strings.Repeat("c", 40),
		SourceProtocolPath: SourceProtocolPath,
		ProtocolID:         verified.Manifest.ProtocolID,
		BundleDigest:       digest,
		Generator:          verified.Manifest.Generator,
	}
	tests := []struct {
		name   string
		mutate func(*Provenance)
	}{
		{name: "wrong repository", mutate: func(pin *Provenance) { pin.SourceRepository = "https://example.com/comis.git" }},
		{name: "short source commit", mutate: func(pin *Provenance) { pin.SourceCommit = "abc" }},
		{name: "wrong source path", mutate: func(pin *Provenance) { pin.SourceProtocolPath = "protocol" }},
		{name: "wrong protocol identifier", mutate: func(pin *Provenance) { pin.ProtocolID = "other" }},
		{name: "wrong bundle digest", mutate: func(pin *Provenance) { pin.BundleDigest = strings.Repeat("d", 64) }},
		{name: "wrong generator", mutate: func(pin *Provenance) { pin.Generator.Version = "2.0.0" }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			pin := valid
			test.mutate(&pin)
			if err := VerifyProvenance(verified.Manifest, pin); err == nil {
				t.Fatal("expected provenance authority rejection")
			}
		})
	}
}

func TestCalculateBundleDigestRejectsUnsafeArtifactRecords(t *testing.T) {
	for _, artifact := range []Artifact{
		{Path: "../escape.json", SHA256: strings.Repeat("a", 64)},
		{Path: "other/value.json", SHA256: strings.Repeat("a", 64)},
		{Path: "fixtures/value.txt", SHA256: strings.Repeat("a", 64)},
		{Path: "fixtures/value.json", SHA256: "ABC"},
	} {
		if _, err := CalculateBundleDigest([]Artifact{artifact}); err == nil {
			t.Fatalf("expected unsafe artifact rejection for %+v", artifact)
		}
	}
}

func cloneManifest(source Manifest) Manifest {
	clone := source
	clone.Artifacts = append([]Artifact(nil), source.Artifacts...)
	clone.ErrorKinds = append([]string(nil), source.ErrorKinds...)
	clone.Errors = append([]ErrorDefinition(nil), source.Errors...)
	clone.Methods = append([]string(nil), source.Methods...)
	clone.MethodCatalog = append([]Method(nil), source.MethodCatalog...)
	for index := range clone.MethodCatalog {
		clone.MethodCatalog[index].SemanticInvariants = append([]string(nil), source.MethodCatalog[index].SemanticInvariants...)
	}
	return clone
}

func writeFixtureBundle(t *testing.T) (string, string) {
	t.Helper()
	root := t.TempDir()
	files := map[string][]byte{
		"fixtures/valid.json":                    []byte("{}\n"),
		"schemas/handshake.request.schema.json":  []byte("{}\n"),
		"schemas/handshake.response.schema.json": []byte("{}\n"),
	}
	paths := make([]string, 0, len(files))
	for path, contents := range files {
		writeFile(t, filepath.Join(root, filepath.FromSlash(path)), contents)
		paths = append(paths, path)
	}
	sort.Strings(paths)
	artifacts := make([]string, 0, len(paths))
	digestInput := make([]byte, 0)
	for _, path := range paths {
		sum := sha256.Sum256(files[path])
		hash := hex.EncodeToString(sum[:])
		artifacts = append(artifacts, fmt.Sprintf(`{"path":%q,"sha256":%q}`, path, hash))
		digestInput = append(digestInput, path...)
		digestInput = append(digestInput, 0)
		digestInput = append(digestInput, hash...)
		digestInput = append(digestInput, '\n')
	}
	bundleSum := sha256.Sum256(digestInput)
	digest := hex.EncodeToString(bundleSum[:])
	manifest := fmt.Sprintf(`{
  "artifacts": [%s],
  "bundleDigest": %q,
  "bundleDigestAlgorithm": "sha256 over lexically ordered path, NUL, hash, newline records",
  "errorKinds": ["invalid_request"],
  "errors": [{"code":-32600,"kind":"invalid_request","retryable":false}],
  "fixtureDigestToken": "__BUNDLE_DIGEST__",
  "generator": {"command":"pnpm capability-protocol:generate","package":"@comis/capability-service-sdk","version":"1.0.59"},
  "limits": {"maxEvidenceBytes":1048576,"maxInFlightRequests":32,"maxLineBytes":65536,"maxReportBytes":16384,"maxRequestBytes":65536,"maxResponseBytes":65536,"reportRetentionDays":30},
  "mcpMeta": {"callContextKey":"comis.callContext","managedRunResultKey":"comis.managedRun"},
  "methodCatalog": [{"callerClass":"capability-service","classification":"mutation","direction":"service-to-comis","maxRequestBytes":65536,"maxResponseBytes":65536,"method":"capabilityServices.handshake","operationIdRequired":true,"requestSchema":"schemas/handshake.request.schema.json","requiredServiceScope":null,"responseSchema":"schemas/handshake.response.schema.json","semanticInvariants":["exact-protocol-identifier"]}],
  "methods": ["capabilityServices.handshake"],
  "protocolId": "comis.capability-service/1"
}
`, strings.Join(artifacts, ","), digest)
	writeFile(t, filepath.Join(root, "manifest.json"), []byte(manifest))
	return root, digest
}

func writeFile(t *testing.T, path string, contents []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("create fixture directory: %v", err)
	}
	if err := os.WriteFile(path, contents, 0o644); err != nil {
		t.Fatalf("write fixture file: %v", err)
	}
}
