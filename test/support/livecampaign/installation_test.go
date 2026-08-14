package livecampaign

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type installationExecutorFixture struct {
	manifest                 Manifest
	omitChecksumLine         bool
	devCrewReportedVersions  map[string]string
	devCrewInstalledReleases []string
}

func (executor *installationExecutorFixture) Run(_ context.Context, command Command) ([]byte, error) {
	comisInstaller := filepath.Join(executor.manifest.Comis.CodeRoot, "website", "public", "install.sh")
	devCrewInstaller := filepath.Join(executor.manifest.DevCrew.CodeRoot, "docs", "install.sh")
	switch command.Path {
	case comisInstaller:
		version := argumentAfter(command.Args, "--version")
		path := filepath.Join(command.Env["NPM_CONFIG_PREFIX"], "lib", "node_modules", "comisai", "node_modules", "@comis", "cli", "dist", "cli.js")
		return nil, writeInstalledFixture(path, "comis-cli", version, 0o600)
	case devCrewInstaller:
		version := command.Env["DEVCREW_VERSION"]
		executor.devCrewInstalledReleases = append(executor.devCrewInstalledReleases, version)
		reportedVersion := version
		if mapped, exists := executor.devCrewReportedVersions[version]; exists {
			reportedVersion = mapped
		}
		for _, kind := range requiredArtifactKinds[1:] {
			if err := writeInstalledFixture(filepath.Join(command.Env["DEVCREW_INSTALL_DIR"], kind), kind, reportedVersion, 0o700); err != nil {
				return nil, err
			}
		}
		if executor.omitChecksumLine {
			return []byte("installed without retained checksum proof\n"), nil
		}
		return []byte("Checksum verified.\n"), nil
	case executor.manifest.Comis.NodePath:
		if len(command.Args) == 2 && command.Args[1] == "--version" {
			return installedFixtureVersion(command.Args[0], "comis-cli")
		}
	default:
		if len(command.Args) == 1 && command.Args[0] == "--version" {
			return installedFixtureVersion(command.Path, filepath.Base(command.Path))
		}
	}
	return nil, errors.New("unexpected installation command")
}

func TestVerifyReleaseInstallationUsesPreviousReleaseCoordinateWhenReportedVersionDiffers(t *testing.T) {
	manifest := installationManifestFixture(t)
	for index := range manifest.Recovery.PreviousArtifacts {
		pin := &manifest.Recovery.PreviousArtifacts[index]
		if pin.Kind == "comis-cli" {
			continue
		}
		pin.Version = "dev"
		if err := writeInstalledFixture(pin.Path, pin.Kind, pin.Version, 0o700); err != nil {
			t.Fatal(err)
		}
		pin.SHA256 = sha256Hex([]byte(pin.Kind + "|" + pin.Version + "\n"))
	}
	contents, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	contents = []byte(strings.Replace(
		string(contents),
		`"previousArtifacts":`,
		`"previousDevcrewRelease":"v0.1.0","previousArtifacts":`,
		1,
	))
	manifestPath := filepath.Join(t.TempDir(), "campaign.json")
	if err := os.WriteFile(manifestPath, contents, 0o600); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadManifest(manifestPath)
	if err != nil {
		t.Fatalf("LoadManifest() error = %v", err)
	}
	parent, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	executor := &installationExecutorFixture{
		manifest:                loaded,
		devCrewReportedVersions: map[string]string{"v0.1.0": "dev"},
	}
	evidence, err := VerifyReleaseInstallation(
		context.Background(), loaded, filepath.Join(parent, "fresh"), filepath.Join(parent, "upgrade"),
		executor, loaded.EndedAtMs,
	)
	if err != nil {
		t.Fatalf("VerifyReleaseInstallation() error = %v", err)
	}
	if got := strings.Join(executor.devCrewInstalledReleases, ","); got != "v0.2.0,v0.1.0,v0.2.0" ||
		evidence.PreviousDevCrewVersion != "dev" {
		t.Fatalf("release coordinates = %q, evidence = %#v", got, evidence)
	}
}

func argumentAfter(arguments []string, name string) string {
	for index := range arguments {
		if arguments[index] == name && index+1 < len(arguments) {
			return arguments[index+1]
		}
	}
	return ""
}

func writeInstalledFixture(path string, kind string, version string, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(kind+"|"+version+"\n"), mode)
}

func installedFixtureVersion(path string, kind string) ([]byte, error) {
	contents, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	value := strings.TrimSpace(string(contents))
	prefix := kind + "|"
	if !strings.HasPrefix(value, prefix) {
		return nil, errors.New("installed fixture kind differs")
	}
	version := strings.TrimPrefix(value, prefix)
	if kind == "comis-cli" {
		return []byte(version + "\n"), nil
	}
	return []byte(kind + " " + version + "\n"), nil
}

func TestVerifyReleaseInstallationProvesFreshAndPriorReleaseUpgradeArtifacts(t *testing.T) {
	manifest := installationManifestFixture(t)
	parent, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(parent, 0o700); err != nil {
		t.Fatal(err)
	}
	freshRoot := filepath.Join(parent, "fresh")
	upgradeRoot := filepath.Join(parent, "upgrade")
	evidence, err := VerifyReleaseInstallation(
		context.Background(), manifest, freshRoot, upgradeRoot,
		&installationExecutorFixture{manifest: manifest}, manifest.EndedAtMs,
	)
	if err != nil {
		t.Fatalf("VerifyReleaseInstallation() error = %v", err)
	}
	if !evidence.Passed || !evidence.DevCrewChecksumVerified || !evidence.ComisPackageVerified ||
		evidence.FreshArtifactsVerified != installedArtifactCount ||
		evidence.PreviousArtifactsVerified != installedArtifactCount ||
		evidence.UpgradedArtifactsVerified != installedArtifactCount ||
		evidence.CurrentDevCrewVersion != "v0.2.0" || evidence.PreviousDevCrewVersion != "v0.1.0" {
		t.Fatalf("installation evidence = %#v", evidence)
	}
}

func TestVerifyReleaseInstallationRefusesMissingArchiveChecksumProof(t *testing.T) {
	manifest := installationManifestFixture(t)
	parent, resolveErr := filepath.EvalSymlinks(t.TempDir())
	if resolveErr != nil {
		t.Fatal(resolveErr)
	}
	if err := os.Chmod(parent, 0o700); err != nil {
		t.Fatal(err)
	}
	_, err := VerifyReleaseInstallation(
		context.Background(), manifest, filepath.Join(parent, "fresh"), filepath.Join(parent, "upgrade"),
		&installationExecutorFixture{manifest: manifest, omitChecksumLine: true}, manifest.EndedAtMs,
	)
	if err == nil || !strings.Contains(err.Error(), "checksum") {
		t.Fatalf("expected checksum-proof refusal, got %v", err)
	}
}

func installationManifestFixture(t *testing.T) Manifest {
	t.Helper()
	manifest := validManifest()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	manifest.Comis.CodeRoot = filepath.Join(root, "comis-source")
	manifest.DevCrew.CodeRoot = filepath.Join(root, "devcrew-source")
	for _, path := range []string{
		filepath.Join(manifest.Comis.CodeRoot, "website", "public", "install.sh"),
		filepath.Join(manifest.DevCrew.CodeRoot, "docs", "install.sh"),
	} {
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("#!/bin/sh\n"), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	currentVersions := map[string]string{"comis-cli": "1.0.61"}
	previousVersions := map[string]string{"comis-cli": "1.0.60"}
	for _, kind := range requiredArtifactKinds[1:] {
		currentVersions[kind] = "v0.2.0"
		previousVersions[kind] = "v0.1.0"
	}
	for index := range manifest.Artifacts {
		pin := &manifest.Artifacts[index]
		pin.Version = currentVersions[pin.Kind]
		pin.Path = filepath.Join(root, "current", pin.Kind)
		if err := writeInstalledFixture(pin.Path, pin.Kind, pin.Version, 0o700); err != nil {
			t.Fatal(err)
		}
		pin.SHA256 = sha256Hex([]byte(pin.Kind + "|" + pin.Version + "\n"))
	}
	for index := range manifest.Recovery.PreviousArtifacts {
		pin := &manifest.Recovery.PreviousArtifacts[index]
		pin.Version = previousVersions[pin.Kind]
		pin.Path = filepath.Join(root, "previous", pin.Kind)
		if err := writeInstalledFixture(pin.Path, pin.Kind, pin.Version, 0o700); err != nil {
			t.Fatal(err)
		}
		pin.SHA256 = sha256Hex([]byte(pin.Kind + "|" + pin.Version + "\n"))
	}
	manifest.Comis.CLIScriptPath = artifactPinsByKind(manifest.Artifacts)["comis-cli"].Path
	manifest.DevCrew.CLIPath = artifactPinsByKind(manifest.Artifacts)["devcrew"].Path
	return manifest
}
