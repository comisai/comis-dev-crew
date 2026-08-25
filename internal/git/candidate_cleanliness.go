package git

import (
	"bytes"
	"context"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"io"
	"os"
	"path/filepath"
	"strconv"
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
	returnErr = registry.withCandidateInspectionWorkspace(
		ctx, worktreePath, commonDirectory, head,
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
			matches, err := registry.candidateTrackedWorktreeMatches(ctx, worktreePath, index)
			if err != nil || !matches {
				clean = false
				return err
			}
			untracked, err := runGitBytesInWorkspaceWithLimit(
				ctx, registry.gitExecutable, workspace, maximumIntegrationTreeListing,
				"-c", "core.excludesFile=/dev/null", "ls-files", "--others", "--exclude-standard", "-z",
			)
			if err != nil {
				return errors.New("candidate untracked files are unavailable")
			}
			clean = len(untracked) == 0
			return nil
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
		"check-attr", "-z", "--stdin", "filter", "working-tree-encoding", "ident", "text", "eol",
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

func (registry *Registry) candidateTrackedWorktreeMatches(
	ctx context.Context,
	worktreePath string,
	entries map[string]candidateTrackedEntry,
) (matches bool, returnErr error) {
	root, err := os.OpenRoot(worktreePath)
	if err != nil {
		return false, errors.New("candidate worktree root is unavailable")
	}
	defer func() { returnErr = errors.Join(returnErr, root.Close()) }()
	for name, entry := range entries {
		if err := ctx.Err(); err != nil {
			return false, err
		}
		if entry.mode == "160000" {
			info, err := root.Lstat(name)
			if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
				return false, nil
			}
			head, err := runGit(ctx, registry.gitExecutable, "--no-optional-locks", "-C",
				filepath.Join(worktreePath, filepath.FromSlash(name)), "rev-parse", "--verify", "HEAD^{commit}")
			if err != nil || head != entry.objectID {
				return false, nil
			}
			final, err := root.Lstat(name)
			if err != nil || !final.IsDir() || final.Mode()&os.ModeSymlink != 0 || !os.SameFile(info, final) {
				return false, nil
			}
			continue
		}
		matched, err := candidateBlobMatches(ctx, root, name, entry)
		if err != nil || !matched {
			return false, err
		}
	}
	return true, nil
}

func candidateBlobMatches(
	ctx context.Context,
	root *os.Root,
	name string,
	entry candidateTrackedEntry,
) (bool, error) {
	info, err := root.Lstat(name)
	if err != nil {
		return false, nil
	}
	if entry.mode == "120000" {
		if info.Mode()&os.ModeSymlink == 0 {
			return false, nil
		}
		target, err := root.Readlink(name)
		if err != nil {
			return false, err
		}
		final, err := root.Lstat(name)
		if err != nil || final.Mode()&os.ModeSymlink == 0 || !os.SameFile(info, final) {
			return false, nil
		}
		return candidateBlobObjectID(entry.objectID, int64(len(target)), strings.NewReader(target)) == entry.objectID, nil
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 ||
		(entry.mode == "100755") != (info.Mode().Perm()&0o111 != 0) {
		return false, nil
	}
	file, err := root.Open(name)
	if err != nil {
		return false, err
	}
	opened, statErr := file.Stat()
	if statErr != nil || !os.SameFile(info, opened) {
		_ = file.Close()
		return false, nil
	}
	if opened.Size() < 0 || opened.Size() == 1<<63-1 {
		_ = file.Close()
		return false, errors.New("candidate worktree entry size is invalid")
	}
	digest := candidateBlobObjectID(
		entry.objectID, opened.Size(),
		io.LimitReader(candidateContextReader{ctx: ctx, reader: file}, opened.Size()+1),
	)
	final, finalErr := file.Stat()
	pathFinal, pathErr := root.Lstat(name)
	closeErr := file.Close()
	if finalErr != nil || pathErr != nil || closeErr != nil || !os.SameFile(opened, final) ||
		!os.SameFile(final, pathFinal) || final.Size() != opened.Size() || final.ModTime() != opened.ModTime() {
		return false, nil
	}
	if err := ctx.Err(); err != nil {
		return false, err
	}
	return digest == entry.objectID, nil
}

type candidateContextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (reader candidateContextReader) Read(destination []byte) (int, error) {
	if err := reader.ctx.Err(); err != nil {
		return 0, err
	}
	return reader.reader.Read(destination)
}

func candidateBlobObjectID(expected string, size int64, reader io.Reader) string {
	var digest hash.Hash
	switch len(expected) {
	case 40:
		digest = sha1.New()
	case 64:
		digest = sha256.New()
	default:
		return ""
	}
	_, _ = io.WriteString(digest, "blob "+strconv.FormatInt(size, 10)+"\x00")
	written, err := io.Copy(digest, reader)
	if err != nil || written != size {
		return ""
	}
	return hex.EncodeToString(digest.Sum(nil))
}

func (registry *Registry) withCandidateInspectionWorkspace(
	ctx context.Context,
	worktreePath string,
	commonDirectory string,
	head string,
	inspect func(gitWorkspaceEnvironment) error,
) (returnErr error) {
	parent := filepath.Dir(worktreePath)
	root, err := os.MkdirTemp(parent, ".candidate-inspection-")
	if err != nil {
		return errors.New("candidate inspection workspace is unavailable")
	}
	identity, err := os.Lstat(root)
	if err != nil || !identity.IsDir() || identity.Mode()&os.ModeSymlink != 0 {
		return errors.New("candidate inspection workspace is invalid")
	}
	defer func() {
		returnErr = errors.Join(returnErr, removeCandidateInspectionWorkspace(parent, root, identity))
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
	index, err := stableCandidateControlFile(source.gitIndex, maximumCandidateIndexBytes)
	if err != nil || os.WriteFile(filepath.Join(gitDirectory, "index"), index, 0o600) != nil {
		return errors.New("candidate index copy is unavailable")
	}
	excludePath := filepath.Join(commonDirectory, "info", "exclude")
	exclude, err := optionalStableCandidateControlFile(excludePath, maximumCandidateIndexBytes)
	if err != nil || os.WriteFile(filepath.Join(gitDirectory, "info", "exclude"), exclude, 0o600) != nil {
		return errors.New("candidate exclude copy is unavailable")
	}
	return inspect(gitWorkspaceEnvironment{
		gitDir: gitDirectory, gitWorkTree: worktreePath, gitIndex: filepath.Join(gitDirectory, "index"),
		gitObjectDirectory:          filepath.Join(gitDirectory, "objects"),
		gitAlternateObjectDirectory: filepath.Join(commonDirectory, "objects"),
	})
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
