package git

import (
	"bytes"
	"context"
	"errors"
	"strconv"
	"strings"
)

func (registry *Registry) validateIsolatedMaterializationSnapshot(
	ctx context.Context,
	workspace gitWorkspaceEnvironment,
	resultingHead string,
) error {
	tree, err := runGitInWorkspace(ctx, registry.gitExecutable, workspace,
		"rev-parse", "--verify", resultingHead+"^{tree}")
	if err != nil || !gitRevisionPattern.MatchString(tree) {
		return errors.New("apply integration candidate: isolated result tree is unavailable")
	}
	listing, err := runGitBytesInWorkspaceWithLimit(ctx, registry.gitExecutable, workspace,
		maximumIntegrationTreeListing, "ls-tree", "-r", "-z", "--full-tree", tree)
	if err != nil {
		return errors.New("apply integration candidate: isolated result tree listing is unavailable")
	}
	objectIDs := make([]string, 0)
	seenObjects := make(map[string]struct{})
	entries := 0
	for _, encoded := range bytes.Split(listing, []byte{0}) {
		if len(encoded) == 0 {
			continue
		}
		metadata, name, found := bytes.Cut(encoded, []byte{'\t'})
		fields := bytes.Fields(metadata)
		if !found || len(fields) != 3 || string(fields[1]) != "blob" || entries == maximumIntegrationTreeEntries {
			return errors.New("apply integration candidate: isolated result tree entry is invalid")
		}
		mode, objectID, entryPath := string(fields[0]), string(fields[2]), string(name)
		if mode != "100644" && mode != "100755" && mode != "120000" ||
			!gitRevisionPattern.MatchString(objectID) || !validIntegrationTreePath(entryPath) {
			return errors.New("apply integration candidate: isolated result tree entry is unsafe")
		}
		entries++
		if _, exists := seenObjects[objectID]; !exists {
			seenObjects[objectID] = struct{}{}
			objectIDs = append(objectIDs, objectID)
		}
	}
	if len(objectIDs) == 0 {
		return nil
	}
	input := []byte(strings.Join(objectIDs, "\n") + "\n")
	limit := maximumIntegrationTreeBytes + len(objectIDs)*128
	output, err := runGitBytesInWorkspaceWithInputAndLimit(
		ctx, registry.gitExecutable, workspace, input, limit, "cat-file", "--batch",
	)
	if err != nil {
		return errors.New("apply integration candidate: isolated result blobs are unavailable")
	}
	remaining := output
	total := 0
	for _, expectedID := range objectIDs {
		line, rest, found := bytes.Cut(remaining, []byte{'\n'})
		fields := bytes.Fields(line)
		if !found || len(fields) != 3 || string(fields[0]) != expectedID || string(fields[1]) != "blob" {
			return errors.New("apply integration candidate: isolated result blob identity differs")
		}
		size, sizeErr := strconv.Atoi(string(fields[2]))
		if sizeErr != nil || size < 0 || size > maximumIntegrationBlobBytes || size > len(rest)-1 || rest[size] != '\n' {
			return errors.New("apply integration candidate: isolated result blob exceeds its bound")
		}
		total += size
		if total > maximumIntegrationTreeBytes {
			return errors.New("apply integration candidate: isolated result tree exceeds its bound")
		}
		remaining = rest[size+1:]
	}
	if len(remaining) != 0 {
		return errors.New("apply integration candidate: isolated result blob response is ambiguous")
	}
	return nil
}
