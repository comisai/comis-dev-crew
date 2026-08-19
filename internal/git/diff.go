package git

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"unicode/utf8"
)

// maximumDiffFiles bounds one diff summary. Reaching it is reported rather than
// silently trimmed, so a listing never reads as complete when it is not.
const maximumDiffFiles = 256

// InspectCandidateDiff summarizes what one task changed inside its own verified
// worktree, separating committed work from work still in the tree.
//
// The two halves mean different things: committed work is what the worker stands
// behind, while uncommitted work is what a handback would land in a developer's
// editor. Only counts and paths are returned — a patch body is unbounded worker
// content, and this read is bounded by design.
func (registry *Registry) InspectCandidateDiff(
	ctx context.Context,
	request CandidateDiffRequest,
) (CandidateDiff, error) {
	if registry == nil || ctx == nil {
		return CandidateDiff{}, errors.New("inspect task diff: registry and context are required")
	}
	if err := ctx.Err(); err != nil {
		return CandidateDiff{}, err
	}
	if !repositoryIDPattern.MatchString(request.TaskHandle) || !repositoryIDPattern.MatchString(request.RepositoryID) {
		return CandidateDiff{}, errors.New("inspect task diff: request identity is invalid")
	}
	if !gitRevisionPattern.MatchString(request.BaseRevision) {
		return CandidateDiff{}, errors.New("inspect task diff: base revision is invalid")
	}
	repository, err := registry.Resolve(request.RepositoryID)
	if err != nil {
		return CandidateDiff{}, errors.New("inspect task diff: repository is unavailable")
	}
	if request.WorktreePath != filepath.Join(repository.WorktreeRoot, request.TaskHandle) {
		return CandidateDiff{}, errors.New("inspect task diff: worktree does not match task root")
	}
	if _, err := registry.ValidateWorktree(ctx, request.RepositoryID, request.WorktreePath); err != nil {
		if ctx.Err() != nil {
			return CandidateDiff{}, ctx.Err()
		}
		return CandidateDiff{}, fmt.Errorf("inspect task diff: worktree inspection failed: %w", err)
	}
	head, err := runGit(ctx, registry.gitExecutable, "--no-optional-locks", "-C", request.WorktreePath,
		"rev-parse", "--verify", "HEAD^{commit}")
	if err != nil {
		if ctx.Err() != nil {
			return CandidateDiff{}, ctx.Err()
		}
		return CandidateDiff{}, errors.New("inspect task diff: head is unavailable")
	}
	// The base must be an object this repository actually knows; otherwise the
	// comparison would silently describe a different history.
	if _, err := runGit(ctx, registry.gitExecutable, "--no-optional-locks", "-C", request.WorktreePath,
		"rev-parse", "--verify", request.BaseRevision+"^{commit}"); err != nil {
		if ctx.Err() != nil {
			return CandidateDiff{}, ctx.Err()
		}
		return CandidateDiff{}, errors.New("inspect task diff: base revision is unknown to the repository")
	}
	diff := CandidateDiff{
		RepositoryID: request.RepositoryID, WorktreePath: request.WorktreePath,
		BaseRevision: request.BaseRevision, HeadRevision: head,
	}
	committed, committedTruncated, err := registry.diffFiles(ctx, request.WorktreePath, request.BaseRevision, head)
	if err != nil {
		return CandidateDiff{}, err
	}
	uncommitted, uncommittedTruncated, err := registry.diffFiles(ctx, request.WorktreePath, head, "")
	if err != nil {
		return CandidateDiff{}, err
	}
	diff.Committed, diff.Uncommitted = committed, uncommitted
	diff.FileListTruncated = committedTruncated || uncommittedTruncated
	diff.CommittedTotals = summarizeDiff(committed)
	diff.UncommittedTotals = summarizeDiff(uncommitted)
	return diff, nil
}

// diffFiles reads one numeric change summary. An empty target compares the index
// and working tree against the given revision.
//
// An oversize summary reports truncation instead of failing: an operator asking
// what changed is better served by "too many files to list" than by an error
// that says nothing about the size of the change.
func (registry *Registry) diffFiles(
	ctx context.Context,
	worktreePath string,
	from string,
	to string,
) ([]CandidateFileChange, bool, error) {
	arguments := []string{
		"--no-optional-locks", "-C", worktreePath, "diff", "--numstat", "-z",
		"--find-renames", "--no-color", "--no-ext-diff", from,
	}
	if to != "" {
		arguments = append(arguments, to)
	}
	output, err := runGitBytes(ctx, registry.gitExecutable, arguments...)
	if err != nil {
		if ctx.Err() != nil {
			return nil, false, ctx.Err()
		}
		if errors.Is(err, errGitOutputTooLarge) {
			return nil, true, nil
		}
		return nil, false, errors.New("inspect task diff: change summary is unavailable")
	}
	changes, err := parseNumstat(output)
	if err != nil {
		return nil, false, err
	}
	if len(changes) > maximumDiffFiles {
		return changes[:maximumDiffFiles], true, nil
	}
	return changes, false, nil
}

// parseNumstat decodes NUL-separated numeric change records.
//
// Paths arrive verbatim, so each is checked for control characters and valid
// encoding and the whole read is refused when one is unsafe. Refusing rather than
// escaping keeps a worker-chosen filename from reaching a terminal as a control
// sequence, and an anomalous name is worth stopping on rather than displaying.
func parseNumstat(output []byte) ([]CandidateFileChange, error) {
	fields := splitNumstatRecords(output)
	var changes []CandidateFileChange
	for index := 0; index < len(fields); index++ {
		record := fields[index]
		added, deleted, path, err := splitNumstatRecord(record)
		if err != nil {
			return nil, err
		}
		change := CandidateFileChange{Added: added, Deleted: deleted, Binary: added < 0}
		if change.Binary {
			change.Added, change.Deleted = 0, 0
		}
		// A rename leaves the path field empty and emits the previous and current
		// paths as the two records that follow.
		if path == "" {
			if index+2 >= len(fields) {
				return nil, errors.New("inspect task diff: rename record is incomplete")
			}
			previous, current := fields[index+1], fields[index+2]
			index += 2
			if err := validateDiffPath(previous); err != nil {
				return nil, err
			}
			if err := validateDiffPath(current); err != nil {
				return nil, err
			}
			change.PreviousPath, change.Path = previous, current
		} else {
			if err := validateDiffPath(path); err != nil {
				return nil, err
			}
			change.Path = path
		}
		changes = append(changes, change)
	}
	return changes, nil
}

func splitNumstatRecords(output []byte) []string {
	trimmed := bytes.TrimSuffix(output, []byte{0})
	if len(trimmed) == 0 {
		return nil
	}
	parts := bytes.Split(trimmed, []byte{0})
	records := make([]string, 0, len(parts))
	for _, part := range parts {
		records = append(records, string(part))
	}
	return records
}

// splitNumstatRecord decodes one "added<TAB>deleted<TAB>path" record. A binary
// change reports "-" for both counts, which becomes a negative added count so
// the caller can mark it rather than record it as an empty change.
func splitNumstatRecord(record string) (int, int, string, error) {
	first := strings.IndexByte(record, '\t')
	if first < 0 {
		return 0, 0, "", errors.New("inspect task diff: change record is malformed")
	}
	second := strings.IndexByte(record[first+1:], '\t')
	if second < 0 {
		return 0, 0, "", errors.New("inspect task diff: change record is malformed")
	}
	addedField := record[:first]
	deletedField := record[first+1 : first+1+second]
	path := record[first+second+2:]
	if addedField == "-" && deletedField == "-" {
		return -1, -1, path, nil
	}
	added, err := strconv.Atoi(addedField)
	if err != nil || added < 0 {
		return 0, 0, "", errors.New("inspect task diff: change record count is invalid")
	}
	deleted, err := strconv.Atoi(deletedField)
	if err != nil || deleted < 0 {
		return 0, 0, "", errors.New("inspect task diff: change record count is invalid")
	}
	return added, deleted, path, nil
}

func validateDiffPath(path string) error {
	if path == "" || len(path) > 1024 || !utf8.ValidString(path) {
		return errors.New("inspect task diff: change path is unsafe")
	}
	for _, character := range path {
		if character < 0x20 || character == 0x7f {
			return errors.New("inspect task diff: change path is unsafe")
		}
	}
	return nil
}

func summarizeDiff(changes []CandidateFileChange) CandidateDiffTotals {
	totals := CandidateDiffTotals{Files: len(changes)}
	for _, change := range changes {
		if change.Binary {
			totals.BinaryFiles++
			continue
		}
		totals.Added += change.Added
		totals.Deleted += change.Deleted
	}
	return totals
}
