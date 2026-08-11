package service

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/comisai/comis-dev-crew/internal/validation"
)

func TestReadCandidateComposition_ParsesStrictReviewedPolicyAndForgeRoute(t *testing.T) {
	root := shortTempDir(t)
	path := filepath.Join(root, "candidate.json")
	contents := `{
  "programs": [{"id":"repo-check","executable":"/usr/bin/true"}],
  "profiles": [{
    "id":"required",
    "localChecks":[{"id":"unit","programId":"repo-check","arguments":[{"kind":"literal","value":"--version"}],"timeout":"2m","required":true}],
    "forgeChecks":[{"name":"ci/unit","required":true}],
    "artifactRules":[{"kind":"regular_file","relativePath":"report.md","mediaType":"text/markdown","maxBytes":16384}],
    "evidenceTtl":"24h"
  }],
  "maxOutputBytes":65536,
  "pollInterval":"250ms",
  "forge":{
    "apiBaseUrl":"https://api.github.com",
    "owner":"comisai",
    "repository":"product-api",
    "remoteUrl":"https://github.com/comisai/product-api.git",
    "readCredentialFile":"/private/config/forge-read.credential",
    "pushCredentialFile":"/private/config/forge-push.credential",
    "credentialDirectory":"/private/run/forge-credentials"
  }
}`
	writeCandidateConfig(t, path, contents, 0o600)
	validationConfig, forgeConfig, err := readCandidateComposition(path)
	if err != nil {
		t.Fatalf("readCandidateComposition() error = %v", err)
	}
	if validationConfig.MaxOutputBytes != 64<<10 || validationConfig.PollInterval != 250*time.Millisecond ||
		len(validationConfig.Programs) != 1 || len(validationConfig.Profiles) != 1 {
		t.Fatalf("validation configuration = %#v", validationConfig)
	}
	profile := validationConfig.Profiles[0]
	if profile.EvidenceTTL != 24*time.Hour || profile.LocalChecks[0].Timeout != 2*time.Minute ||
		profile.ArtifactRules[0].Kind != validation.ArtifactRegularFile {
		t.Fatalf("reviewed profile = %#v", profile)
	}
	if forgeConfig.APIBaseURL != "https://api.github.com" || forgeConfig.Owner != "comisai" ||
		forgeConfig.LocalFixtureRemoteRoot != "" {
		t.Fatalf("forge configuration = %#v", forgeConfig)
	}
}

func TestReadCandidateComposition_RejectsUntrustedFileAndUnknownPolicy(t *testing.T) {
	root := shortTempDir(t)
	valid := `{"programs":[],"profiles":[],"maxOutputBytes":1,"pollInterval":"1ms","forge":{"apiBaseUrl":"https://api.github.com","owner":"owner","repository":"repository","remoteUrl":"https://example.com/repository.git","readCredentialFile":"/private/read","pushCredentialFile":"/private/push","credentialDirectory":"/private/credentials"}}`
	public := filepath.Join(root, "public.json")
	writeCandidateConfig(t, public, valid, 0o644)
	if _, _, err := readCandidateComposition(public); err == nil {
		t.Fatal("readCandidateComposition(public) error = nil")
	}
	unknown := filepath.Join(root, "unknown.json")
	writeCandidateConfig(t, unknown, `{"unknown":true}`, 0o600)
	if _, _, err := readCandidateComposition(unknown); err == nil {
		t.Fatal("readCandidateComposition(unknown) error = nil")
	}
	invalidDuration := filepath.Join(root, "duration.json")
	writeCandidateConfig(t, invalidDuration, `{"pollInterval":"eventually"}`, 0o600)
	if _, _, err := readCandidateComposition(invalidDuration); err == nil {
		t.Fatal("readCandidateComposition(invalid duration) error = nil")
	}
	symlink := filepath.Join(root, "symlink.json")
	if err := os.Symlink(unknown, symlink); err != nil {
		t.Fatal(err)
	}
	if _, _, err := readCandidateComposition(symlink); err == nil {
		t.Fatal("readCandidateComposition(symlink) error = nil")
	}
}

func writeCandidateConfig(t *testing.T, path, contents string, mode os.FileMode) {
	t.Helper()
	if err := os.WriteFile(path, []byte(contents), mode); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, mode); err != nil {
		t.Fatal(err)
	}
}
