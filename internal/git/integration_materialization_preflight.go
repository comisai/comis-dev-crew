package git

import (
	"context"
	"errors"
	"path"

	"github.com/comisai/comis-dev-crew/internal/application"
)

func (registry *Registry) validateIntegrationMaterializationResult(
	ctx context.Context,
	request application.IntegrationAdapterRequest,
	resultingHead string,
) error {
	expectedTree, err := registry.integrationCommitTree(
		ctx, request.Target.WorktreePath, request.Target.ExpectedHead,
	)
	if err != nil {
		return err
	}
	resultingTree, err := registry.integrationCommitTree(ctx, request.Target.WorktreePath, resultingHead)
	if err != nil {
		return err
	}
	expected, err := registry.loadIntegrationTreeSnapshot(ctx, request.Target.WorktreePath, expectedTree)
	if err != nil {
		return err
	}
	resulting, err := registry.loadIntegrationTreeSnapshot(ctx, request.Target.WorktreePath, resultingTree)
	if err != nil {
		return err
	}
	return validateIntegrationMaterializationTopology(expected, resulting)
}

func validateIntegrationMaterializationTopology(
	expected integrationTreeSnapshot,
	resulting integrationTreeSnapshot,
) error {
	if snapshotContainsMaterializationAncestor(expected, resulting) ||
		snapshotContainsMaterializationAncestor(resulting, expected) {
		return errors.New("apply integration candidate: materialization directory transition is unsupported")
	}
	return nil
}

func snapshotContainsMaterializationAncestor(
	entries integrationTreeSnapshot,
	paths integrationTreeSnapshot,
) bool {
	for name := range paths {
		for parent := path.Dir(name); parent != "."; parent = path.Dir(parent) {
			if _, exists := entries[parent]; exists {
				return true
			}
		}
	}
	return false
}
