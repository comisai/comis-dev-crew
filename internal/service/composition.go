package service

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/comisai/comis-dev-crew/internal/application"
	"github.com/comisai/comis-dev-crew/internal/comiswire"
	"github.com/comisai/comis-dev-crew/internal/domain"
	"github.com/comisai/comis-dev-crew/internal/forge"
	devgit "github.com/comisai/comis-dev-crew/internal/git"
	"github.com/comisai/comis-dev-crew/internal/validation"
	"github.com/comisai/comis-dev-crew/internal/workers"
)

func composeInstalledRuntime(ctx context.Context, config Config) (Config, error) {
	configured := config.RepositoryComposition != nil || config.ComisComposition != nil || config.CodexComposition != nil || config.ClaudeComposition != nil ||
		config.ValidationComposition != nil || config.ForgeComposition != nil
	if !configured {
		return config, nil
	}
	if config.RepositoryComposition == nil || config.ComisComposition == nil || config.CodexComposition == nil ||
		config.ValidationComposition == nil || config.ForgeComposition == nil ||
		config.MCPSocketPath == "" || config.RuntimeRoot == "" || config.ServiceInstanceID == "" {
		return Config{}, errors.New("run service: installed composition is incomplete")
	}
	if config.Repositories != nil || config.Workspaces != nil || config.TaskIDs != nil ||
		config.RuntimeAttachments != nil || config.WorkerHarnesses != nil || config.RegistrationNonces != nil || config.ComisControl != nil ||
		config.candidateGit != nil || config.workspaceInspector != nil || config.validationCatalog != nil || config.pullRequests != nil ||
		config.cleanupRemover != nil || config.cleanupForge != nil ||
		config.fixtureCandidatePreparer != nil ||
		config.validationMaxOutputBytes != 0 || config.validationPollInterval != 0 {
		return Config{}, errors.New("run service: installed and injected composition cannot be combined")
	}
	if config.FixtureComposition != nil {
		decision := config.FixtureComposition.Decision
		artifact := config.FixtureComposition.ArtifactRelativePath
		if strings.TrimSpace(decision) == "" || strings.TrimSpace(decision) != decision || len([]byte(decision)) > 1024 ||
			strings.TrimSpace(artifact) == "" || strings.TrimSpace(artifact) != artifact || len([]byte(artifact)) > 128 ||
			artifact == "." || artifact == ".." || filepath.Base(artifact) != artifact || strings.ContainsAny(artifact, `/\`) {
			return Config{}, errors.New("run service: fixture composition is invalid")
		}
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
	profileConfig := []workers.StaticProfile{{
		ID: codexConfig.ProfileID, Harness: workers.HarnessCodex,
		AllowedShapes: []domain.TaskShape{domain.ShapeShip, domain.ShapeScout},
		Model:         codexConfig.Model, Effort: codexConfig.Effort,
		TerminalAllowEntry: codexConfig.TerminalAllowEntryID,
		Network:            codexConfig.Network, ConcurrencyLimit: codexConfig.ConcurrencyLimit,
		Unattended: true, Executable: codexConfig.Executable,
		Arguments: []string{"exec", "--json"},
		EnvironmentKeys: []string{
			application.RuntimeAttachmentPathEnvironment,
			application.RuntimeAttachmentTargetEnvironment,
			"PATH",
		},
		Availability: workers.AvailabilityAvailable,
	}}
	if config.ClaudeComposition != nil {
		claudeConfig := config.ClaudeComposition
		profileConfig = append(profileConfig, workers.StaticProfile{
			ID: claudeConfig.ProfileID, Harness: workers.HarnessClaude,
			AllowedShapes: []domain.TaskShape{domain.ShapeShip, domain.ShapeScout},
			Model:         claudeConfig.Model, Effort: claudeConfig.Effort,
			TerminalAllowEntry: claudeConfig.TerminalAllowEntryID,
			Network:            claudeConfig.Network, ConcurrencyLimit: claudeConfig.ConcurrencyLimit,
			Unattended: true, Executable: claudeConfig.Executable,
			Arguments: []string{"-p"},
			EnvironmentKeys: []string{
				application.RuntimeAttachmentPathEnvironment,
				application.RuntimeAttachmentTargetEnvironment,
				"CLAUDE_CONFIG_DIR",
				"PATH",
			},
			Availability: workers.AvailabilityAvailable,
		})
	}
	profiles, err := workers.NewProfileCatalog(profileConfig)
	if err != nil {
		return Config{}, fmt.Errorf("run service Codex profile composition: %w", err)
	}
	codexAdapter, err := workers.NewCodexAdapter(workers.CodexAdapterConfig{
		Profiles: profiles, ProfileID: codexConfig.ProfileID,
		ExpectedVersion: codexConfig.ExpectedVersion, SettleSignalVerified: false,
	})
	if err != nil {
		return Config{}, fmt.Errorf("run service Codex adapter composition: %w", err)
	}
	probe, err := codexAdapter.ProbeVersion(ctx)
	if err != nil {
		return Config{}, fmt.Errorf("run service Codex version probe: %w", err)
	}
	if probe.Availability != application.HarnessAvailable || probe.Version != codexConfig.ExpectedVersion {
		return Config{}, errors.New("run service: exact Codex version is unavailable")
	}
	harnessAdapters := map[string]application.WorkerHarnessAdapter{codexConfig.ProfileID: codexAdapter}
	if config.ClaudeComposition != nil {
		claudeConfig := config.ClaudeComposition
		claudeAdapter, claudeErr := workers.NewClaudeAdapter(workers.ClaudeAdapterConfig{
			Profiles: profiles, ProfileID: claudeConfig.ProfileID,
			ExpectedVersion: claudeConfig.ExpectedVersion, ConfigDirectory: claudeConfig.ConfigDirectory,
			SettleSignalVerified: false,
		})
		if claudeErr != nil {
			return Config{}, fmt.Errorf("run service Claude adapter composition: %w", claudeErr)
		}
		claudeProbe, claudeErr := claudeAdapter.ProbeVersion(ctx)
		if claudeErr != nil {
			return Config{}, fmt.Errorf("run service Claude version probe: %w", claudeErr)
		}
		if claudeProbe.Availability != application.HarnessAvailable || claudeProbe.Version != claudeConfig.ExpectedVersion {
			return Config{}, errors.New("run service: exact Claude version is unavailable")
		}
		harnessAdapters[claudeConfig.ProfileID] = claudeAdapter
	}
	validationConfig := config.ValidationComposition
	catalog, err := validation.NewCatalog(validation.CatalogConfig{
		Programs: validationConfig.Programs, Profiles: validationConfig.Profiles,
	})
	if err != nil {
		return Config{}, fmt.Errorf("run service validation composition: %w", err)
	}
	if validationConfig.MaxOutputBytes < 1 || validationConfig.MaxOutputBytes > 16<<20 ||
		validationConfig.PollInterval <= 0 || validationConfig.PollInterval > time.Minute {
		return Config{}, errors.New("run service: validation execution bounds are invalid")
	}
	forgeConfig := config.ForgeComposition
	readCredential, err := readOwnerCredential(forgeConfig.ReadCredentialFile)
	if err != nil {
		return Config{}, fmt.Errorf("run service forge read credential: %w", err)
	}
	pushCredential, err := readOwnerCredential(forgeConfig.PushCredentialFile)
	if err != nil {
		return Config{}, fmt.Errorf("run service forge push credential: %w", err)
	}
	if forgeConfig.ReadCredentialFile == forgeConfig.PushCredentialFile || readCredential == pushCredential {
		return Config{}, errors.New("run service: forge read and push identities must differ")
	}
	pusher, err := forge.NewGitBranchPusher(forge.GitBranchPusherConfig{
		GitExecutable: repositoryConfig.GitExecutable, RemoteURL: forgeConfig.RemoteURL,
		CredentialDirectory: forgeConfig.CredentialDirectory, LocalFixtureRemoteRoot: forgeConfig.LocalFixtureRemoteRoot,
		SSHTransportExecutable: forgeConfig.SSHTransportExecutable,
		SSHExecutable:          forgeConfig.SSHExecutable, SSHKnownHostsFile: forgeConfig.SSHKnownHostsFile,
	})
	if err != nil {
		return Config{}, fmt.Errorf("run service forge push composition: %w", err)
	}
	pullRequests, err := forge.NewGitHubAdapter(forge.GitHubConfig{
		APIBaseURL: forgeConfig.APIBaseURL, Owner: forgeConfig.Owner, Repository: forgeConfig.Repository,
		RepositoryIdentity: repositoryConfig.RepositoryID, BaseBranch: repositoryConfig.DefaultBranch,
		HTTPClient: &http.Client{Timeout: 30 * time.Second}, Pusher: pusher,
		ReadCredentials: ownerCredentialSource{
			path: forgeConfig.ReadCredentialFile, kind: forge.CredentialRead,
			scopes: []forge.CredentialScope{forge.ScopeContentsRead, forge.ScopePullRequestsRead, forge.ScopeChecksRead},
		},
		PushCredentials: ownerCredentialSource{
			path: forgeConfig.PushCredentialFile, kind: forge.CredentialPush,
			scopes: []forge.CredentialScope{forge.ScopeContentsWrite},
		},
	})
	if err != nil {
		return Config{}, fmt.Errorf("run service GitHub composition: %w", err)
	}
	config.Repositories = registry
	config.Workspaces = registry
	config.TaskIDs = func(operationID string) (string, error) {
		return stableTaskIdentity(config.ServiceInstanceID, operationID), nil
	}
	config.RegistrationNonces = func() (string, error) { return randomIdentity("registration-nonce", 16) }
	config.WorkerHarnesses = exactWorkerHarnesses{adapters: harnessAdapters}
	config.candidateGit = registry
	config.workspaceInspector = registry
	config.validationCatalog = catalog
	config.validationMaxOutputBytes = validationConfig.MaxOutputBytes
	config.validationPollInterval = validationConfig.PollInterval
	config.pullRequests = pullRequests
	config.cleanupRemover = registry
	config.cleanupForge = pullRequests
	if config.FixtureComposition != nil {
		config.fixtureCandidatePreparer = registry
	}
	return config, nil
}

type ownerCredentialSource struct {
	path   string
	kind   forge.CredentialKind
	scopes []forge.CredentialScope
}

func (source ownerCredentialSource) Resolve(ctx context.Context) (forge.Credential, error) {
	if ctx == nil {
		return forge.Credential{}, errors.New("resolve forge credential: context is required")
	}
	if err := ctx.Err(); err != nil {
		return forge.Credential{}, err
	}
	secret, err := readOwnerCredential(source.path)
	if err != nil {
		return forge.Credential{}, errors.New("resolve forge credential: owner file is unavailable")
	}
	return forge.Credential{Kind: source.kind, Secret: secret, Scopes: append([]forge.CredentialScope(nil), source.scopes...)}, nil
}

type exactWorkerHarnesses struct {
	adapters map[string]application.WorkerHarnessAdapter
}

func (harnesses exactWorkerHarnesses) ResolveWorkerHarness(profileID string) (application.WorkerHarnessAdapter, error) {
	adapter := harnesses.adapters[profileID]
	if adapter == nil {
		return nil, errors.New("worker profile is unavailable")
	}
	return adapter, nil
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

const maximumOwnerCredentialBytes = 4096

func readOwnerCredential(path string) (string, error) {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return "", errors.New("run service Comis credential: path must be absolute and canonical")
	}
	info, err := os.Lstat(path)
	if err != nil {
		return "", fmt.Errorf("run service Comis credential: inspect file: %w", err)
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0o077 != 0 || info.Size() > maximumOwnerCredentialBytes {
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
	contents, err := io.ReadAll(io.LimitReader(file, maximumOwnerCredentialBytes+1))
	if err != nil {
		return "", fmt.Errorf("run service Comis credential: read file: %w", err)
	}
	if len(contents) > maximumOwnerCredentialBytes {
		return "", errors.New("run service Comis credential: content exceeds the byte limit")
	}
	credential := strings.TrimSuffix(string(contents), "\n")
	credential = strings.TrimSuffix(credential, "\r")
	if credential == "" || strings.ContainsAny(credential, "\r\n\t ") {
		return "", errors.New("run service Comis credential: content is invalid")
	}
	return credential, nil
}
