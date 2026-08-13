package service

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/comisai/comis-dev-crew/internal/validation"
)

const maximumCandidateConfigurationBytes = 1 << 20

type candidateCompositionDocument struct {
	Programs       []validation.Program       `json:"programs"`
	Profiles       []candidateProfileDocument `json:"profiles"`
	MaxOutputBytes int64                      `json:"maxOutputBytes"`
	PollInterval   string                     `json:"pollInterval"`
	Forge          candidateForgeDocument     `json:"forge"`
}

type candidateProfileDocument struct {
	ID            string                        `json:"id"`
	LocalChecks   []candidateLocalCheckDocument `json:"localChecks"`
	ForgeChecks   []validation.ForgeCheck       `json:"forgeChecks"`
	ArtifactRules []validation.ArtifactRule     `json:"artifactRules"`
	EvidenceTTL   string                        `json:"evidenceTtl"`
}

type candidateLocalCheckDocument struct {
	ID        string                        `json:"id"`
	ProgramID string                        `json:"programId"`
	Arguments []validation.ArgumentTemplate `json:"arguments"`
	Timeout   string                        `json:"timeout"`
	Required  bool                          `json:"required"`
}

type candidateForgeDocument struct {
	APIBaseURL             string `json:"apiBaseUrl"`
	Owner                  string `json:"owner"`
	Repository             string `json:"repository"`
	RemoteURL              string `json:"remoteUrl"`
	ReadCredentialFile     string `json:"readCredentialFile"`
	PushCredentialFile     string `json:"pushCredentialFile"`
	CredentialDirectory    string `json:"credentialDirectory"`
	LocalFixtureRemoteRoot string `json:"localFixtureRemoteRoot"`
	SSHTransportExecutable string `json:"sshTransportExecutable"`
	SSHExecutable          string `json:"sshExecutable"`
	SSHKnownHostsFile      string `json:"sshKnownHostsFile"`
}

func readCandidateComposition(path string) (*ValidationComposition, *ForgeComposition, error) {
	contents, err := readPrivateCandidateConfiguration(path)
	if err != nil {
		return nil, nil, err
	}
	decoder := json.NewDecoder(bytes.NewReader(contents))
	decoder.DisallowUnknownFields()
	var document candidateCompositionDocument
	if err := decoder.Decode(&document); err != nil {
		return nil, nil, errors.New("read candidate composition: configuration is invalid")
	}
	if err := requireJSONEnd(decoder); err != nil {
		return nil, nil, err
	}
	pollInterval, err := time.ParseDuration(document.PollInterval)
	if err != nil {
		return nil, nil, errors.New("read candidate composition: poll interval is invalid")
	}
	profiles := make([]validation.Profile, 0, len(document.Profiles))
	for _, configured := range document.Profiles {
		evidenceTTL, parseErr := time.ParseDuration(configured.EvidenceTTL)
		if parseErr != nil {
			return nil, nil, errors.New("read candidate composition: evidence lifetime is invalid")
		}
		checks := make([]validation.LocalCheck, 0, len(configured.LocalChecks))
		for _, candidateCheck := range configured.LocalChecks {
			timeout, timeoutErr := time.ParseDuration(candidateCheck.Timeout)
			if timeoutErr != nil {
				return nil, nil, errors.New("read candidate composition: check timeout is invalid")
			}
			checks = append(checks, validation.LocalCheck{
				ID: candidateCheck.ID, ProgramID: candidateCheck.ProgramID,
				Arguments: candidateCheck.Arguments, Timeout: timeout, Required: candidateCheck.Required,
			})
		}
		profiles = append(profiles, validation.Profile{
			ID: configured.ID, LocalChecks: checks, ForgeChecks: configured.ForgeChecks,
			ArtifactRules: configured.ArtifactRules, EvidenceTTL: evidenceTTL,
		})
	}
	return &ValidationComposition{
			Programs: document.Programs, Profiles: profiles,
			MaxOutputBytes: document.MaxOutputBytes, PollInterval: pollInterval,
		}, &ForgeComposition{
			APIBaseURL: document.Forge.APIBaseURL, Owner: document.Forge.Owner, Repository: document.Forge.Repository,
			RemoteURL: document.Forge.RemoteURL, ReadCredentialFile: document.Forge.ReadCredentialFile,
			PushCredentialFile: document.Forge.PushCredentialFile, CredentialDirectory: document.Forge.CredentialDirectory,
			LocalFixtureRemoteRoot: document.Forge.LocalFixtureRemoteRoot,
			SSHTransportExecutable: document.Forge.SSHTransportExecutable,
			SSHExecutable:          document.Forge.SSHExecutable, SSHKnownHostsFile: document.Forge.SSHKnownHostsFile,
		}, nil
}

func readPrivateCandidateConfiguration(path string) ([]byte, error) {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return nil, errors.New("read candidate composition: path must be absolute and canonical")
	}
	info, err := os.Lstat(path)
	if err != nil {
		return nil, errors.New("read candidate composition: configuration is unavailable")
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0o077 != 0 || info.Size() > maximumCandidateConfigurationBytes {
		return nil, errors.New("read candidate composition: configuration must be owner-private, regular, and bounded")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, errors.New("read candidate composition: configuration could not be opened")
	}
	defer func() { _ = file.Close() }()
	opened, err := file.Stat()
	if err != nil || !os.SameFile(info, opened) {
		return nil, errors.New("read candidate composition: file identity changed during open")
	}
	contents, err := io.ReadAll(io.LimitReader(file, maximumCandidateConfigurationBytes+1))
	if err != nil || len(contents) > maximumCandidateConfigurationBytes {
		return nil, errors.New("read candidate composition: configuration exceeds its bound")
	}
	return contents, nil
}

func requireJSONEnd(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return errors.New("read candidate composition: configuration has trailing content")
	}
	return nil
}
