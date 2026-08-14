package livecampaign

import (
	"errors"
	"fmt"
	"path/filepath"
)

// RecoveryTarget binds backup configuration and synthetic rollback fixtures.
type RecoveryTarget struct {
	CandidateConfigPath          string        `json:"candidateConfigPath"`
	SyntheticComisDataDir        string        `json:"syntheticComisDataDir"`
	SyntheticDevCrewDatabasePath string        `json:"syntheticDevcrewDatabasePath"`
	PreviousArtifacts            []ArtifactPin `json:"previousArtifacts"`
}

func (manifest Manifest) validateRecovery() error {
	for name, path := range map[string]string{
		"recovery.candidateConfigPath":          manifest.Recovery.CandidateConfigPath,
		"recovery.syntheticComisDataDir":        manifest.Recovery.SyntheticComisDataDir,
		"recovery.syntheticDevcrewDatabasePath": manifest.Recovery.SyntheticDevCrewDatabasePath,
	} {
		if !filepath.IsAbs(path) || filepath.Clean(path) != path {
			return fmt.Errorf("%s must be one clean absolute path", name)
		}
	}
	if pathWithin(manifest.Comis.DataDir, manifest.Recovery.SyntheticComisDataDir) ||
		pathWithin(manifest.Recovery.SyntheticComisDataDir, manifest.Comis.DataDir) ||
		pathWithin(filepath.Dir(manifest.DevCrew.DatabasePath), manifest.Recovery.SyntheticComisDataDir) ||
		pathWithin(manifest.Recovery.SyntheticComisDataDir, filepath.Dir(manifest.DevCrew.DatabasePath)) ||
		pathWithin(manifest.Comis.DataDir, manifest.Recovery.SyntheticDevCrewDatabasePath) ||
		pathWithin(filepath.Dir(manifest.DevCrew.DatabasePath), manifest.Recovery.SyntheticDevCrewDatabasePath) ||
		manifest.Recovery.SyntheticDevCrewDatabasePath == manifest.DevCrew.DatabasePath {
		return errors.New("recovery synthetic fixtures must not overlap live campaign state")
	}
	if err := validatePreviousArtifacts(manifest); err != nil {
		return err
	}
	return nil
}

func validatePreviousArtifacts(manifest Manifest) error {
	artifacts := manifest.Recovery.PreviousArtifacts
	if len(artifacts) != len(requiredArtifactKinds) {
		return errors.New("previous artifact catalog must contain exactly the five required product artifacts")
	}
	byKind := make(map[string]ArtifactPin, len(artifacts))
	currentPaths := make(map[string]struct{}, len(manifest.Artifacts))
	previousPaths := make(map[string]struct{}, len(artifacts))
	currentVersions := make(map[string]string, len(manifest.Artifacts))
	for _, current := range manifest.Artifacts {
		currentPaths[current.Path] = struct{}{}
		currentVersions[current.Kind] = current.Version
	}
	for _, artifact := range artifacts {
		if !contains(requiredArtifactKinds, artifact.Kind) {
			return errors.New("previous artifact kind is outside the closed catalog")
		}
		if _, exists := byKind[artifact.Kind]; exists {
			return errors.New("previous artifact kinds must be unique")
		}
		if !filepath.IsAbs(artifact.Path) || filepath.Clean(artifact.Path) != artifact.Path {
			return errors.New("previous artifact paths must be clean and absolute")
		}
		if _, exists := currentPaths[artifact.Path]; exists {
			return errors.New("previous and current artifact paths must be distinct")
		}
		if _, exists := previousPaths[artifact.Path]; exists {
			return errors.New("previous artifact paths must be unique")
		}
		if artifact.Version == currentVersions[artifact.Kind] {
			return errors.New("previous artifact versions must differ from the current campaign artifacts")
		}
		if !lowerHexDigestPattern.MatchString(artifact.SHA256) || !safeReferencePattern.MatchString(artifact.Version) {
			return errors.New("previous artifact digest or version is invalid")
		}
		byKind[artifact.Kind] = artifact
		previousPaths[artifact.Path] = struct{}{}
	}
	for _, kind := range requiredArtifactKinds {
		if _, exists := byKind[kind]; !exists {
			return fmt.Errorf("previous artifact catalog is missing %s", kind)
		}
	}
	devCrewVersion := byKind["devcrew"].Version
	for _, kind := range []string{"devcrew-mcp", "devcrew-report", "devcrew-service"} {
		if byKind[kind].Version != devCrewVersion {
			return errors.New("all four previous DevCrew artifacts must declare one exact version")
		}
	}
	return nil
}
