package git

import (
	"bytes"
	"context"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"hash"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
)

const (
	maximumCandidateDiffSnapshotBytes = 64 << 20
	maximumCandidateDiffDetailBytes   = 16 << 20
	maximumCandidateDiffLines         = 65536
	maximumCandidateLineDiffWork      = 4 << 20
	maximumCandidateReportedBlobBytes = 1 << 40
)

type candidateDiffEntry struct {
	mode        string
	objectID    string
	size        int64
	contents    []byte
	detailKnown bool
}

type candidateDiffSnapshot map[string]candidateDiffEntry

func (registry *Registry) candidateDiffSnapshots(
	ctx context.Context,
	worktreePath string,
	from string,
	to string,
) (candidateDiffSnapshot, candidateDiffSnapshot, error) {
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
	index, err := registry.candidateRevisionSnapshot(ctx, worktreePath, indexTree)
	if err != nil {
		return nil, nil, err
	}
	after, err := registry.candidateWorktreeSnapshot(ctx, worktreePath, index)
	return before, after, err
}

func (registry *Registry) candidateRevisionSnapshot(
	ctx context.Context,
	worktreePath string,
	revision string,
) (candidateDiffSnapshot, error) {
	snapshot, err := registry.streamCandidateDiffTree(ctx, worktreePath, revision)
	if err != nil {
		return nil, err
	}
	if err := registry.loadCandidateRevisionDetails(ctx, worktreePath, snapshot); err != nil {
		return nil, err
	}
	return snapshot, nil
}

func parseCandidateDiffTree(listing []byte) (candidateDiffSnapshot, error) {
	if len(listing) != 0 && listing[len(listing)-1] != 0 {
		return nil, errors.New("inspect task diff: tree metadata has trailing data")
	}
	snapshot := make(candidateDiffSnapshot)
	previous := ""
	for _, record := range bytes.Split(listing, []byte{0}) {
		if len(record) == 0 {
			continue
		}
		name, entry, err := parseCandidateDiffTreeRecord(record)
		if err != nil || len(snapshot) == maximumIntegrationTreeEntries || previous != "" && name <= previous {
			return nil, errors.New("inspect task diff: tree metadata is duplicated, unordered, or malformed")
		}
		previous = name
		snapshot[name] = entry
	}
	return snapshot, nil
}

func (registry *Registry) candidateWorktreeSnapshot(
	ctx context.Context,
	worktreePath string,
	index candidateDiffSnapshot,
) (snapshot candidateDiffSnapshot, returnErr error) {
	root, err := os.OpenRoot(worktreePath)
	if err != nil {
		return nil, errors.New("inspect task diff: worktree root is unavailable")
	}
	defer func() { returnErr = errors.Join(returnErr, root.Close()) }()
	snapshot = make(candidateDiffSnapshot, len(index))
	retained := 0
	for _, name := range sortedCandidateDiffNames(index) {
		entry, found, err := registry.candidateWorktreeDiffEntry(ctx, root, worktreePath, name, index[name], &retained)
		if err != nil {
			return nil, err
		}
		if found {
			snapshot[name] = entry
		}
	}
	return snapshot, nil
}

func (registry *Registry) candidateWorktreeDiffEntry(
	ctx context.Context,
	root *os.Root,
	worktreePath string,
	name string,
	index candidateDiffEntry,
	retained *int,
) (candidateDiffEntry, bool, error) {
	info, err := root.Lstat(name)
	if errors.Is(err, os.ErrNotExist) {
		return candidateDiffEntry{}, false, nil
	}
	if err != nil {
		return candidateDiffEntry{}, false, errors.New("inspect task diff: worktree entry is unavailable")
	}
	if index.mode == "160000" {
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return candidateDiffEntry{mode: "unsupported"}, true, nil
		}
		head, err := runGit(ctx, registry.gitExecutable, "--no-optional-locks", "-C",
			filepath.Join(worktreePath, filepath.FromSlash(name)), "rev-parse", "--verify", "HEAD^{commit}")
		if err != nil || !gitRevisionPattern.MatchString(head) {
			return candidateDiffEntry{mode: "160000", objectID: "unavailable"}, true, nil
		}
		return candidateDiffEntry{mode: "160000", objectID: head}, true, nil
	}
	mode, contents, detail, err := candidateReadWorktreeDiffEntry(root, name, info, retained)
	if err != nil {
		return candidateDiffEntry{}, false, err
	}
	objectID := ""
	if detail {
		objectID = candidateBlobObjectID(index.objectID, int64(len(contents)), bytes.NewReader(contents))
	} else {
		final, finalErr := root.Lstat(name)
		if finalErr != nil || !os.SameFile(info, final) || final.Size() != info.Size() {
			return candidateDiffEntry{}, false, errors.New("inspect task diff: worktree entry identity changed")
		}
		objectID = "worktree-detail-unavailable-" + strconv.FormatInt(info.Size(), 10)
	}
	return candidateDiffEntry{
		mode: mode, objectID: objectID, size: info.Size(), contents: contents, detailKnown: detail,
	}, true, nil
}

func candidateReadWorktreeDiffEntry(
	root *os.Root,
	name string,
	info os.FileInfo,
	retained *int,
) (string, []byte, bool, error) {
	if info.Mode()&os.ModeSymlink != 0 {
		target, err := root.Readlink(name)
		if err != nil {
			return "", nil, false, errors.New("inspect task diff: symlink entry is unavailable")
		}
		return "120000", []byte(target), true, nil
	}
	if !info.Mode().IsRegular() || info.Size() < 0 || info.Size() > maximumCandidateReportedBlobBytes {
		return "", nil, false, errors.New("inspect task diff: worktree entry type is unsupported")
	}
	mode := "100644"
	if info.Mode().Perm()&0o111 != 0 {
		mode = "100755"
	}
	if info.Size() > maximumCandidateDiffDetailBytes || info.Size() > int64(maximumCandidateDiffSnapshotBytes-*retained) {
		return mode, nil, false, nil
	}
	file, err := openRootRegularFile(root, name)
	if err != nil {
		return "", nil, false, errors.New("inspect task diff: worktree entry identity changed")
	}
	contents, readErr := io.ReadAll(io.LimitReader(file, info.Size()+1))
	final, statErr := file.Stat()
	pathFinal, pathErr := root.Lstat(name)
	closeErr := file.Close()
	if readErr != nil || statErr != nil || pathErr != nil || closeErr != nil || int64(len(contents)) != info.Size() ||
		!os.SameFile(info, final) || !os.SameFile(final, pathFinal) || final.Size() != info.Size() {
		return "", nil, false, errors.New("inspect task diff: worktree entry identity changed")
	}
	*retained += len(contents)
	return mode, contents, true, nil
}

func candidateSnapshotChanges(before, after candidateDiffSnapshot) ([]CandidateFileChange, bool) {
	deleted := make(candidateDiffSnapshot)
	added := make(candidateDiffSnapshot)
	changes := make([]CandidateFileChange, 0)
	truncated := false
	for name, previous := range before {
		result, exists := after[name]
		if !exists {
			deleted[name] = previous
			continue
		}
		if !candidateDiffEntriesEqual(previous, result) {
			change, detailTruncated := candidateDiffEntryChange(name, "", previous, result)
			changes = append(changes, change)
			truncated = truncated || detailTruncated
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
	sort.Slice(changes, func(left, right int) bool { return changes[left].Path < changes[right].Path })
	return changes, truncated
}

func candidateDiffEntriesEqual(left, right candidateDiffEntry) bool {
	return left.mode == right.mode && left.objectID != "" && left.objectID == right.objectID
}

func candidateRenamesAndUnpaired(
	deleted candidateDiffSnapshot,
	added candidateDiffSnapshot,
) ([]CandidateFileChange, bool) {
	changes := make([]CandidateFileChange, 0, len(deleted)+len(added))
	truncated := false
	deletedNames := sortedCandidateDiffNames(deleted)
	addedNames := sortedCandidateDiffNames(added)
	type renameIdentity struct{ mode, objectID string }
	buckets := make(map[renameIdentity][]string, len(deletedNames))
	for _, name := range deletedNames {
		entry := deleted[name]
		if entry.detailKnown || entry.mode == "160000" {
			identity := renameIdentity{entry.mode, entry.objectID}
			buckets[identity] = append(buckets[identity], name)
		}
	}
	for _, current := range addedNames {
		result := added[current]
		if !result.detailKnown && result.mode != "160000" {
			continue
		}
		identity := renameIdentity{result.mode, result.objectID}
		candidates := buckets[identity]
		if len(candidates) != 0 {
			previous := candidates[0]
			buckets[identity] = candidates[1:]
			changes = append(changes, CandidateFileChange{Path: current, PreviousPath: previous})
			delete(deleted, previous)
			delete(added, current)
		}
	}
	for _, name := range deletedNames {
		if entry, exists := deleted[name]; exists {
			change, detailTruncated := candidateDiffEntryChange(name, "", entry, candidateDiffEntry{detailKnown: true})
			changes = append(changes, change)
			truncated = truncated || detailTruncated
		}
	}
	for _, name := range addedNames {
		if entry, exists := added[name]; exists {
			change, detailTruncated := candidateDiffEntryChange(name, "", candidateDiffEntry{detailKnown: true}, entry)
			changes = append(changes, change)
			truncated = truncated || detailTruncated
		}
	}
	return changes, truncated
}

func sortedCandidateDiffNames(snapshot candidateDiffSnapshot) []string {
	names := make([]string, 0, len(snapshot))
	for name := range snapshot {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func candidateDiffEntryChange(path, previous string, before, after candidateDiffEntry) (CandidateFileChange, bool) {
	if !before.detailKnown || !after.detailKnown || before.mode == "160000" || after.mode == "160000" {
		return CandidateFileChange{Path: path, PreviousPath: previous, DetailTruncated: true}, true
	}
	return candidateContentChangeExtent(path, previous, before.contents, after.contents)
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
		change.DetailTruncated = true
		return change, true
	}
	added, deleted, exact := candidateLineExtent(beforeLines, afterLines)
	if !exact {
		change.DetailTruncated = true
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
	lines := bytes.SplitAfter(contents, []byte{'\n'})
	if len(lines[len(lines)-1]) == 0 {
		lines = lines[:len(lines)-1]
	}
	return lines, len(lines) <= maximumCandidateDiffLines
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
			for beforeIndex < len(before) && afterIndex < len(after) && bytes.Equal(before[beforeIndex], after[afterIndex]) {
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
