package git

import (
	"context"

	"github.com/comisai/comis-dev-crew/internal/application"
)

func (registry *Registry) completedMaterialization(
	ctx context.Context,
	request application.IntegrationAdapterRequest,
	transition integrationMaterializationTransition,
) bool {
	headRef, attached, err := registry.integrationHeadRef(ctx, request.Target.WorktreePath)
	if err != nil || !attached || headRef != transition.TargetRef {
		return false
	}
	branchHead, err := registry.integrationBranchHead(ctx, request.Target.WorktreePath, transition.TargetRef)
	if err != nil || branchHead != transition.ResultingHead {
		return false
	}
	indexTree, err := registry.integrationIndexTree(ctx, request.Target.WorktreePath)
	if err != nil || indexTree != transition.ResultingTree {
		return false
	}
	snapshot, err := registry.loadIntegrationTreeSnapshot(ctx, request.Target.WorktreePath, transition.ResultingTree)
	if err != nil {
		return false
	}
	matches, err := integrationWorktreeMatchesMaterializationSnapshot(request.Target.WorktreePath, snapshot)
	if err != nil || !matches {
		return false
	}
	expected, err := registry.loadIntegrationTreeSnapshot(ctx, request.Target.WorktreePath, transition.ExpectedTree)
	if err != nil {
		return false
	}
	recoveryAvailable, recoveryErr := integrationMaterializationRecoveryStatus(
		request.Target.WorktreePath, expected, snapshot,
	)
	return recoveryErr == nil && !recoveryAvailable
}
