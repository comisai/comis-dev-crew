package livecampaign

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const installedArtifactCount = 5

// VerifyReleaseInstallation proves fresh Comis and DevCrew installs, then upgrades both
// from the exact previous versions into a second isolated prefix.
func VerifyReleaseInstallation(
	ctx context.Context,
	manifest Manifest,
	freshRoot string,
	upgradeRoot string,
	executor Executor,
	capturedAtMs int64,
) (InstallationEvidence, error) {
	if ctx == nil || executor == nil || capturedAtMs <= 0 {
		return InstallationEvidence{}, errors.New("verify release installation: dependencies and capture time are required")
	}
	if err := manifest.validate(); err != nil {
		return InstallationEvidence{}, fmt.Errorf("verify release installation: %w", err)
	}
	if err := validateInstallationRoots(manifest, freshRoot, upgradeRoot); err != nil {
		return InstallationEvidence{}, err
	}
	if err := reserveInstallationRoot(freshRoot); err != nil {
		return InstallationEvidence{}, err
	}
	passed := false
	defer func() {
		if !passed {
			_ = os.RemoveAll(freshRoot)
			_ = os.RemoveAll(upgradeRoot)
		}
	}()
	if err := reserveInstallationRoot(upgradeRoot); err != nil {
		return InstallationEvidence{}, err
	}

	current := artifactPinsByKind(manifest.Artifacts)
	previous := artifactPinsByKind(manifest.Recovery.PreviousArtifacts)
	if err := installComis(ctx, manifest, executor, freshRoot, current["comis-cli"].Version); err != nil {
		return InstallationEvidence{}, err
	}
	checksumFresh, err := installDevCrew(ctx, manifest, executor, freshRoot, current["devcrew"].Version)
	if err != nil {
		return InstallationEvidence{}, err
	}
	freshCount, err := verifyInstalledArtifacts(ctx, manifest, executor, freshRoot, current)
	if err != nil {
		return InstallationEvidence{}, err
	}

	if err := installComis(ctx, manifest, executor, upgradeRoot, previous["comis-cli"].Version); err != nil {
		return InstallationEvidence{}, err
	}
	checksumPrevious, err := installDevCrew(ctx, manifest, executor, upgradeRoot, previous["devcrew"].Version)
	if err != nil {
		return InstallationEvidence{}, err
	}
	previousCount, err := verifyInstalledArtifacts(ctx, manifest, executor, upgradeRoot, previous)
	if err != nil {
		return InstallationEvidence{}, err
	}
	if err := installComis(ctx, manifest, executor, upgradeRoot, current["comis-cli"].Version); err != nil {
		return InstallationEvidence{}, err
	}
	checksumUpgrade, err := installDevCrew(ctx, manifest, executor, upgradeRoot, current["devcrew"].Version)
	if err != nil {
		return InstallationEvidence{}, err
	}
	upgradedCount, err := verifyInstalledArtifacts(ctx, manifest, executor, upgradeRoot, current)
	if err != nil {
		return InstallationEvidence{}, err
	}

	evidence := InstallationEvidence{
		SchemaVersion: 1, CapturedAtMs: capturedAtMs, Passed: true,
		DevCrewChecksumVerified: checksumFresh && checksumPrevious && checksumUpgrade,
		ComisPackageVerified:    true,
		FreshArtifactsVerified:  freshCount, PreviousArtifactsVerified: previousCount,
		UpgradedArtifactsVerified: upgradedCount,
		CurrentComisVersion:       current["comis-cli"].Version, PreviousComisVersion: previous["comis-cli"].Version,
		CurrentDevCrewVersion: current["devcrew"].Version, PreviousDevCrewVersion: previous["devcrew"].Version,
	}
	passed = true
	return evidence, nil
}

func validateInstallationRoots(manifest Manifest, freshRoot string, upgradeRoot string) error {
	if freshRoot == upgradeRoot || pathWithin(freshRoot, upgradeRoot) || pathWithin(upgradeRoot, freshRoot) {
		return errors.New("verify release installation: fresh and upgrade roots must be distinct")
	}
	for _, root := range []string{freshRoot, upgradeRoot} {
		if err := validateRecoveryOutputRoot(manifest, root); err != nil {
			return fmt.Errorf("verify release installation: %w", err)
		}
	}
	return nil
}

func reserveInstallationRoot(root string) error {
	if err := ensurePrivateDirectory(filepath.Dir(root)); err != nil {
		return fmt.Errorf("verify release installation: prepare root parent: %w", err)
	}
	if err := os.Mkdir(root, 0o700); err != nil {
		return fmt.Errorf("verify release installation: reserve root: %w", err)
	}
	return nil
}

func installComis(ctx context.Context, manifest Manifest, executor Executor, root string, version string) error {
	installer := filepath.Join(manifest.Comis.CodeRoot, "website", "public", "install.sh")
	if !pathWithin(manifest.Comis.CodeRoot, installer) || validateExecutable(installer) != nil {
		return errors.New("verify release installation: Comis installer is unavailable")
	}
	home := filepath.Join(root, "comis-home")
	prefix := filepath.Join(root, "comis-prefix")
	if err := os.MkdirAll(home, 0o700); err != nil {
		return fmt.Errorf("verify release installation: create Comis home: %w", err)
	}
	path := filepath.Join(prefix, "bin") + string(os.PathListSeparator) + os.Getenv("PATH")
	_, err := executor.Run(ctx, Command{Path: installer, Args: []string{
		"--npm", "--version", version, "--no-user", "--no-init", "--no-prompt",
		"--service", "none", "--without-browser", "--without-xvfb",
	}, Env: map[string]string{
		"HOME": home, "NPM_CONFIG_PREFIX": prefix, "PATH": path,
	}})
	if err != nil {
		return errors.New("verify release installation: Comis package install failed")
	}
	return nil
}

func installDevCrew(
	ctx context.Context,
	manifest Manifest,
	executor Executor,
	root string,
	version string,
) (bool, error) {
	installer := filepath.Join(manifest.DevCrew.CodeRoot, "docs", "install.sh")
	if !pathWithin(manifest.DevCrew.CodeRoot, installer) || validateExecutable(installer) != nil {
		return false, errors.New("verify release installation: DevCrew installer is unavailable")
	}
	bin := filepath.Join(root, "devcrew", "bin")
	output, err := executor.Run(ctx, Command{Path: installer, Env: map[string]string{
		"DEVCREW_INSTALL_DIR": bin, "DEVCREW_LINK_DIR": bin, "DEVCREW_VERSION": version,
	}})
	if err != nil || !strings.Contains(string(output), "Checksum verified.") {
		return false, errors.New("verify release installation: DevCrew checksum-verified install failed")
	}
	return true, nil
}

func verifyInstalledArtifacts(
	ctx context.Context,
	manifest Manifest,
	executor Executor,
	root string,
	expected map[string]ArtifactPin,
) (int, error) {
	paths := map[string]string{
		"comis-cli":       filepath.Join(root, "comis-prefix", "lib", "node_modules", "comisai", "node_modules", "@comis", "cli", "dist", "cli.js"),
		"devcrew":         filepath.Join(root, "devcrew", "bin", "devcrew"),
		"devcrew-mcp":     filepath.Join(root, "devcrew", "bin", "devcrew-mcp"),
		"devcrew-report":  filepath.Join(root, "devcrew", "bin", "devcrew-report"),
		"devcrew-service": filepath.Join(root, "devcrew", "bin", "devcrew-service"),
	}
	verified := 0
	for _, kind := range requiredArtifactKinds {
		pin := expected[kind]
		pin.Path = paths[kind]
		if err := validatePinnedArtifact(pin); err != nil {
			return 0, fmt.Errorf("verify release installation: installed %s artifact: %w", kind, err)
		}
		if err := validatePinnedArtifactVersion(ctx, manifest, pin, executor); err != nil {
			return 0, fmt.Errorf("verify release installation: installed %s version: %w", kind, err)
		}
		verified++
	}
	if verified != installedArtifactCount {
		return 0, errors.New("verify release installation: installed artifact catalog is incomplete")
	}
	return verified, nil
}
