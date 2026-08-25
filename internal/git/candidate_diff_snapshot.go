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

const (
	maximumCandidateDiffSnapshotBytes = 64 << 20
	maximumCandidateDiffLines         = 65536
	maximumCandidateLineDiffWork      = 4 << 20
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
	retainedBytes := 0
	for name := range index {
		entry, found, err := candidateWorktreeEntry(root, name)
		if err != nil {
			return nil, err
		}
		if found {
			if len(entry.contents) > maximumCandidateDiffSnapshotBytes-retainedBytes {
				return nil, errors.New("inspect task diff: worktree snapshot exceeds its aggregate bound")
			}
			retainedBytes += len(entry.contents)
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

func candidateSnapshotChanges(before, after integrationTreeSnapshot) ([]CandidateFileChange, bool) {
	deleted := make(map[string]integrationTreeEntry)
	added := make(map[string]integrationTreeEntry)
	changes := make([]CandidateFileChange, 0)
	truncated := false
	for name, previous := range before {
		result, exists := after[name]
		if !exists {
			deleted[name] = previous
			continue
		}
		if previous.mode != result.mode || !bytes.Equal(previous.contents, result.contents) {
			change, extentTruncated := candidateContentChangeExtent(name, "", previous.contents, result.contents)
			changes = append(changes, change)
			truncated = truncated || extentTruncated
		}
	}
	for name, result := range after {
		if _, exists := before[name]; !exists {
			added[name] = result
		}
	}
	unpaired, unpairedTruncated := candidateRenamesAndUnpaired(deleted, added)
	changes = append(changes, unpaired...)
	truncated = truncated || unpairedTruncated
	sort.Slice(changes, func(left, right int) bool {
		return changes[left].Path < changes[right].Path
	})
	return changes, truncated
}

func candidateRenamesAndUnpaired(
	deleted map[string]integrationTreeEntry,
	added map[string]integrationTreeEntry,
) ([]CandidateFileChange, bool) {
	changes := make([]CandidateFileChange, 0, len(deleted)+len(added))
	truncated := false
	deletedNames := sortedSnapshotNames(deleted)
	addedNames := sortedSnapshotNames(added)
	type renameIdentity struct {
		mode   string
		size   int
		digest [sha256.Size]byte
	}
	buckets := make(map[renameIdentity][]string, len(deletedNames))
	for _, name := range deletedNames {
		entry := deleted[name]
		identity := renameIdentity{mode: entry.mode, size: len(entry.contents), digest: sha256.Sum256(entry.contents)}
		buckets[identity] = append(buckets[identity], name)
	}
	for _, current := range addedNames {
		result := added[current]
		identity := renameIdentity{mode: result.mode, size: len(result.contents), digest: sha256.Sum256(result.contents)}
		candidates := buckets[identity]
		for len(candidates) > 0 {
			previous := candidates[0]
			candidates = candidates[1:]
			prior, exists := deleted[previous]
			if exists && bytes.Equal(prior.contents, result.contents) {
				changes = append(changes, CandidateFileChange{Path: current, PreviousPath: previous})
				delete(deleted, previous)
				delete(added, current)
				break
			}
		}
		buckets[identity] = candidates
	}
	for _, name := range deletedNames {
		if entry, exists := deleted[name]; exists {
			change, extentTruncated := candidateContentChangeExtent(name, "", entry.contents, nil)
			changes = append(changes, change)
			truncated = truncated || extentTruncated
		}
	}
	for _, name := range addedNames {
		if entry, exists := added[name]; exists {
			change, extentTruncated := candidateContentChangeExtent(name, "", nil, entry.contents)
			changes = append(changes, change)
			truncated = truncated || extentTruncated
		}
	}
	return changes, truncated
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
	change, _ := candidateContentChangeExtent(path, previous, before, after)
	return change
}

func candidateContentChangeExtent(path, previous string, before, after []byte) (CandidateFileChange, bool) {
	change := CandidateFileChange{Path: path, PreviousPath: previous}
	if bytes.IndexByte(before, 0) >= 0 || bytes.IndexByte(after, 0) >= 0 {
		change.Binary = true
		return change, false
	}
	beforeLines, beforeBounded := candidateLines(before)
	afterLines, afterBounded := candidateLines(after)
	if !beforeBounded || !afterBounded {
		return change, true
	}
	added, deleted, exact := candidateLineExtent(beforeLines, afterLines)
	if !exact {
		return change, true
	}
	change.Added, change.Deleted = added, deleted
	return change, false
}

func candidateLines(contents []byte) ([][]byte, bool) {
	if len(contents) == 0 {
		return nil, true
	}
	if bytes.Count(contents, []byte{'\n'}) > maximumCandidateDiffLines {
		return nil, false
	}
	lines := bytes.Split(contents, []byte{'\n'})
	if len(lines[len(lines)-1]) == 0 {
		lines = lines[:len(lines)-1]
	}
	if len(lines) > maximumCandidateDiffLines {
		return nil, false
	}
	return lines, true
}

func candidateLineExtent(before, after [][]byte) (int, int, bool) {
	maximum := len(before) + len(after)
	if maximum == 0 {
		return 0, 0, true
	}
	frontier := make([]int, 2*maximum+3)
	offset := maximum + 1
	work := 0
	for distance := 0; distance <= maximum; distance++ {
		for diagonal := -distance; diagonal <= distance; diagonal += 2 {
			work++
			if work > maximumCandidateLineDiffWork {
				return 0, 0, false
			}
			position := offset + diagonal
			var beforeIndex int
			if diagonal == -distance || diagonal != distance && frontier[position-1] < frontier[position+1] {
				beforeIndex = frontier[position+1]
			} else {
				beforeIndex = frontier[position-1] + 1
			}
			afterIndex := beforeIndex - diagonal
			for beforeIndex < len(before) && afterIndex < len(after) &&
				bytes.Equal(before[beforeIndex], after[afterIndex]) {
				beforeIndex++
				afterIndex++
				work++
				if work > maximumCandidateLineDiffWork {
					return 0, 0, false
				}
			}
			frontier[position] = beforeIndex
			if beforeIndex >= len(before) && afterIndex >= len(after) {
				common := (len(before) + len(after) - distance) / 2
				return len(after) - common, len(before) - common, true
			}
		}
	}
	return 0, 0, false
}
