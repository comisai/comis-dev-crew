package git

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"sort"
)

func (registry *Registry) candidateDiffSnapshots(
	ctx context.Context,
	worktreePath string,
	from string,
	to string,
) (integrationTreeSnapshot, integrationTreeSnapshot, error) {
	before, err := registry.candidateRevisionSnapshot(ctx, worktreePath, from)
	if err != nil {
		return nil, nil, err
	}
	if to != "" {
		after, err := registry.candidateRevisionSnapshot(ctx, worktreePath, to)
		return before, after, err
	}
	indexTree, err := registry.integrationIndexTree(ctx, worktreePath)
	if err != nil {
		return nil, nil, err
	}
	index, err := registry.loadIntegrationTreeSnapshot(ctx, worktreePath, indexTree)
	if err != nil {
		return nil, nil, err
	}
	after, err := candidateWorktreeSnapshot(worktreePath, index)
	return before, after, err
}

func (registry *Registry) candidateRevisionSnapshot(
	ctx context.Context,
	worktreePath string,
	revision string,
) (integrationTreeSnapshot, error) {
	tree, err := registry.integrationCommitTree(ctx, worktreePath, revision)
	if err != nil {
		return nil, err
	}
	return registry.loadIntegrationTreeSnapshot(ctx, worktreePath, tree)
}

func candidateWorktreeSnapshot(
	worktreePath string,
	index integrationTreeSnapshot,
) (snapshot integrationTreeSnapshot, returnErr error) {
	root, err := os.OpenRoot(worktreePath)
	if err != nil {
		return nil, errors.New("inspect task diff: worktree root is unavailable")
	}
	defer func() { returnErr = errors.Join(returnErr, root.Close()) }()
	snapshot = make(integrationTreeSnapshot, len(index))
	for name := range index {
		entry, found, err := candidateWorktreeEntry(root, name)
		if err != nil {
			return nil, err
		}
		if found {
			snapshot[name] = entry
		}
	}
	return snapshot, nil
}

func candidateWorktreeEntry(root *os.Root, name string) (integrationTreeEntry, bool, error) {
	info, err := root.Lstat(name)
	if errors.Is(err, os.ErrNotExist) {
		return integrationTreeEntry{}, false, nil
	}
	if err != nil {
		return integrationTreeEntry{}, false, errors.New("inspect task diff: worktree entry is unavailable")
	}
	var mode string
	var contents []byte
	switch {
	case info.Mode()&os.ModeSymlink != 0:
		mode = "120000"
		target, err := root.Readlink(name)
		if err != nil || len(target) > maximumIntegrationBlobBytes {
			return integrationTreeEntry{}, false, errors.New("inspect task diff: symlink entry is unavailable")
		}
		contents = []byte(target)
	case info.Mode().IsRegular():
		mode = "100644"
		if info.Mode().Perm()&0o111 != 0 {
			mode = "100755"
		}
		if info.Size() < 0 || info.Size() > maximumIntegrationBlobBytes {
			return integrationTreeEntry{}, false, errors.New("inspect task diff: worktree entry exceeds its bound")
		}
		file, err := openRootRegularFile(root, name)
		if err != nil {
			return integrationTreeEntry{}, false, errors.New("inspect task diff: worktree entry identity changed")
		}
		contents, err = io.ReadAll(io.LimitReader(file, maximumIntegrationBlobBytes+1))
		final, statErr := file.Stat()
		pathFinal, pathErr := root.Lstat(name)
		closeErr := file.Close()
		if err != nil || statErr != nil || pathErr != nil || closeErr != nil ||
			len(contents) > maximumIntegrationBlobBytes || !os.SameFile(info, final) || !os.SameFile(final, pathFinal) ||
			final.Size() != int64(len(contents)) {
			return integrationTreeEntry{}, false, errors.New("inspect task diff: worktree entry identity changed")
		}
	default:
		return integrationTreeEntry{}, false, errors.New("inspect task diff: worktree entry type is unsupported")
	}
	digest := sha256.Sum256(append([]byte(mode+"\x00"), contents...))
	return integrationTreeEntry{mode: mode, objectID: hex.EncodeToString(digest[:]), contents: contents}, true, nil
}

func candidateSnapshotChanges(before, after integrationTreeSnapshot) []CandidateFileChange {
	deleted := make(map[string]integrationTreeEntry)
	added := make(map[string]integrationTreeEntry)
	changes := make([]CandidateFileChange, 0)
	for name, previous := range before {
		result, exists := after[name]
		if !exists {
			deleted[name] = previous
			continue
		}
		if previous.mode != result.mode || !bytes.Equal(previous.contents, result.contents) {
			changes = append(changes, candidateContentChange(name, "", previous.contents, result.contents))
		}
	}
	for name, result := range after {
		if _, exists := before[name]; !exists {
			added[name] = result
		}
	}
	changes = append(changes, candidateRenamesAndUnpaired(deleted, added)...)
	sort.Slice(changes, func(left, right int) bool {
		return changes[left].Path < changes[right].Path
	})
	return changes
}

func candidateRenamesAndUnpaired(
	deleted map[string]integrationTreeEntry,
	added map[string]integrationTreeEntry,
) []CandidateFileChange {
	changes := make([]CandidateFileChange, 0, len(deleted)+len(added))
	deletedNames := sortedSnapshotNames(deleted)
	addedNames := sortedSnapshotNames(added)
	for _, current := range addedNames {
		result := added[current]
		for _, previous := range deletedNames {
			prior, exists := deleted[previous]
			if exists && prior.mode == result.mode && bytes.Equal(prior.contents, result.contents) {
				changes = append(changes, CandidateFileChange{Path: current, PreviousPath: previous})
				delete(deleted, previous)
				delete(added, current)
				break
			}
		}
	}
	for _, name := range deletedNames {
		if entry, exists := deleted[name]; exists {
			changes = append(changes, candidateContentChange(name, "", entry.contents, nil))
		}
	}
	for _, name := range addedNames {
		if entry, exists := added[name]; exists {
			changes = append(changes, candidateContentChange(name, "", nil, entry.contents))
		}
	}
	return changes
}

func sortedSnapshotNames(snapshot map[string]integrationTreeEntry) []string {
	names := make([]string, 0, len(snapshot))
	for name := range snapshot {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func candidateContentChange(path, previous string, before, after []byte) CandidateFileChange {
	change := CandidateFileChange{Path: path, PreviousPath: previous}
	if bytes.IndexByte(before, 0) >= 0 || bytes.IndexByte(after, 0) >= 0 {
		change.Binary = true
		return change
	}
	beforeLines := bytes.Split(before, []byte{'\n'})
	afterLines := bytes.Split(after, []byte{'\n'})
	if len(before) == 0 {
		beforeLines = nil
	} else if len(beforeLines[len(beforeLines)-1]) == 0 {
		beforeLines = beforeLines[:len(beforeLines)-1]
	}
	if len(after) == 0 {
		afterLines = nil
	} else if len(afterLines[len(afterLines)-1]) == 0 {
		afterLines = afterLines[:len(afterLines)-1]
	}
	prefix := 0
	for prefix < len(beforeLines) && prefix < len(afterLines) && bytes.Equal(beforeLines[prefix], afterLines[prefix]) {
		prefix++
	}
	suffix := 0
	for suffix < len(beforeLines)-prefix && suffix < len(afterLines)-prefix &&
		bytes.Equal(beforeLines[len(beforeLines)-1-suffix], afterLines[len(afterLines)-1-suffix]) {
		suffix++
	}
	change.Deleted = len(beforeLines) - prefix - suffix
	change.Added = len(afterLines) - prefix - suffix
	return change
}
