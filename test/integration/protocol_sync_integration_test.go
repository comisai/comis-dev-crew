//go:build integration

package integration_test

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestProtocolSyncCopiesOnlyVerifiedPinnedArtifacts(t *testing.T) {
	repository := protocolRepositoryRoot(t)
	source, commit := createComisProtocolRepository(t)
	destination := filepath.Join(t.TempDir(), "protocol", "comis")
	if err := os.MkdirAll(destination, 0o755); err != nil {
		t.Fatalf("create destination: %v", err)
	}
	if err := os.WriteFile(filepath.Join(destination, "README.md"), []byte("status\n"), 0o644); err != nil {
		t.Fatalf("write destination README: %v", err)
	}

	command := exec.Command(
		"go", "run", "./tools/protocolsync",
		"-source-root", source,
		"-source-commit", commit,
		"-destination-root", destination,
	)
	command.Dir = repository
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("protocol sync failed: %v\n%s", err, output)
	}

	for _, relative := range []string{
		"README.md",
		"manifest.json",
		"provenance.json",
		"fixtures/valid.json",
		"schemas/handshake.request.schema.json",
		"schemas/handshake.response.schema.json",
	} {
		if _, err := os.Stat(filepath.Join(destination, filepath.FromSlash(relative))); err != nil {
			t.Fatalf("pinned file %s: %v", relative, err)
		}
	}
	if _, err := os.Stat(filepath.Join(destination, "private-source.txt")); !os.IsNotExist(err) {
		t.Fatalf("non-protocol source was copied: %v", err)
	}
	provenance, err := os.ReadFile(filepath.Join(destination, "provenance.json"))
	if err != nil {
		t.Fatalf("read provenance: %v", err)
	}
	if !strings.Contains(string(provenance), commit) ||
		!strings.Contains(string(provenance), "comis.capability-service/1") ||
		!strings.Contains(string(provenance), "@comis/capability-service-sdk") {
		t.Fatalf("incomplete provenance: %s", provenance)
	}
}

func TestProtocolSyncRejectsWrongSourceCommitWithoutReplacingPin(t *testing.T) {
	repository := protocolRepositoryRoot(t)
	source, _ := createComisProtocolRepository(t)
	destination := filepath.Join(t.TempDir(), "protocol", "comis")
	if err := os.MkdirAll(destination, 0o755); err != nil {
		t.Fatalf("create destination: %v", err)
	}
	sentinel := filepath.Join(destination, "README.md")
	if err := os.WriteFile(sentinel, []byte("preserve\n"), 0o644); err != nil {
		t.Fatalf("write destination sentinel: %v", err)
	}

	command := exec.Command(
		"go", "run", "./tools/protocolsync",
		"-source-root", source,
		"-source-commit", strings.Repeat("f", 40),
		"-destination-root", destination,
	)
	command.Dir = repository
	if output, err := command.CombinedOutput(); err == nil {
		t.Fatalf("wrong source commit unexpectedly synced: %s", output)
	}
	contents, err := os.ReadFile(sentinel)
	if err != nil {
		t.Fatalf("read preserved sentinel: %v", err)
	}
	if string(contents) != "preserve\n" {
		t.Fatalf("destination changed on rejected sync: %q", contents)
	}
}

func createComisProtocolRepository(t *testing.T) (string, string) {
	t.Helper()
	root := t.TempDir()
	protocolRoot := filepath.Join(root, "packages", "capability-service-sdk", "protocol")
	files := map[string][]byte{
		"fixtures/valid.json":                    []byte("{}\n"),
		"schemas/handshake.request.schema.json":  []byte("{}\n"),
		"schemas/handshake.response.schema.json": []byte("{}\n"),
	}
	paths := []string{
		"fixtures/valid.json",
		"schemas/handshake.request.schema.json",
		"schemas/handshake.response.schema.json",
	}
	artifacts := make([]string, 0, len(paths))
	digestInput := make([]byte, 0)
	for _, path := range paths {
		contents := files[path]
		writeIntegrationFile(t, filepath.Join(protocolRoot, filepath.FromSlash(path)), contents)
		sum := sha256.Sum256(contents)
		hash := hex.EncodeToString(sum[:])
		artifacts = append(artifacts, fmt.Sprintf(`{"path":%q,"sha256":%q}`, path, hash))
		digestInput = append(digestInput, path...)
		digestInput = append(digestInput, 0)
		digestInput = append(digestInput, hash...)
		digestInput = append(digestInput, '\n')
	}
	sum := sha256.Sum256(digestInput)
	digest := hex.EncodeToString(sum[:])
	manifest := fmt.Sprintf("{\"artifacts\":[%s],\"bundleDigest\":%q,\"bundleDigestAlgorithm\":\"sha256 over lexically ordered path, NUL, hash, newline records\",\"errorKinds\":[\"invalid_request\"],\"errors\":[{\"code\":-32600,\"kind\":\"invalid_request\",\"retryable\":false}],\"fixtureDigestToken\":\"__BUNDLE_DIGEST__\",\"generator\":{\"command\":\"pnpm capability-protocol:generate\",\"package\":\"@comis/capability-service-sdk\",\"version\":\"1.0.59\"},\"limits\":{\"maxEvidenceBytes\":1048576,\"maxInFlightRequests\":32,\"maxLineBytes\":65536,\"maxReportBytes\":16384,\"maxRequestBytes\":65536,\"maxResponseBytes\":65536,\"reportRetentionDays\":30},\"mcpMeta\":{\"callContextKey\":\"comis.callContext\",\"managedRunResultKey\":\"comis.managedRun\"},\"methodCatalog\":[{\"callerClass\":\"capability-service\",\"classification\":\"mutation\",\"direction\":\"service-to-comis\",\"maxRequestBytes\":65536,\"maxResponseBytes\":65536,\"method\":\"capabilityServices.handshake\",\"operationIdRequired\":true,\"requestSchema\":\"schemas/handshake.request.schema.json\",\"requiredServiceScope\":null,\"responseSchema\":\"schemas/handshake.response.schema.json\",\"semanticInvariants\":[\"exact-protocol-identifier\"]}],\"methods\":[\"capabilityServices.handshake\"],\"protocolId\":\"comis.capability-service/1\"}\n", strings.Join(artifacts, ","), digest)
	writeIntegrationFile(t, filepath.Join(protocolRoot, "manifest.json"), []byte(manifest))
	writeIntegrationFile(t, filepath.Join(root, "private-source.txt"), []byte("must not copy\n"))

	runGit(t, root, "init")
	runGit(t, root, "config", "user.name", "Protocol Fixture")
	runGit(t, root, "config", "user.email", "fixture@example.com")
	runGit(t, root, "add", ".")
	runGit(t, root, "commit", "-m", "fixture")
	return root, strings.TrimSpace(runGit(t, root, "rev-parse", "HEAD"))
}

func protocolRepositoryRoot(t *testing.T) string {
	t.Helper()
	_, sourceFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve integration test path")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(sourceFile), "..", ".."))
}

func writeIntegrationFile(t *testing.T, path string, contents []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("create fixture directory: %v", err)
	}
	if err := os.WriteFile(path, contents, 0o644); err != nil {
		t.Fatalf("write fixture file: %v", err)
	}
}

func runGit(t *testing.T, directory string, arguments ...string) string {
	t.Helper()
	command := exec.Command("git", append([]string{"-C", directory}, arguments...)...)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v failed: %v\n%s", arguments, err, output)
	}
	return string(output)
}
