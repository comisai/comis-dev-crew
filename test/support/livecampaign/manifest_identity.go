package livecampaign

import (
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
)

var (
	lowerHexCommitPattern = regexp.MustCompile(`^[a-f0-9]{40}$`)
	lowerHexDigestPattern = regexp.MustCompile(`^[a-f0-9]{64}$`)
)

var requiredArtifactKinds = []string{
	"comis-cli",
	"devcrew",
	"devcrew-mcp",
	"devcrew-report",
	"devcrew-service",
}

var requiredWorkerKinds = []string{"codex", "claude"}

type SourcePins struct {
	ComisCommit   string `json:"comisCommit"`
	DevCrewCommit string `json:"devcrewCommit"`
}

type ProtocolPin struct {
	ID     string `json:"id"`
	Digest string `json:"digest"`
}

type ArtifactPin struct {
	Kind    string `json:"kind"`
	Path    string `json:"path"`
	SHA256  string `json:"sha256"`
	Version string `json:"version"`
}

type WorkerPin struct {
	Kind      string `json:"kind"`
	ProfileID string `json:"profileId"`
	Path      string `json:"path"`
	SHA256    string `json:"sha256"`
	Version   string `json:"version"`
}

func (manifest Manifest) validateWorkers() error {
	if len(manifest.Workers) != len(requiredWorkerKinds) {
		return errors.New("worker pin catalog must contain exactly Codex and Claude Code")
	}
	byKind := make(map[string]WorkerPin, len(manifest.Workers))
	profiles := make(map[string]struct{}, len(manifest.Workers))
	paths := make(map[string]struct{}, len(manifest.Workers))
	for _, worker := range manifest.Workers {
		if !contains(requiredWorkerKinds, worker.Kind) {
			return errors.New("worker pin kind is outside the closed catalog")
		}
		if _, exists := byKind[worker.Kind]; exists {
			return errors.New("worker pin kinds must be unique")
		}
		if !safeReferencePattern.MatchString(worker.ProfileID) {
			return errors.New("worker pin profile must be one bounded identifier")
		}
		if _, exists := profiles[worker.ProfileID]; exists {
			return errors.New("worker pin profiles must be unique")
		}
		if !filepath.IsAbs(worker.Path) || filepath.Clean(worker.Path) != worker.Path {
			return errors.New("worker pin paths must be clean and absolute")
		}
		if _, exists := paths[worker.Path]; exists {
			return errors.New("worker pin paths must be unique")
		}
		if !lowerHexDigestPattern.MatchString(worker.SHA256) || !validDisplayName(worker.Version) {
			return errors.New("worker pin digest or version is invalid")
		}
		byKind[worker.Kind] = worker
		profiles[worker.ProfileID] = struct{}{}
		paths[worker.Path] = struct{}{}
	}
	for _, kind := range requiredWorkerKinds {
		if _, exists := byKind[kind]; !exists {
			return fmt.Errorf("worker pin catalog is missing %s", kind)
		}
	}
	for _, task := range manifest.Tasks {
		if _, exists := profiles[task.WorkerProfileID]; !exists {
			return errors.New("worker pin profiles must exactly cover the task profiles")
		}
	}
	return nil
}

func (manifest Manifest) validateArtifacts() error {
	if len(manifest.Artifacts) != len(requiredArtifactKinds) {
		return errors.New("runtime artifact catalog must contain exactly the five required product artifacts")
	}
	byKind := make(map[string]ArtifactPin, len(manifest.Artifacts))
	paths := make(map[string]struct{}, len(manifest.Artifacts))
	for _, artifact := range manifest.Artifacts {
		if !contains(requiredArtifactKinds, artifact.Kind) {
			return fmt.Errorf("runtime artifact kind %s is outside the closed artifact catalog", artifact.Kind)
		}
		if _, exists := byKind[artifact.Kind]; exists {
			return errors.New("runtime artifact kinds must be unique")
		}
		if !filepath.IsAbs(artifact.Path) || filepath.Clean(artifact.Path) != artifact.Path {
			return errors.New("runtime artifact paths must be clean and absolute")
		}
		if _, exists := paths[artifact.Path]; exists {
			return errors.New("runtime artifact paths must be unique")
		}
		if !lowerHexDigestPattern.MatchString(artifact.SHA256) {
			return errors.New("runtime artifact SHA-256 pins must be lowercase 64-character digests")
		}
		if !safeReferencePattern.MatchString(artifact.Version) {
			return errors.New("runtime artifact versions must be bounded identifiers")
		}
		byKind[artifact.Kind] = artifact
		paths[artifact.Path] = struct{}{}
	}
	for _, kind := range requiredArtifactKinds {
		if _, exists := byKind[kind]; !exists {
			return fmt.Errorf("runtime artifact catalog is missing %s", kind)
		}
	}
	if byKind["comis-cli"].Path != manifest.Comis.CLIScriptPath {
		return errors.New("comis-cli artifact path must equal comis.cliScriptPath")
	}
	if byKind["devcrew"].Path != manifest.DevCrew.CLIPath {
		return errors.New("devcrew artifact path must equal devcrew.cliPath")
	}
	devCrewVersion := byKind["devcrew"].Version
	for _, kind := range []string{"devcrew-mcp", "devcrew-report", "devcrew-service"} {
		if byKind[kind].Version != devCrewVersion {
			return errors.New("all four DevCrew artifacts must declare one exact version")
		}
	}
	return nil
}
