package git

import (
	"bytes"
	"context"
	"errors"
	"strconv"
)

func (registry *Registry) loadCandidateRevisionDetails(
	ctx context.Context,
	worktreePath string,
	snapshot candidateDiffSnapshot,
) error {
	names := make([]string, 0, len(snapshot))
	retained := int64(0)
	for _, name := range sortedCandidateDiffNames(snapshot) {
		entry := snapshot[name]
		if entry.mode == "160000" || entry.size > maximumCandidateDiffDetailBytes ||
			entry.size > int64(maximumCandidateDiffSnapshotBytes)-retained {
			continue
		}
		retained += entry.size
		names = append(names, name)
	}
	if len(names) == 0 {
		return nil
	}
	input := make([]byte, 0, len(names)*65)
	for _, name := range names {
		input = append(input, snapshot[name].objectID...)
		input = append(input, '\n')
	}
	limit := maximumCandidateDiffSnapshotBytes + len(names)*128
	output, err := runGitBytesWithInputAndLimit(
		ctx, input, limit, registry.gitExecutable,
		"--no-optional-locks", "-C", worktreePath, "cat-file", "--batch",
	)
	if err != nil {
		return errors.New("inspect task diff: bounded blob detail is unavailable")
	}
	remaining := output
	for _, name := range names {
		entry := snapshot[name]
		header, rest, found := bytes.Cut(remaining, []byte{'\n'})
		fields := bytes.Fields(header)
		if !found || len(fields) != 3 || string(fields[0]) != entry.objectID || string(fields[1]) != "blob" {
			return errors.New("inspect task diff: bounded blob identity differs")
		}
		size, sizeErr := strconv.ParseInt(string(fields[2]), 10, 64)
		if sizeErr != nil || size != entry.size || size > int64(len(rest)-1) || rest[size] != '\n' {
			return errors.New("inspect task diff: bounded blob detail is malformed")
		}
		entry.contents, entry.detailKnown = rest[:size], true
		snapshot[name] = entry
		remaining = rest[size+1:]
	}
	if len(remaining) != 0 {
		return errors.New("inspect task diff: bounded blob response is ambiguous")
	}
	return nil
}
