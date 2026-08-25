package git

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sort"
)

const (
	maximumCandidateSubmodules     = 64
	maximumCandidateSubmoduleDepth = 8
)

type candidateSubmoduleBudget struct {
	count int
}

func (registry *Registry) candidateSubmodulesClean(
	ctx context.Context,
	worktreePath string,
	authorityRoot string,
	entries map[string]candidateTrackedEntry,
	scratchParent string,
	budget *candidateSubmoduleBudget,
	depth int,
) (clean bool, returnErr error) {
	names := make([]string, 0)
	for name, entry := range entries {
		if entry.mode == "160000" {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	root, err := os.OpenRoot(worktreePath)
	if err != nil {
		return false, errors.New("candidate submodule root is unavailable")
	}
	defer func() { returnErr = errors.Join(returnErr, root.Close()) }()
	for _, name := range names {
		if err := ctx.Err(); err != nil {
			return false, err
		}
		if depth >= maximumCandidateSubmoduleDepth || budget.count == maximumCandidateSubmodules {
			return false, errors.New("candidate submodule inspection exceeds its bound")
		}
		identity, err := root.Lstat(name)
		if err != nil || !identity.IsDir() || identity.Mode()&os.ModeSymlink != 0 {
			return false, nil
		}
		nested := filepath.Join(worktreePath, filepath.FromSlash(name))
		common, gitDirectory, safe, err := registry.candidateSubmoduleCommonDirectory(ctx, nested, authorityRoot)
		if err != nil {
			return false, err
		}
		if !safe {
			return false, nil
		}
		budget.count++
		clean, err := registry.candidateWorkspaceCleanAtCommit(
			ctx, nested, common, entries[name].objectID, scratchParent, authorityRoot,
			gitDirectory, budget, depth+1,
		)
		if err != nil || !clean {
			return false, err
		}
		final, err := root.Lstat(name)
		if err != nil || !final.IsDir() || final.Mode()&os.ModeSymlink != 0 || !os.SameFile(identity, final) {
			return false, nil
		}
	}
	return true, nil
}

func (registry *Registry) candidateSubmoduleCommonDirectory(
	ctx context.Context,
	worktreePath string,
	authorityRoot string,
) (string, string, bool, error) {
	gitDirectory, err := runGit(ctx, registry.gitExecutable, "--no-optional-locks", "-C", worktreePath,
		"rev-parse", "--absolute-git-dir")
	if err != nil {
		return "", "", false, nil
	}
	common, err := runGit(ctx, registry.gitExecutable, "--no-optional-locks", "-C", worktreePath,
		"rev-parse", "--path-format=absolute", "--git-common-dir")
	if err != nil {
		return "", "", false, errors.New("candidate submodule common directory is unavailable")
	}
	canonicalGit, gitErr := filepath.EvalSymlinks(gitDirectory)
	canonicalCommon, commonErr := filepath.EvalSymlinks(common)
	canonicalWorktree, worktreeErr := filepath.EvalSymlinks(worktreePath)
	if gitErr != nil || commonErr != nil || worktreeErr != nil || canonicalWorktree != worktreePath ||
		(!pathWithin(authorityRoot, canonicalGit, false) && !pathWithin(worktreePath, canonicalGit, true)) ||
		(!pathWithin(authorityRoot, canonicalCommon, false) && !pathWithin(worktreePath, canonicalCommon, true)) {
		return "", "", false, errors.New("candidate submodule Git identity is unsafe")
	}
	return canonicalCommon, canonicalGit, true, nil
}
