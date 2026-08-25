package git

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/comisai/comis-dev-crew/internal/application"
)

type isolatedRebaseResult struct {
	head       string
	commits    []string
	conflicted bool
}

func (registry *Registry) preflightRebasePatches(
	ctx context.Context,
	repository Repository,
	request application.IntegrationAdapterRequest,
	directory string,
	commits []string,
	patches []string,
) (isolatedRebaseResult, error) {
	var result isolatedRebaseResult
	err := registry.withIsolatedRebaseWorkspace(ctx, request.Target.WorktreePath, directory,
		func(workspace gitWorkspaceEnvironment) error {
			branch := "refs/heads/rebase-proof"
			if _, err := runGitBytesInWorkspace(ctx, registry.gitExecutable, workspace,
				"update-ref", branch, request.Candidate.HeadRevision); err != nil {
				return errors.New("apply integration candidate: isolated rebase head is unavailable")
			}
			if _, err := runGitBytesInWorkspace(ctx, registry.gitExecutable, workspace,
				"symbolic-ref", "HEAD", branch); err != nil {
				return errors.New("apply integration candidate: isolated rebase attachment is unavailable")
			}
			if _, err := runGitBytesInWorkspace(ctx, registry.gitExecutable, workspace,
				"reset", "--hard", request.Candidate.HeadRevision); err != nil {
				return errors.New("apply integration candidate: isolated rebase checkout is unavailable")
			}
			arguments := []string{
				"-c", "core.hooksPath=/dev/null", "-c", "core.fsmonitor=false", "-c", "commit.gpgSign=false",
				"-c", "user.name=DevCrew Integration", "-c", "user.email=integration@example.invalid",
				"rebase", "--no-autostash", "--no-stat", "--reapply-cherry-picks", "--keep-empty",
				"--committer-date-is-author-date", "--onto", request.Target.ExpectedHead,
				request.Candidate.BaseRevision, strings.TrimPrefix(branch, "refs/heads/"),
			}
			_, exitCode, err := executeGitWithEnvironmentAndOutputLimit(
				ctx, registry.gitExecutable, &workspace, maximumGitOutputBytes, arguments...,
			)
			if err != nil {
				return errors.New("apply integration candidate: isolated rebase execution is unavailable")
			}
			switch exitCode {
			case 0:
				var validationErr error
				result, validationErr = registry.validateIsolatedRebaseResult(ctx, workspace, request, commits, patches)
				if validationErr != nil {
					return validationErr
				}
				return importIsolatedGitObjects(workspace.gitObjectDirectory, workspace.gitAlternateObjectDirectory)
			case 1:
				if err := registry.validateIsolatedRebaseConflict(ctx, repository, workspace, commits); err != nil {
					return err
				}
				result.conflicted = true
				return nil
			default:
				return errors.New("apply integration candidate: isolated rebase execution failed")
			}
		})
	return result, err
}

func (registry *Registry) validateIsolatedRebaseResult(
	ctx context.Context,
	workspace gitWorkspaceEnvironment,
	request application.IntegrationAdapterRequest,
	commits []string,
	patches []string,
) (isolatedRebaseResult, error) {
	output, err := runGitBytesInWorkspace(ctx, registry.gitExecutable, workspace,
		"rev-list", "--reverse", request.Target.ExpectedHead+"..HEAD")
	if err != nil {
		return isolatedRebaseResult{}, errors.New("apply integration candidate: isolated rebase result is unavailable")
	}
	results := strings.Fields(string(output))
	if len(results) != len(commits) {
		return isolatedRebaseResult{}, errors.New("apply integration candidate: isolated rebase dropped candidate commits")
	}
	for index, result := range results {
		identity, err := registry.rebasePatchIdentityInWorkspace(ctx, workspace, result)
		if err != nil || identity != patches[index] {
			return isolatedRebaseResult{}, errors.New("apply integration candidate: isolated rebase content differs")
		}
	}
	return isolatedRebaseResult{head: results[len(results)-1], commits: results}, nil
}

func importIsolatedGitObjects(source, destination string) error {
	if !filepath.IsAbs(source) || !filepath.IsAbs(destination) || source == destination {
		return errors.New("apply integration candidate: isolated object boundary is invalid")
	}
	changedDirectories := make(map[string]struct{})
	err := filepath.WalkDir(source, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		info, err := entry.Info()
		if err != nil || !info.Mode().IsRegular() {
			return errors.New("isolated object entry is invalid")
		}
		relative, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		parts := strings.Split(filepath.ToSlash(relative), "/")
		if len(parts) != 2 || len(parts[0]) != 2 || len(parts[1]) != 38 && len(parts[1]) != 62 ||
			!lowerHex(parts[0]+parts[1]) {
			return errors.New("isolated object identity is invalid")
		}
		directory := filepath.Join(destination, parts[0])
		if err := os.MkdirAll(directory, 0o755); err != nil {
			return err
		}
		target := filepath.Join(directory, parts[1])
		if existing, err := os.Lstat(target); err == nil {
			if !existing.Mode().IsRegular() {
				return errors.New("shared object identity is invalid")
			}
			return nil
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		if err := os.Link(path, target); err != nil {
			return err
		}
		changedDirectories[directory] = struct{}{}
		return nil
	})
	if err != nil {
		return errors.New("apply integration candidate: isolated result objects could not be imported")
	}
	for directory := range changedDirectories {
		if err := syncDirectory(directory); err != nil {
			return err
		}
	}
	return syncDirectory(destination)
}

func lowerHex(value string) bool {
	for _, character := range value {
		if character < '0' || character > '9' {
			if character < 'a' || character > 'f' {
				return false
			}
		}
	}
	return value != ""
}

func (registry *Registry) validateIsolatedRebaseConflict(
	ctx context.Context,
	repository Repository,
	workspace gitWorkspaceEnvironment,
	commits []string,
) error {
	rebaseHead, err := runGitInWorkspace(ctx, registry.gitExecutable, workspace,
		"rev-parse", "--verify", "REBASE_HEAD^{commit}")
	if err != nil || len(commits) == 0 || rebaseHead != commits[len(commits)-1] {
		return errors.New("apply integration candidate: isolated rebase sequence cannot be proven")
	}
	conflicts, err := rebaseIndexOutput(ctx, registry.gitExecutable, workspace,
		"diff", "--name-only", "--diff-filter=U", "-z")
	if err != nil || len(conflicts) == 0 {
		return errors.New("apply integration candidate: isolated rebase conflict is unavailable")
	}
	changed, err := runGitBytesWithLimit(ctx, maximumRebasePatchBytes, registry.gitExecutable,
		"--no-optional-locks", "-C", repository.PrimaryCheckout, "diff-tree", "--no-commit-id",
		"--name-only", "-z", "--no-renames", rebaseHead+"^", rebaseHead)
	if err != nil || !conflictPathsBelongToCommit(conflicts, changed) {
		return errors.New("apply integration candidate: rebase engine relocates candidate conflicts")
	}
	return nil
}

func conflictPathsBelongToCommit(conflicts []byte, changed []byte) bool {
	changedPaths := make(map[string]struct{})
	for _, path := range bytes.Split(bytes.TrimSuffix(changed, []byte{0}), []byte{0}) {
		changedPaths[string(path)] = struct{}{}
	}
	for _, path := range bytes.Split(bytes.TrimSuffix(conflicts, []byte{0}), []byte{0}) {
		if _, found := changedPaths[string(path)]; !found {
			return false
		}
	}
	return true
}

func (registry *Registry) rebasePatchIdentityInWorkspace(
	ctx context.Context,
	workspace gitWorkspaceEnvironment,
	revision string,
) (string, error) {
	patch, err := rebaseIndexOutput(ctx, registry.gitExecutable, workspace,
		"show", "--format=%H", "--no-color", "--no-ext-diff", "--no-textconv",
		"--no-renames", "--full-index", "--binary", revision)
	if err != nil {
		return "", err
	}
	output, exitCode, err := executeGitWithEnvironmentInputAndOutputLimit(
		ctx, registry.gitExecutable, &workspace, patch, 256, "patch-id", "--verbatim")
	if err != nil || exitCode != 0 {
		return "", errors.New("apply integration candidate: isolated patch identity is unavailable")
	}
	fields := strings.Fields(string(output))
	if len(fields) == 0 {
		return "-", nil
	}
	if len(fields) != 2 || !gitRevisionPattern.MatchString(fields[0]) || fields[1] != revision {
		return "", errors.New("apply integration candidate: isolated patch identity is invalid")
	}
	return fields[0], nil
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

func (registry *Registry) withIsolatedRebaseWorkspace(
	ctx context.Context,
	worktreePath string,
	directory string,
	inspect func(gitWorkspaceEnvironment) error,
) (resultErr error) {
	root, err := os.MkdirTemp(directory, ".rebase-workspace-")
	if err != nil {
		return errors.New("apply integration candidate: isolated rebase workspace is unavailable")
	}
	rootInfo, err := os.Lstat(root)
	if err != nil || !rootInfo.IsDir() || rootInfo.Mode()&os.ModeSymlink != 0 {
		return errors.New("apply integration candidate: isolated rebase workspace is invalid")
	}
	defer func() {
		resultErr = errors.Join(resultErr, removeIsolatedRebaseWorkspace(directory, root, rootInfo))
	}()
	if _, err := runGitBytes(ctx, registry.gitExecutable, "init", "--quiet", "--template=", root); err != nil {
		return errors.New("apply integration candidate: isolated rebase repository is unavailable")
	}
	commonDirectory, err := runGit(ctx, registry.gitExecutable, "--no-optional-locks", "-C", worktreePath,
		"rev-parse", "--path-format=absolute", "--git-common-dir")
	if err != nil || !filepath.IsAbs(commonDirectory) {
		return errors.New("apply integration candidate: rebase object identity is unavailable")
	}
	return inspect(gitWorkspaceEnvironment{
		gitDir: filepath.Join(root, ".git"), gitWorkTree: root, gitIndex: filepath.Join(root, ".git", "index"),
		gitObjectDirectory:          filepath.Join(root, ".git", "objects"),
		gitAlternateObjectDirectory: filepath.Join(commonDirectory, "objects"),
	})
}

func removeIsolatedRebaseWorkspace(parent, root string, identity os.FileInfo) error {
	if !filepath.IsAbs(parent) || !filepath.IsAbs(root) || filepath.Dir(root) != parent ||
		!strings.HasPrefix(filepath.Base(root), ".rebase-workspace-") {
		return errors.New("apply integration candidate: isolated rebase workspace is invalid")
	}
	info, err := os.Lstat(root)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || !os.SameFile(identity, info) {
		return errors.New("apply integration candidate: isolated rebase workspace is invalid")
	}
	if err := os.RemoveAll(root); err != nil {
		return errors.New("apply integration candidate: isolated rebase workspace could not be removed")
	}
	return nil
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
		return errors.Join(errors.New("apply integration candidate: temporary rebase index is unavailable"),
			removeTemporaryRebaseIndex(indexPath))
	}
	if err := os.Remove(indexPath); err != nil {
		return errors.New("apply integration candidate: temporary rebase index is unavailable")
	}
	commonDirectory, err := runGit(ctx, registry.gitExecutable, "--no-optional-locks", "-C", worktreePath,
		"rev-parse", "--path-format=absolute", "--git-common-dir")
	if err != nil || !filepath.IsAbs(commonDirectory) {
		return errors.Join(errors.New("apply integration candidate: rebase object identity is unavailable"),
			removeTemporaryRebaseIndex(indexPath))
	}
	objectDirectory, err := os.MkdirTemp(directory, ".rebase-objects-")
	if err != nil {
		return errors.Join(errors.New("apply integration candidate: temporary rebase objects are unavailable"),
			removeTemporaryRebaseIndex(indexPath))
	}
	defer func() {
		resultErr = errors.Join(resultErr, removeTemporaryRebaseIndex(indexPath),
			removeTemporaryRebaseObjects(objectDirectory))
	}()
	return inspect(gitWorkspaceEnvironment{
		gitDir: gitDirectory, gitWorkTree: worktreePath, gitIndex: indexPath,
		gitObjectDirectory: objectDirectory, gitAlternateObjectDirectory: filepath.Join(commonDirectory, "objects"),
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
