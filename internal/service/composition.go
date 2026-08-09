package service

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/comisai/comis-dev-crew/internal/comiswire"
	devgit "github.com/comisai/comis-dev-crew/internal/git"
)

func composeInstalledRuntime(ctx context.Context, config Config) (Config, error) {
	configured := config.RepositoryComposition != nil || config.ComisComposition != nil || config.FixtureComposition != nil
	if !configured {
		return config, nil
	}
	if config.RepositoryComposition == nil || config.ComisComposition == nil || config.FixtureComposition == nil ||
		config.MCPSocketPath == "" || config.ServiceInstanceID == "" {
		return Config{}, errors.New("run service: installed composition is incomplete")
	}
	if config.Repositories != nil || config.TaskIDs != nil || config.RegistrationNonces != nil ||
		config.RequestedWorkspaceRoot != "" || config.ComisControl != nil {
		return Config{}, errors.New("run service: installed and injected composition cannot be combined")
	}
	repositoryConfig := config.RepositoryComposition
	registry, err := devgit.NewRegistry(ctx, devgit.RegistryConfig{
		GitExecutable: repositoryConfig.GitExecutable,
		ApprovedRoots: []string{repositoryConfig.ApprovedRoot},
		Repositories: []devgit.RepositoryConfig{{
			ID: repositoryConfig.RepositoryID, PrimaryCheckout: repositoryConfig.PrimaryCheckout,
			WorktreeRoot: repositoryConfig.WorktreeRoot,
		}},
	})
	if err != nil {
		return Config{}, fmt.Errorf("run service repository composition: %w", err)
	}
	if _, err := registry.ValidateWorktree(ctx, repositoryConfig.RepositoryID, repositoryConfig.WorkspaceRoot); err != nil {
		return Config{}, fmt.Errorf("run service fixture workspace: %w", err)
	}
	decision := config.FixtureComposition.Decision
	if strings.TrimSpace(decision) == "" || strings.TrimSpace(decision) != decision || len([]byte(decision)) > 1024 {
		return Config{}, errors.New("run service: fixture decision is invalid")
	}
	config.Repositories = registry
	config.TaskIDs = func() (string, error) { return randomIdentity("task", 12) }
	config.RegistrationNonces = func() (string, error) { return randomIdentity("registration-nonce", 16) }
	config.RequestedWorkspaceRoot = repositoryConfig.WorkspaceRoot
	return config, nil
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
