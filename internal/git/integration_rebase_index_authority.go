package git

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"

	"github.com/comisai/comis-dev-crew/internal/application"
)

func (registry *Registry) preflightRebasePatches(
	ctx context.Context,
	repository Repository,
	request application.IntegrationAdapterRequest,
	directory string,
	commits []string,
) error {
	return registry.withTemporaryRebaseIndex(ctx, request.Target.WorktreePath, directory,
		func(workspace gitWorkspaceEnvironment) error {
			if _, err := runGitBytesInWorkspace(ctx, registry.gitExecutable, workspace,
				"read-tree", request.Target.ExpectedHead); err != nil {
				return errors.New("apply integration candidate: rebase sequence proof is unavailable")
			}
			for _, commit := range commits {
				patch, err := registry.rebaseCommitPatch(ctx, repository, commit)
				if err != nil {
					return err
				}
				_, reverseCode, reverseErr := executeGitWithEnvironmentInputAndOutputLimit(
					ctx, registry.gitExecutable, &workspace, patch, 4096,
					"apply", "--cached", "--reverse", "--check", "-",
				)
				if reverseErr != nil || reverseCode != 0 && reverseCode != 1 {
					return errors.New("apply integration candidate: rebase sequence proof is unavailable")
				}
				if reverseCode == 0 {
					return errors.New("apply integration candidate: target subsumes candidate content")
				}
				_, exitCode, err := executeGitWithEnvironmentInputAndOutputLimit(
					ctx, registry.gitExecutable, &workspace, patch, 4096,
					"apply", "--cached", "--3way", "--whitespace=nowarn", "-",
				)
				if err != nil {
					return errors.New("apply integration candidate: rebase sequence proof is unavailable")
				}
				switch exitCode {
				case 0:
					continue
				case 1:
					return nil
				default:
					return errors.New("apply integration candidate: rebase sequence proof is unavailable")
				}
			}
			return nil
		})
}

func (registry *Registry) validateReconstructedRebaseConflict(
	ctx context.Context,
	repository Repository,
	request application.IntegrationAdapterRequest,
	directory string,
	rebaseHead string,
	conflicts []string,
) error {
	return registry.withTemporaryRebaseIndex(ctx, request.Target.WorktreePath, directory,
		func(workspace gitWorkspaceEnvironment) error {
			if _, err := runGitBytesInWorkspace(ctx, registry.gitExecutable, workspace,
				"read-tree", "HEAD"); err != nil {
				return errors.New("apply integration candidate: interrupted conflict base is unavailable")
			}
			patch, err := registry.rebaseCommitPatch(ctx, repository, rebaseHead)
			if err != nil {
				return err
			}
			_, exitCode, err := executeGitWithEnvironmentInputAndOutputLimit(
				ctx, registry.gitExecutable, &workspace, patch, 4096,
				"apply", "--cached", "--3way", "--whitespace=nowarn", "-",
			)
			if err != nil || exitCode != 1 {
				return errors.New("apply integration candidate: interrupted conflict cannot be reconstructed")
			}
			expectedConflicts, err := rebaseIndexOutput(ctx, registry.gitExecutable, workspace,
				"diff", "--name-only", "--diff-filter=U", "-z")
			if err != nil || !sameRebasePathEncoding(expectedConflicts, conflicts) {
				return errors.New("apply integration candidate: interrupted conflict paths differ")
			}
			expected, err := rebaseIndexOutput(ctx, registry.gitExecutable, workspace,
				"ls-files", "--stage", "-z")
			if err != nil {
				return errors.New("apply integration candidate: interrupted conflict index is unavailable")
			}
			current, err := runGitBytesWithLimit(ctx, maximumRebasePatchBytes, registry.gitExecutable,
				"--no-optional-locks", "-C", request.Target.WorktreePath, "ls-files", "--stage", "-z")
			if err != nil || !bytes.Equal(expected, current) {
				return errors.New("apply integration candidate: interrupted conflict index differs")
			}
			return nil
		})
}

func rebaseIndexOutput(
	ctx context.Context,
	executable string,
	workspace gitWorkspaceEnvironment,
	arguments ...string,
) ([]byte, error) {
	output, exitCode, err := executeGitWithEnvironmentAndOutputLimit(
		ctx, executable, &workspace, maximumRebasePatchBytes, arguments...,
	)
	if err != nil || exitCode != 0 {
		return nil, errors.New("apply integration candidate: temporary rebase index inspection failed")
	}
	return output, nil
}

func (registry *Registry) rebaseCommitPatch(
	ctx context.Context,
	repository Repository,
	commit string,
) ([]byte, error) {
	patch, err := runGitBytesWithLimit(ctx, maximumRebasePatchBytes, registry.gitExecutable,
		"--no-optional-locks", "-C", repository.PrimaryCheckout, "diff-tree", "--no-commit-id", "-p",
		"--binary", "--full-index", "--no-renames", commit+"^", commit)
	if err != nil {
		return nil, errors.New("apply integration candidate: rebase sequence proof is unavailable")
	}
	return patch, nil
}

func (registry *Registry) withTemporaryRebaseIndex(
	ctx context.Context,
	worktreePath string,
	directory string,
	inspect func(gitWorkspaceEnvironment) error,
) (resultErr error) {
	gitDirectory, err := runGit(ctx, registry.gitExecutable, "--no-optional-locks", "-C", worktreePath,
		"rev-parse", "--absolute-git-dir")
	if err != nil || !filepath.IsAbs(gitDirectory) {
		return errors.New("apply integration candidate: rebase repository identity is unavailable")
	}
	file, err := os.CreateTemp(directory, ".rebase-index-")
	if err != nil {
		return errors.New("apply integration candidate: temporary rebase index is unavailable")
	}
	indexPath := file.Name()
	if closeErr := file.Close(); closeErr != nil {
		return errors.Join(
			errors.New("apply integration candidate: temporary rebase index is unavailable"),
			removeTemporaryRebaseIndex(indexPath),
		)
	}
	if err := os.Remove(indexPath); err != nil {
		return errors.New("apply integration candidate: temporary rebase index is unavailable")
	}
	commonDirectory, err := runGit(ctx, registry.gitExecutable, "--no-optional-locks", "-C", worktreePath,
		"rev-parse", "--path-format=absolute", "--git-common-dir")
	if err != nil || !filepath.IsAbs(commonDirectory) {
		return errors.Join(
			errors.New("apply integration candidate: rebase object identity is unavailable"),
			removeTemporaryRebaseIndex(indexPath),
		)
	}
	objectDirectory, err := os.MkdirTemp(directory, ".rebase-objects-")
	if err != nil {
		return errors.Join(
			errors.New("apply integration candidate: temporary rebase objects are unavailable"),
			removeTemporaryRebaseIndex(indexPath),
		)
	}
	defer func() {
		resultErr = errors.Join(
			resultErr,
			removeTemporaryRebaseIndex(indexPath),
			removeTemporaryRebaseObjects(objectDirectory),
		)
	}()
	return inspect(gitWorkspaceEnvironment{
		gitDir: gitDirectory, gitWorkTree: worktreePath, gitIndex: indexPath,
		gitObjectDirectory:          objectDirectory,
		gitAlternateObjectDirectory: filepath.Join(commonDirectory, "objects"),
	})
}

func removeTemporaryRebaseIndex(path string) error {
	for _, candidate := range []string{path, path + ".lock"} {
		info, err := os.Lstat(candidate)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil || !info.Mode().IsRegular() {
			return errors.New("apply integration candidate: temporary rebase index is invalid")
		}
		if err := os.Remove(candidate); err != nil {
			return errors.New("apply integration candidate: temporary rebase index could not be removed")
		}
	}
	return nil
}

func removeTemporaryRebaseObjects(path string) error {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("apply integration candidate: temporary rebase objects are invalid")
	}
	if err := filepath.WalkDir(path, func(_ string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.Type()&os.ModeSymlink != 0 || !entry.IsDir() && !entry.Type().IsRegular() {
			return errors.New("temporary rebase object entry is invalid")
		}
		return nil
	}); err != nil {
		return errors.New("apply integration candidate: temporary rebase objects are invalid")
	}
	if err := os.RemoveAll(path); err != nil {
		return errors.New("apply integration candidate: temporary rebase objects could not be removed")
	}
	return nil
}

func sameRebasePathEncoding(encoded []byte, paths []string) bool {
	if len(encoded) == 0 || encoded[len(encoded)-1] != 0 {
		return false
	}
	entries := bytes.Split(encoded[:len(encoded)-1], []byte{0})
	if len(entries) != len(paths) {
		return false
	}
	for index := range entries {
		if string(entries[index]) != paths[index] {
			return false
		}
	}
	return true
}
