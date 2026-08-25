package git

import (
	"bytes"
	"context"
	"errors"
	"strconv"
	"strings"

	"github.com/comisai/comis-dev-crew/internal/application"
)

func (registry *Registry) validateIsolatedMaterializationTopology(
	ctx context.Context,
	workspace gitWorkspaceEnvironment,
	expectedHead string,
	resultingHead string,
) error {
	expected, err := registry.loadIsolatedMaterializationSnapshot(ctx, workspace, expectedHead)
	if err != nil {
		return err
	}
	resulting, err := registry.loadIsolatedMaterializationSnapshot(ctx, workspace, resultingHead)
	if err != nil {
		return err
	}
	return validateIntegrationMaterializationTopology(expected, resulting)
}

func (registry *Registry) validateLiveMaterializationBaseBeforeImport(
	ctx context.Context,
	request application.IntegrationAdapterRequest,
) error {
	tree, err := registry.integrationCommitTree(
		ctx, request.Target.WorktreePath, request.Target.ExpectedHead,
	)
	if err != nil {
		return err
	}
	snapshot, err := registry.loadIntegrationTreeSnapshot(ctx, request.Target.WorktreePath, tree)
	if err != nil {
		return err
	}
	matches, err := integrationWorktreeMatchesMaterializationSnapshot(request.Target.WorktreePath, snapshot)
	if err != nil || !matches {
		return errors.New("apply integration candidate: live materialization topology is blocked")
	}
	indexTree, err := registry.integrationIndexTree(ctx, request.Target.WorktreePath)
	if err != nil || indexTree != tree {
		return errors.New("apply integration candidate: live materialization index differs")
	}
	return nil
}

func (registry *Registry) loadIsolatedMaterializationSnapshot(
	ctx context.Context,
	workspace gitWorkspaceEnvironment,
	revision string,
) (integrationTreeSnapshot, error) {
	tree, err := runGitInWorkspace(ctx, registry.gitExecutable, workspace,
		"rev-parse", "--verify", revision+"^{tree}")
	if err != nil || !gitRevisionPattern.MatchString(tree) {
		return nil, errors.New("apply integration candidate: isolated result tree is unavailable")
	}
	listing, err := runGitBytesInWorkspaceWithLimit(ctx, registry.gitExecutable, workspace,
		maximumIntegrationTreeListing, "ls-tree", "-r", "-z", "--full-tree", tree)
	if err != nil {
		return nil, errors.New("apply integration candidate: isolated result tree listing is unavailable")
	}
	snapshot := make(integrationTreeSnapshot)
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
			return nil, errors.New("apply integration candidate: isolated result tree entry is invalid")
		}
		mode, objectID, entryPath := string(fields[0]), string(fields[2]), string(name)
		if mode != "100644" && mode != "100755" && mode != "120000" ||
			!gitRevisionPattern.MatchString(objectID) || !validIntegrationTreePath(entryPath) {
			return nil, errors.New("apply integration candidate: isolated result tree entry is unsafe")
		}
		entries++
		snapshot[entryPath] = integrationTreeEntry{mode: mode, objectID: objectID}
		if _, exists := seenObjects[objectID]; !exists {
			seenObjects[objectID] = struct{}{}
			objectIDs = append(objectIDs, objectID)
		}
	}
	if len(objectIDs) == 0 {
		return snapshot, nil
	}
	input := []byte(strings.Join(objectIDs, "\n") + "\n")
	limit := maximumIntegrationTreeBytes + len(objectIDs)*128
	output, err := runGitBytesInWorkspaceWithInputAndLimit(
		ctx, registry.gitExecutable, workspace, input, limit, "cat-file", "--batch",
	)
	if err != nil {
		return nil, errors.New("apply integration candidate: isolated result blobs are unavailable")
	}
	remaining := output
	total := 0
	for _, expectedID := range objectIDs {
		line, rest, found := bytes.Cut(remaining, []byte{'\n'})
		fields := bytes.Fields(line)
		if !found || len(fields) != 3 || string(fields[0]) != expectedID || string(fields[1]) != "blob" {
			return nil, errors.New("apply integration candidate: isolated result blob identity differs")
		}
		size, sizeErr := strconv.Atoi(string(fields[2]))
		if sizeErr != nil || size < 0 || size > maximumIntegrationBlobBytes || size > len(rest)-1 || rest[size] != '\n' {
			return nil, errors.New("apply integration candidate: isolated result blob exceeds its bound")
		}
		total += size
		if total > maximumIntegrationTreeBytes {
			return nil, errors.New("apply integration candidate: isolated result tree exceeds its bound")
		}
		remaining = rest[size+1:]
	}
	if len(remaining) != 0 {
		return nil, errors.New("apply integration candidate: isolated result blob response is ambiguous")
	}
	return snapshot, nil
}
