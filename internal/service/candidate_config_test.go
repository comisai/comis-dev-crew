package service

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/comisai/comis-dev-crew/internal/forge"
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
	for _, test := range []struct {
		name     string
		path     string
		contents string
	}{
		{name: "relative path", path: "candidate.json", contents: valid},
		{name: "missing file", path: filepath.Join(root, "missing.json"), contents: ""},
		{name: "trailing document", path: filepath.Join(root, "trailing.json"), contents: valid + `{}`},
		{name: "invalid evidence lifetime", path: filepath.Join(root, "lifetime.json"), contents: `{"pollInterval":"1ms","profiles":[{"evidenceTtl":"later"}]}`},
		{name: "invalid check timeout", path: filepath.Join(root, "timeout.json"), contents: `{"pollInterval":"1ms","profiles":[{"evidenceTtl":"1h","localChecks":[{"timeout":"later"}]}]}`},
		{name: "oversized file", path: filepath.Join(root, "oversized.json"), contents: strings.Repeat("x", maximumCandidateConfigurationBytes+1)},
	} {
		t.Run(test.name, func(t *testing.T) {
			if filepath.IsAbs(test.path) && test.contents != "" {
				writeCandidateConfig(t, test.path, test.contents, 0o600)
			}
			if _, _, err := readCandidateComposition(test.path); err == nil {
				t.Fatal("readCandidateComposition(invalid) error = nil")
			}
		})
	}
}

func TestReadCandidateCompositionPreservesPinnedSSHTransport(t *testing.T) {
	path := filepath.Join(shortTempDir(t), "candidate.json")
	writeCandidateConfig(t, path, `{
  "programs":[],"profiles":[],"maxOutputBytes":1,"pollInterval":"1ms",
  "forge":{
    "apiBaseUrl":"https://api.github.com","owner":"fixture-owner","repository":"fixture-repository",
    "remoteUrl":"ssh://git@github.com/fixture-owner/fixture-repository.git",
    "readCredentialFile":"/private/read","pushCredentialFile":"/private/push",
    "credentialDirectory":"/private/credentials",
    "sshTransportExecutable":"/opt/devcrew/bin/devcrew-service",
    "sshExecutable":"/usr/bin/ssh","sshKnownHostsFile":"/private/known-hosts"
  }
}`, 0o600)
	_, forgeConfig, err := readCandidateComposition(path)
	if err != nil {
		t.Fatalf("readCandidateComposition(SSH) error = %v", err)
	}
	if forgeConfig.RemoteURL != "ssh://git@github.com/fixture-owner/fixture-repository.git" ||
		forgeConfig.SSHTransportExecutable != "/opt/devcrew/bin/devcrew-service" ||
		forgeConfig.SSHExecutable != "/usr/bin/ssh" || forgeConfig.SSHKnownHostsFile != "/private/known-hosts" {
		t.Fatalf("SSH forge configuration = %#v", forgeConfig)
	}
}

func TestOwnerCredentialSource_ResolvesOneScopedPrivateIdentity(t *testing.T) {
	path := filepath.Join(shortTempDir(t), "forge.credential")
	writeCandidateConfig(t, path, "forge_identity_secret", 0o600)
	source := ownerCredentialSource{
		path: path, kind: forge.CredentialRead,
		scopes: []forge.CredentialScope{forge.ScopeContentsRead, forge.ScopePullRequestsRead, forge.ScopeChecksRead},
	}
	credential, err := source.Resolve(context.Background())
	if err != nil || credential.Kind != forge.CredentialRead || credential.Secret != "forge_identity_secret" ||
		len(credential.Scopes) != 3 {
		t.Fatalf("Resolve() = %#v, %v", credential, err)
	}
	source.scopes[0] = forge.ScopeContentsWrite
	if credential.Scopes[0] != forge.ScopeContentsRead {
		t.Fatal("resolved credential scopes alias mutable configuration")
	}
	//lint:ignore SA1012 This boundary test proves a nil context is rejected without dereferencing it.
	if _, err := source.Resolve(nil); err == nil {
		t.Fatal("Resolve(nil) error = nil")
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := source.Resolve(cancelled); !errors.Is(err, context.Canceled) {
		t.Fatalf("Resolve(cancelled) error = %v", err)
	}
	source.path = filepath.Join(filepath.Dir(path), "missing")
	if _, err := source.Resolve(context.Background()); err == nil {
		t.Fatal("Resolve(missing) error = nil")
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
