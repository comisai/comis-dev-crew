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
	if integrationWriterCustodyProven(request) {
		return validateIntegrationMaterializationTopology(expected, resulting)
	}
	return validateFreshMaterializationTopology(expected, resulting)
}

// integrationWriterCustodyProven reports whether the operation already revalidated
// the durable task, evidence, worktree, and sequencer state naming an exact prior
// operation. Custody-free materialization may only add entries; a recovery that
// carries that proof may also rewrite and remove them.
func integrationWriterCustodyProven(request application.IntegrationAdapterRequest) bool {
	return request.RecoveryOperationID != ""
}

func validateFreshMaterializationTopology(
	expected integrationTreeSnapshot,
	resulting integrationTreeSnapshot,
) error {
	if err := validateIntegrationMaterializationTopology(expected, resulting); err != nil {
		return err
	}
	for name, previous := range expected {
		result, retained := resulting[name]
		if !retained {
			return errors.New("apply integration candidate: materialization deletion is unsupported")
		}
		if previous.mode == result.mode && previous.objectID == result.objectID {
			continue
		}
		return errors.New("apply integration candidate: existing entry materialization is unsupported")
	}
	return nil
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

func regularIntegrationMode(mode string) bool {
	return mode == "100644" || mode == "100755"
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
