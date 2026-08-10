package service

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/comisai/comis-dev-crew/internal/application"
	"github.com/comisai/comis-dev-crew/internal/comiswire"
	"github.com/comisai/comis-dev-crew/internal/domain"
	devgit "github.com/comisai/comis-dev-crew/internal/git"
	"github.com/comisai/comis-dev-crew/internal/workers"
)

func composeInstalledRuntime(ctx context.Context, config Config) (Config, error) {
	configured := config.RepositoryComposition != nil || config.ComisComposition != nil || config.CodexComposition != nil
	if !configured {
		return config, nil
	}
	if config.RepositoryComposition == nil || config.ComisComposition == nil || config.CodexComposition == nil || config.FixtureComposition != nil ||
		config.MCPSocketPath == "" || config.RuntimeRoot == "" || config.ServiceInstanceID == "" {
		return Config{}, errors.New("run service: installed composition is incomplete")
	}
	if config.Repositories != nil || config.Workspaces != nil || config.TaskIDs != nil ||
		config.RuntimeAttachments != nil || config.WorkerHarnesses != nil || config.RegistrationNonces != nil || config.ComisControl != nil {
		return Config{}, errors.New("run service: installed and injected composition cannot be combined")
	}
	repositoryConfig := config.RepositoryComposition
	registry, err := devgit.NewRegistry(ctx, devgit.RegistryConfig{
		GitExecutable: repositoryConfig.GitExecutable,
		ApprovedRoots: []string{repositoryConfig.ApprovedRoot},
		Repositories: []devgit.RepositoryConfig{{
			ID: repositoryConfig.RepositoryID, PrimaryCheckout: repositoryConfig.PrimaryCheckout,
			WorktreeRoot: repositoryConfig.WorktreeRoot, DefaultBranch: repositoryConfig.DefaultBranch,
		}},
	})
	if err != nil {
		return Config{}, fmt.Errorf("run service repository composition: %w", err)
	}
	codexConfig := config.CodexComposition
	profiles, err := workers.NewProfileCatalog([]workers.StaticProfile{{
		ID: codexConfig.ProfileID, Harness: workers.HarnessCodex,
		AllowedShapes: []domain.TaskShape{domain.ShapeShip, domain.ShapeScout},
		Model:         codexConfig.Model, Effort: codexConfig.Effort,
		TerminalAllowEntry: codexConfig.TerminalAllowEntryID,
		Network:            codexConfig.Network, ConcurrencyLimit: codexConfig.ConcurrencyLimit,
		Unattended: true, Executable: codexConfig.Executable,
		Arguments:       []string{"exec", "--json"},
		EnvironmentKeys: []string{"DEV_CREW_ATTACHMENT", "PATH"},
		Availability:    workers.AvailabilityAvailable,
	}})
	if err != nil {
		return Config{}, fmt.Errorf("run service Codex profile composition: %w", err)
	}
	adapter, err := workers.NewCodexAdapter(workers.CodexAdapterConfig{
		Profiles: profiles, ProfileID: codexConfig.ProfileID,
		ExpectedVersion: codexConfig.ExpectedVersion, SettleSignalVerified: false,
	})
	if err != nil {
		return Config{}, fmt.Errorf("run service Codex adapter composition: %w", err)
	}
	probe, err := adapter.ProbeVersion(ctx)
	if err != nil {
		return Config{}, fmt.Errorf("run service Codex version probe: %w", err)
	}
	if probe.Availability != application.HarnessAvailable || probe.Version != codexConfig.ExpectedVersion {
		return Config{}, errors.New("run service: exact Codex version is unavailable")
	}
	config.Repositories = registry
	config.Workspaces = registry
	config.TaskIDs = func(operationID string) (string, error) {
		return stableTaskIdentity(config.ServiceInstanceID, operationID), nil
	}
	config.RegistrationNonces = func() (string, error) { return randomIdentity("registration-nonce", 16) }
	config.WorkerHarnesses = exactWorkerHarnesses{profileID: codexConfig.ProfileID, adapter: adapter}
	return config, nil
}

type exactWorkerHarnesses struct {
	profileID string
	adapter   application.WorkerHarnessAdapter
}

func (harnesses exactWorkerHarnesses) ResolveWorkerHarness(profileID string) (application.WorkerHarnessAdapter, error) {
	if profileID != harnesses.profileID || harnesses.adapter == nil {
		return nil, errors.New("worker profile is unavailable")
	}
	return harnesses.adapter, nil
}

func stableTaskIdentity(serviceInstanceID, operationID string) string {
	digest := fmt.Sprintf("%x", sha256.Sum256([]byte(serviceInstanceID+"\x00"+operationID)))
	return "task-" + digest[:24]
}

func composeComisControl(config Config, mutations comiswire.DurableControlMutations) (ComisControl, error) {
	if config.ComisComposition == nil {
		return config.ComisControl, nil
	}
	if mutations == nil {
		return nil, errors.New("run service: Comis control requires durable mutations")
	}
	credential, err := readOwnerCredential(config.ComisComposition.CredentialFile)
	if err != nil {
		return nil, err
	}
	handler, err := comiswire.NewDurableControlHandler(comiswire.DurableControlHandlerConfig{
		Mutations: mutations, ServiceInstanceID: comiswire.ServiceInstanceID(config.ServiceInstanceID),
	})
	if err != nil {
		return nil, fmt.Errorf("run service Comis handler: %w", err)
	}
	connection, err := comiswire.NewControlConnection(comiswire.ControlConnectionConfig{
		SocketPath: config.ComisComposition.SocketPath, Credential: credential,
		ServiceInstanceID:    comiswire.ServiceInstanceID(config.ServiceInstanceID),
		HandshakeOperationID: comiswire.OperationID(config.ComisComposition.HandshakeOperationID),
		Handler:              handler, RequestTimeout: comisRequestTimeout,
		MinimumBackoff: comisMinimumBackoff, MaximumBackoff: comisMaximumBackoff,
	})
	if err != nil {
		return nil, fmt.Errorf("run service Comis connection: %w", err)
	}
	return connection, nil
}

func randomIdentity(prefix string, entropyBytes int) (string, error) {
	entropy := make([]byte, entropyBytes)
	if _, err := io.ReadFull(rand.Reader, entropy); err != nil {
		return "", fmt.Errorf("generate %s identity: %w", prefix, err)
	}
	return prefix + "-" + hex.EncodeToString(entropy), nil
}

func readOwnerCredential(path string) (string, error) {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return "", errors.New("run service Comis credential: path must be absolute and canonical")
	}
	info, err := os.Lstat(path)
	if err != nil {
		return "", fmt.Errorf("run service Comis credential: inspect file: %w", err)
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0o077 != 0 || info.Size() > 512 {
		return "", errors.New("run service Comis credential: file must be owner-private, regular, and bounded")
	}
	file, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("run service Comis credential: open file: %w", err)
	}
	defer func() { _ = file.Close() }()
	opened, err := file.Stat()
	if err != nil || !os.SameFile(info, opened) {
		return "", errors.New("run service Comis credential: file identity changed during open")
	}
	contents, err := io.ReadAll(io.LimitReader(file, 513))
	if err != nil {
		return "", fmt.Errorf("run service Comis credential: read file: %w", err)
	}
	if len(contents) > 512 {
		return "", errors.New("run service Comis credential: content exceeds the byte limit")
	}
	credential := strings.TrimSuffix(string(contents), "\n")
	credential = strings.TrimSuffix(credential, "\r")
	if credential == "" || strings.ContainsAny(credential, "\r\n\t ") {
		return "", errors.New("run service Comis credential: content is invalid")
	}
	return credential, nil
}
