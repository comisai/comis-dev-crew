package git

import (
	"bytes"
	"context"
	"errors"
	"io"
	"io/fs"
	"os"
	"path"
	"strconv"
	"strings"
)

const (
	maximumIntegrationTreeEntries = 65536
	maximumIntegrationTreeListing = 16 << 20
	maximumIntegrationBlobBytes   = 16 << 20
	maximumIntegrationTreeBytes   = 64 << 20
)

type integrationTreeEntry struct {
	mode     string
	objectID string
	contents []byte
}

type integrationTreeSnapshot map[string]integrationTreeEntry

func (registry *Registry) loadIntegrationTreeSnapshot(
	ctx context.Context,
	worktreePath string,
	tree string,
) (integrationTreeSnapshot, error) {
	listing, err := runGitBytesWithLimit(ctx, maximumIntegrationTreeListing, registry.gitExecutable,
		"--no-optional-locks", "-C", worktreePath, "ls-tree", "-r", "-z", "--full-tree", tree)
	if err != nil {
		return nil, errors.New("apply integration candidate: materialization tree listing is unavailable")
	}
	snapshot := make(integrationTreeSnapshot)
	objectOrder := make([]string, 0)
	seenObjects := make(map[string]struct{})
	for _, encoded := range bytes.Split(listing, []byte{0}) {
		if len(encoded) == 0 {
			continue
		}
		metadata, name, found := bytes.Cut(encoded, []byte{'\t'})
		fields := bytes.Fields(metadata)
		if !found || len(fields) != 3 || string(fields[1]) != "blob" || len(snapshot) == maximumIntegrationTreeEntries {
			return nil, errors.New("apply integration candidate: materialization tree entry is invalid")
		}
		mode, objectID, entryPath := string(fields[0]), string(fields[2]), string(name)
		if mode != "100644" && mode != "100755" && mode != "120000" ||
			!gitRevisionPattern.MatchString(objectID) || !validIntegrationTreePath(entryPath) {
			return nil, errors.New("apply integration candidate: materialization tree entry is unsafe")
		}
		if _, exists := snapshot[entryPath]; exists {
			return nil, errors.New("apply integration candidate: materialization tree entry is duplicated")
		}
		snapshot[entryPath] = integrationTreeEntry{mode: mode, objectID: objectID}
		if _, exists := seenObjects[objectID]; !exists {
			seenObjects[objectID] = struct{}{}
			objectOrder = append(objectOrder, objectID)
		}
	}
	contents, err := registry.loadIntegrationBlobContents(ctx, worktreePath, objectOrder)
	if err != nil {
		return nil, err
	}
	for entryPath, entry := range snapshot {
		entry.contents = contents[entry.objectID]
		snapshot[entryPath] = entry
	}
	total := 0
	for _, entry := range snapshot {
		total += len(entry.contents)
		if total > maximumIntegrationTreeBytes {
			return nil, errors.New("apply integration candidate: materialization tree exceeds its bound")
		}
	}
	return snapshot, nil
}

func validIntegrationTreePath(name string) bool {
	if name == "" || len([]byte(name)) > 1024 || strings.ContainsAny(name, "\\\x00") ||
		path.IsAbs(name) || path.Clean(name) != name || name == ".git" || strings.HasPrefix(name, ".git/") {
		return false
	}
	for _, component := range strings.Split(name, "/") {
		if component == "" || component == "." || component == ".." {
			return false
		}
	}
	return true
}

func (registry *Registry) loadIntegrationBlobContents(
	ctx context.Context,
	worktreePath string,
	objectIDs []string,
) (map[string][]byte, error) {
	contents := make(map[string][]byte, len(objectIDs))
	if len(objectIDs) == 0 {
		return contents, nil
	}
	input := []byte(strings.Join(objectIDs, "\n") + "\n")
	limit := maximumIntegrationTreeBytes + len(objectIDs)*128
	output, err := runGitBytesWithInputAndLimit(ctx, input, limit, registry.gitExecutable,
		"--no-optional-locks", "-C", worktreePath, "cat-file", "--batch")
	if err != nil {
		return nil, errors.New("apply integration candidate: materialization blobs are unavailable")
	}
	remaining := output
	total := 0
	for _, expectedID := range objectIDs {
		line, rest, found := bytes.Cut(remaining, []byte{'\n'})
		fields := bytes.Fields(line)
		if !found || len(fields) != 3 || string(fields[0]) != expectedID || string(fields[1]) != "blob" {
			return nil, errors.New("apply integration candidate: materialization blob identity differs")
		}
		size, sizeErr := strconv.Atoi(string(fields[2]))
		if sizeErr != nil || size < 0 || size > maximumIntegrationBlobBytes || size > len(rest)-1 || rest[size] != '\n' {
			return nil, errors.New("apply integration candidate: materialization blob exceeds its bound")
		}
		total += size
		if total > maximumIntegrationTreeBytes {
			return nil, errors.New("apply integration candidate: materialization tree exceeds its bound")
		}
		contents[expectedID] = append([]byte(nil), rest[:size]...)
		remaining = rest[size+1:]
	}
	if len(remaining) != 0 {
		return nil, errors.New("apply integration candidate: materialization blob response is ambiguous")
	}
	return contents, nil
}

func integrationWorktreeMatchesSnapshot(
	worktreePath string,
	snapshot integrationTreeSnapshot,
) (matches bool, returnErr error) {
	return integrationWorktreeMatchesSnapshotTopology(worktreePath, snapshot, false)
}

func integrationWorktreeMatchesMaterializationSnapshot(
	worktreePath string,
	snapshot integrationTreeSnapshot,
) (matches bool, returnErr error) {
	return integrationWorktreeMatchesSnapshotTopology(worktreePath, snapshot, true)
}

func integrationWorktreeMatchesSnapshotTopology(
	worktreePath string,
	snapshot integrationTreeSnapshot,
	strictDirectories bool,
) (matches bool, returnErr error) {
	root, err := os.OpenRoot(worktreePath)
	if err != nil {
		return false, errors.New("apply integration candidate: materialization root is unavailable")
	}
	defer func() { returnErr = errors.Join(returnErr, root.Close()) }()
	return integrationRootMatchesSnapshotTopology(root, snapshot, strictDirectories)
}

func integrationRootMatchesSnapshot(root *os.Root, snapshot integrationTreeSnapshot) (bool, error) {
	return integrationRootMatchesSnapshotTopology(root, snapshot, false)
}

func integrationRootMatchesSnapshotTopology(
	root *os.Root,
	snapshot integrationTreeSnapshot,
	strictDirectories bool,
) (bool, error) {
	seen := make(map[string]struct{}, len(snapshot))
	var directories map[string]struct{}
	if strictDirectories {
		var err error
		directories, err = integrationSnapshotDirectories(snapshot)
		if err != nil {
			return false, err
		}
	}
	err := fs.WalkDir(root.FS(), ".", func(name string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if name == ".git" {
			if entry.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if name == "." {
			return nil
		}
		if entry.IsDir() {
			if !strictDirectories {
				return nil
			}
			if _, expected := directories[name]; !expected {
				return fs.ErrExist
			}
			info, err := root.Lstat(name)
			if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
				return fs.ErrInvalid
			}
			return nil
		}
		expected, exists := snapshot[name]
		if !exists {
			return fs.ErrExist
		}
		info, err := root.Lstat(name)
		if err != nil {
			return err
		}
		switch expected.mode {
		case "120000":
			if info.Mode()&os.ModeSymlink == 0 {
				return fs.ErrInvalid
			}
			target, err := root.Readlink(name)
			if err != nil || !bytes.Equal([]byte(target), expected.contents) {
				return fs.ErrInvalid
			}
		case "100644", "100755":
			if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 ||
				(expected.mode == "100755") != (info.Mode().Perm()&0o111 != 0) {
				return fs.ErrInvalid
			}
			matches, err := integrationRegularFileMatches(root, name, info, expected.contents)
			if err != nil || !matches {
				return fs.ErrInvalid
			}
		default:
			return fs.ErrInvalid
		}
		seen[name] = struct{}{}
		return nil
	})
	if errors.Is(err, fs.ErrExist) || errors.Is(err, fs.ErrInvalid) {
		return false, nil
	}
	if err != nil {
		return false, errors.New("apply integration candidate: materialization worktree is unavailable")
	}
	return len(seen) == len(snapshot), nil
}

func integrationSnapshotDirectories(snapshot integrationTreeSnapshot) (map[string]struct{}, error) {
	directories := make(map[string]struct{})
	for name := range snapshot {
		for directory := path.Dir(name); directory != "."; directory = path.Dir(directory) {
			if _, exists := directories[directory]; exists {
				continue
			}
			if len(directories) == maximumIntegrationTreeEntries {
				return nil, errors.New("apply integration candidate: materialization directory topology exceeds its bound")
			}
			directories[directory] = struct{}{}
		}
	}
	return directories, nil
}

func integrationRegularFileMatches(
	root *os.Root,
	name string,
	initial os.FileInfo,
	expected []byte,
) (bool, error) {
	return integrationRegularFileMatchesAtBoundary(root, name, initial, expected, nil)
}

func integrationRegularFileMatchesAtBoundary(
	root *os.Root,
	name string,
	initial os.FileInfo,
	expected []byte,
	boundary func(),
) (matches bool, returnErr error) {
	if int64(len(expected)) != initial.Size() {
		return false, nil
	}
	file, err := root.Open(name)
	if err != nil {
		return false, err
	}
	defer func() { returnErr = errors.Join(returnErr, file.Close()) }()
	opened, err := file.Stat()
	if err != nil || !opened.Mode().IsRegular() || opened.Size() != int64(len(expected)) ||
		!os.SameFile(initial, opened) {
		return false, err
	}
	if boundary != nil {
		boundary()
	}
	buffer := make([]byte, 32<<10)
	for offset := 0; offset < len(expected); {
		length := min(len(buffer), len(expected)-offset)
		read, readErr := io.ReadFull(file, buffer[:length])
		if readErr != nil || read != length || !bytes.Equal(buffer[:length], expected[offset:offset+length]) {
			return false, nil
		}
		offset += length
	}
	var overflow [1]byte
	if read, readErr := file.Read(overflow[:]); read != 0 || readErr != io.EOF {
		return false, nil
	}
	finalPath, err := root.Lstat(name)
	if err != nil {
		return false, err
	}
	finalFile, err := file.Stat()
	if err != nil || !os.SameFile(opened, finalFile) || !os.SameFile(finalPath, finalFile) ||
		finalFile.Size() != int64(len(expected)) {
		return false, err
	}
	return true, nil
}

func (registry *Registry) integrationIndexTree(ctx context.Context, worktreePath string) (string, error) {
	tree, err := runGit(ctx, registry.gitExecutable, "--no-optional-locks", "-C", worktreePath, "write-tree")
	if err != nil || !gitRevisionPattern.MatchString(tree) {
		return "", errors.New("apply integration candidate: materialization index tree is unavailable")
	}
	return tree, nil
}

func (registry *Registry) integrationWorktreeCleanAtCommit(
	ctx context.Context,
	worktreePath string,
	head string,
) (bool, error) {
	tree, err := registry.integrationCommitTree(ctx, worktreePath, head)
	if err != nil {
		return false, err
	}
	indexTree, err := registry.integrationIndexTree(ctx, worktreePath)
	if err != nil || indexTree != tree {
		return false, nil
	}
	snapshot, err := registry.loadIntegrationTreeSnapshot(ctx, worktreePath, tree)
	if err != nil {
		return false, err
	}
	return integrationWorktreeMatchesSnapshot(worktreePath, snapshot)
}
