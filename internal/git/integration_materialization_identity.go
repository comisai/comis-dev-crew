package git

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"path/filepath"
)

func (registry *Registry) integrationIndexDigest(ctx context.Context, worktreePath string) (string, error) {
	workspace, err := registry.integrationMaterializationWorkspace(ctx, worktreePath)
	if err != nil {
		return "", err
	}
	file, err := openRegularFile(workspace.gitIndex)
	if err != nil {
		return "", errors.New("apply integration candidate: target index is unavailable")
	}
	digest := sha256.New()
	_, copyErr := io.Copy(digest, file)
	closeErr := file.Close()
	if copyErr != nil || closeErr != nil {
		return "", errors.New("apply integration candidate: target index could not be read")
	}
	return hex.EncodeToString(digest.Sum(nil)), nil
}

func (registry *Registry) integrationMaterializationWorkspace(
	ctx context.Context,
	worktreePath string,
) (gitWorkspaceEnvironment, error) {
	gitDirectory, err := runGit(ctx, registry.gitExecutable, "--no-optional-locks", "-C", worktreePath,
		"rev-parse", "--absolute-git-dir")
	if err != nil || !filepath.IsAbs(gitDirectory) {
		return gitWorkspaceEnvironment{}, errors.New("apply integration candidate: target index identity is unavailable")
	}
	return gitWorkspaceEnvironment{
		gitDir: gitDirectory, gitWorkTree: worktreePath, gitIndex: filepath.Join(gitDirectory, "index"),
	}, nil
}

func (registry *Registry) integrationCommitTree(ctx context.Context, worktreePath, head string) (string, error) {
	tree, err := runGit(ctx, registry.gitExecutable, "--no-optional-locks", "-C", worktreePath,
		"rev-parse", "--verify", head+"^{tree}")
	if err != nil || !gitRevisionPattern.MatchString(tree) {
		return "", errors.New("apply integration candidate: materialization tree is unavailable")
	}
	return tree, nil
}
