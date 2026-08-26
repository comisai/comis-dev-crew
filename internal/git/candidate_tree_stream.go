package git

import (
	"bytes"
	"context"
	"errors"
	"strconv"
)

func (registry *Registry) streamCandidateTrackedEntries(
	ctx context.Context,
	workspace gitWorkspaceEnvironment,
	index bool,
	arguments ...string,
) (map[string]candidateTrackedEntry, error) {
	entries := make(map[string]candidateTrackedEntry)
	previous := ""
	err := streamHermeticGitNULRecords(
		ctx, registry.gitExecutable, &workspace, maximumIntegrationTreeEntries, arguments,
		func(record []byte) error {
			name, entry, err := parseCandidateTrackedRecord(record, index)
			if err != nil || previous != "" && name <= previous {
				return errors.New("candidate tracked entries are unordered or malformed")
			}
			previous = name
			entries[name] = entry
			return nil
		},
	)
	if err != nil {
		return nil, err
	}
	return entries, nil
}

func parseCandidateTrackedRecord(record []byte, index bool) (string, candidateTrackedEntry, error) {
	metadata, encodedPath, found := bytes.Cut(record, []byte{'\t'})
	fields := bytes.Fields(metadata)
	if !found || len(fields) != 3 {
		return "", candidateTrackedEntry{}, errors.New("candidate tracked entry is malformed")
	}
	mode, objectID, entryPath := string(fields[0]), string(fields[1]), string(encodedPath)
	if !index {
		objectID = string(fields[2])
	}
	if index && string(fields[2]) != "0" || !candidateTrackedMode(mode) ||
		!gitRevisionPattern.MatchString(objectID) || !validIntegrationTreePath(entryPath) {
		return "", candidateTrackedEntry{}, errors.New("candidate tracked entry is unsafe")
	}
	if !index {
		objectType := string(fields[1])
		if mode == "160000" && objectType != "commit" || mode != "160000" && objectType != "blob" {
			return "", candidateTrackedEntry{}, errors.New("candidate tracked entry type differs")
		}
	}
	return entryPath, candidateTrackedEntry{mode: mode, objectID: objectID}, nil
}

func (registry *Registry) streamCandidateDiffTree(
	ctx context.Context,
	worktreePath string,
	revision string,
) (candidateDiffSnapshot, error) {
	snapshot := make(candidateDiffSnapshot)
	previous := ""
	err := streamHermeticGitNULRecords(
		ctx, registry.gitExecutable, nil, maximumIntegrationTreeEntries,
		[]string{"--no-optional-locks", "-C", worktreePath,
			"ls-tree", "-r", "-z", "--full-tree", "--long", revision},
		func(record []byte) error {
			name, entry, err := parseCandidateDiffTreeRecord(record)
			if err != nil || previous != "" && name <= previous {
				return errors.New("inspect task diff: tree metadata is unordered or malformed")
			}
			previous = name
			snapshot[name] = entry
			return nil
		},
	)
	if err != nil {
		return nil, err
	}
	return snapshot, nil
}

func parseCandidateDiffTreeRecord(record []byte) (string, candidateDiffEntry, error) {
	metadata, encodedPath, found := bytes.Cut(record, []byte{'\t'})
	fields := bytes.Fields(metadata)
	if !found || len(fields) != 4 {
		return "", candidateDiffEntry{}, errors.New("inspect task diff: tree metadata is malformed")
	}
	mode, objectType, objectID := string(fields[0]), string(fields[1]), string(fields[2])
	name := string(encodedPath)
	if !candidateTrackedMode(mode) || !gitRevisionPattern.MatchString(objectID) ||
		!validIntegrationTreePath(name) || mode == "160000" && objectType != "commit" ||
		mode != "160000" && objectType != "blob" {
		return "", candidateDiffEntry{}, errors.New("inspect task diff: tree metadata is unsafe")
	}
	size := int64(0)
	if mode == "160000" {
		if string(fields[3]) != "-" {
			return "", candidateDiffEntry{}, errors.New("inspect task diff: gitlink metadata is malformed")
		}
	} else {
		var err error
		size, err = strconv.ParseInt(string(fields[3]), 10, 64)
		if err != nil || size < 0 || size > maximumCandidateReportedBlobBytes {
			return "", candidateDiffEntry{}, errors.New("inspect task diff: blob metadata exceeds its bound")
		}
	}
	return name, candidateDiffEntry{mode: mode, objectID: objectID, size: size}, nil
}
