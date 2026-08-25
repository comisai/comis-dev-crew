package git

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

const maximumCandidateIndexBytes = 64 << 20

type candidateTrackedEntry struct {
	mode     string
	objectID string
}

func (registry *Registry) candidateWorktreeCleanAtCommit(
	ctx context.Context,
	worktreePath string,
	commonDirectory string,
	head string,
) (clean bool, returnErr error) {
	budget := candidateSubmoduleBudget{}
	return registry.candidateWorkspaceCleanAtCommit(
		ctx, worktreePath, commonDirectory, head, filepath.Dir(worktreePath), commonDirectory, "", &budget, 0,
	)
}

func (registry *Registry) candidateWorkspaceCleanAtCommit(
	ctx context.Context,
	worktreePath string,
	commonDirectory string,
	head string,
	scratchParent string,
	authorityRoot string,
	expectedGitDirectory string,
	budget *candidateSubmoduleBudget,
	depth int,
) (clean bool, returnErr error) {
	returnErr = registry.withCandidateInspectionWorkspaceAt(
		ctx, worktreePath, commonDirectory, head, scratchParent, expectedGitDirectory,
		func(workspace gitWorkspaceEnvironment) error {
			indexOutput, err := runGitBytesInWorkspaceWithLimit(
				ctx, registry.gitExecutable, workspace, maximumIntegrationTreeListing,
				"ls-files", "--stage", "-z",
			)
			if err != nil {
				return errors.New("candidate index is unavailable")
			}
			index, err := parseCandidateTrackedEntries(indexOutput, true)
			if err != nil {
				return err
			}
			treeOutput, err := runGitBytesInWorkspaceWithLimit(
				ctx, registry.gitExecutable, workspace, maximumIntegrationTreeListing,
				"ls-tree", "-r", "-z", "--full-tree", head,
			)
			if err != nil {
				return errors.New("candidate head tree is unavailable")
			}
			tree, err := parseCandidateTrackedEntries(treeOutput, false)
			if err != nil {
				return err
			}
			if !sameCandidateTrackedEntries(index, tree) {
				clean = false
				return nil
			}
			attributesSafe, err := candidateConversionAttributesSafe(
				ctx, registry.gitExecutable, workspace, index,
			)
			if err != nil || !attributesSafe {
				return errors.New("candidate conversion attributes are unavailable")
			}
			status, err := runGitBytesInWorkspaceWithLimit(
				ctx, registry.gitExecutable, workspace, maximumIntegrationTreeListing,
				"-c", "core.excludesFile=/dev/null", "status", "--porcelain=v2", "-z",
				"--untracked-files=all", "--ignore-submodules=all",
			)
			if err != nil {
				return errors.New("candidate controlled status is unavailable")
			}
			if len(status) != 0 {
				clean = false
				return nil
			}
			clean, err = registry.candidateSubmodulesClean(
				ctx, worktreePath, authorityRoot, index, scratchParent, budget, depth,
			)
			return err
		},
	)
	return clean, returnErr
}

func parseCandidateTrackedEntries(output []byte, index bool) (map[string]candidateTrackedEntry, error) {
	entries := make(map[string]candidateTrackedEntry)
	for _, record := range bytes.Split(output, []byte{0}) {
		if len(record) == 0 {
			continue
		}
		metadata, encodedPath, found := bytes.Cut(record, []byte{'\t'})
		fields := bytes.Fields(metadata)
		if !found || len(fields) != 3 || len(entries) == maximumIntegrationTreeEntries {
			return nil, errors.New("candidate tracked entry is malformed")
		}
		mode, objectID, entryPath := string(fields[0]), string(fields[1]), string(encodedPath)
		if !index {
			objectID = string(fields[2])
		}
		if index && string(fields[2]) != "0" || !candidateTrackedMode(mode) ||
			!gitRevisionPattern.MatchString(objectID) || !validIntegrationTreePath(entryPath) {
			return nil, errors.New("candidate tracked entry is unsafe")
		}
		if !index {
			objectType := string(fields[1])
			if mode == "160000" && objectType != "commit" || mode != "160000" && objectType != "blob" {
				return nil, errors.New("candidate tracked entry type differs")
			}
		}
		if _, duplicate := entries[entryPath]; duplicate {
			return nil, errors.New("candidate tracked entry is duplicated")
		}
		entries[entryPath] = candidateTrackedEntry{mode: mode, objectID: objectID}
	}
	return entries, nil
}

func candidateTrackedMode(mode string) bool {
	return mode == "100644" || mode == "100755" || mode == "120000" || mode == "160000"
}

func sameCandidateTrackedEntries(left, right map[string]candidateTrackedEntry) bool {
	if len(left) != len(right) {
		return false
	}
	for name, entry := range left {
		if right[name] != entry {
			return false
		}
	}
	return true
}

func candidateConversionAttributesSafe(
	ctx context.Context,
	executable string,
	workspace gitWorkspaceEnvironment,
	entries map[string]candidateTrackedEntry,
) (bool, error) {
	input := make([]byte, 0)
	for name, entry := range entries {
		if entry.mode == "160000" {
			continue
		}
		input = append(input, name...)
		input = append(input, 0)
		if len(input) > maximumIntegrationTreeListing {
			return false, errors.New("candidate attribute input exceeds its bound")
		}
	}
	output, err := runGitBytesInWorkspaceWithInputAndLimit(
		ctx, executable, workspace, input, maximumIntegrationTreeListing,
		"check-attr", "-z", "--stdin", "filter", "working-tree-encoding",
	)
	if err != nil {
		return false, err
	}
	fields := bytes.Split(output, []byte{0})
	if len(fields) > 0 && len(fields[len(fields)-1]) == 0 {
		fields = fields[:len(fields)-1]
	}
	if len(fields)%3 != 0 {
		return false, errors.New("candidate attribute response is malformed")
	}
	for offset := 0; offset < len(fields); offset += 3 {
		value := string(fields[offset+2])
		if value != "unspecified" && value != "unset" {
			return false, nil
		}
	}
	return true, nil
}

func (registry *Registry) withCandidateInspectionWorkspace(
	ctx context.Context,
	worktreePath string,
	commonDirectory string,
	head string,
	inspect func(gitWorkspaceEnvironment) error,
) (returnErr error) {
	return registry.withCandidateInspectionWorkspaceAt(
		ctx, worktreePath, commonDirectory, head, filepath.Dir(worktreePath), "", inspect,
	)
}

func (registry *Registry) withCandidateInspectionWorkspaceAt(
	ctx context.Context,
	worktreePath string,
	commonDirectory string,
	head string,
	scratchParent string,
	expectedGitDirectory string,
	inspect func(gitWorkspaceEnvironment) error,
) (returnErr error) {
	root, err := os.MkdirTemp(scratchParent, ".candidate-inspection-")
	if err != nil {
		return errors.New("candidate inspection workspace is unavailable")
	}
	identity, err := os.Lstat(root)
	if err != nil || !identity.IsDir() || identity.Mode()&os.ModeSymlink != 0 {
		return errors.New("candidate inspection workspace is invalid")
	}
	defer func() {
		returnErr = errors.Join(returnErr, removeCandidateInspectionWorkspace(scratchParent, root, identity))
	}()
	gitDirectory := filepath.Join(root, ".git")
	for _, directory := range []string{
		gitDirectory, filepath.Join(gitDirectory, "objects"), filepath.Join(gitDirectory, "info"),
		filepath.Join(gitDirectory, "refs"), filepath.Join(gitDirectory, "refs", "heads"),
	} {
		if err := os.Mkdir(directory, 0o700); err != nil {
			return errors.New("candidate inspection workspace is unavailable")
		}
	}
	version, extension := "0", ""
	if len(head) == 64 {
		version, extension = "1", "\n[extensions]\n\tobjectFormat = sha256"
	}
	config := fmt.Sprintf("[core]\n\trepositoryformatversion = %s\n\tbare = false%s\n", version, extension)
	if err := os.WriteFile(filepath.Join(gitDirectory, "config"), []byte(config), 0o600); err != nil {
		return errors.New("candidate inspection configuration is unavailable")
	}
	if err := os.WriteFile(filepath.Join(gitDirectory, "HEAD"), []byte(head+"\n"), 0o600); err != nil {
		return errors.New("candidate inspection head is unavailable")
	}
	source, err := registry.integrationMaterializationWorkspace(ctx, worktreePath)
	if err != nil {
		return errors.New("candidate index identity is unavailable")
	}
	if expectedGitDirectory != "" {
		canonical, err := filepath.EvalSymlinks(source.gitDir)
		if err != nil || canonical != expectedGitDirectory {
			return errors.New("candidate submodule Git identity changed")
		}
	}
	index, err := stableCandidateControlFile(source.gitIndex, maximumCandidateIndexBytes)
	if err != nil || os.WriteFile(filepath.Join(gitDirectory, "index"), index, 0o600) != nil {
		return errors.New("candidate index copy is unavailable")
	}
	excludePath := filepath.Join(commonDirectory, "info", "exclude")
	exclude, err := optionalStableCandidateControlFile(excludePath, maximumCandidateIndexBytes)
	if err != nil || os.WriteFile(filepath.Join(gitDirectory, "info", "exclude"), exclude, 0o600) != nil {
		return errors.New("candidate exclude copy is unavailable")
	}
	inspectErr := inspect(gitWorkspaceEnvironment{
		gitDir: gitDirectory, gitWorkTree: worktreePath, gitIndex: filepath.Join(gitDirectory, "index"),
		gitObjectDirectory:          filepath.Join(gitDirectory, "objects"),
		gitAlternateObjectDirectory: filepath.Join(commonDirectory, "objects"),
	})
	if expectedGitDirectory != "" {
		final, err := registry.integrationMaterializationWorkspace(ctx, worktreePath)
		canonical, canonicalErr := filepath.EvalSymlinks(final.gitDir)
		if err != nil || canonicalErr != nil || canonical != expectedGitDirectory {
			return errors.Join(inspectErr, errors.New("candidate submodule Git identity changed"))
		}
	}
	return inspectErr
}

func stableCandidateControlFile(path string, limit int64) ([]byte, error) {
	first, err := readStableCandidateControlFile(path, limit)
	if err != nil {
		return nil, err
	}
	second, err := readStableCandidateControlFile(path, limit)
	if err != nil || !bytes.Equal(first, second) {
		return nil, errors.New("candidate control file changed while copied")
	}
	return first, nil
}

func readStableCandidateControlFile(path string, limit int64) ([]byte, error) {
	file, err := openRegularFile(path)
	if err != nil {
		return nil, err
	}
	initial, err := file.Stat()
	if err != nil || initial.Size() < 0 || initial.Size() > limit {
		_ = file.Close()
		return nil, errors.New("candidate control file exceeds its bound")
	}
	contents, readErr := io.ReadAll(io.LimitReader(file, limit+1))
	final, statErr := file.Stat()
	pathFinal, pathErr := os.Lstat(path)
	closeErr := file.Close()
	if readErr != nil || statErr != nil || pathErr != nil || closeErr != nil || int64(len(contents)) != initial.Size() ||
		!os.SameFile(initial, final) || !os.SameFile(final, pathFinal) || final.Size() != initial.Size() ||
		final.ModTime() != initial.ModTime() {
		return nil, errors.New("candidate control file changed while read")
	}
	return contents, nil
}

func optionalStableCandidateControlFile(path string, limit int64) ([]byte, error) {
	found, err := regularFileExists(path)
	if err != nil || !found {
		return nil, err
	}
	return stableCandidateControlFile(path, limit)
}

func removeCandidateInspectionWorkspace(parent, root string, identity os.FileInfo) error {
	if !filepath.IsAbs(parent) || !filepath.IsAbs(root) || filepath.Dir(root) != parent ||
		!strings.HasPrefix(filepath.Base(root), ".candidate-inspection-") {
		return errors.New("candidate inspection workspace is invalid")
	}
	info, err := os.Lstat(root)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || !os.SameFile(identity, info) {
		return errors.New("candidate inspection workspace is invalid")
	}
	if err := os.RemoveAll(root); err != nil {
		return errors.New("candidate inspection workspace could not be removed")
	}
	return nil
}
