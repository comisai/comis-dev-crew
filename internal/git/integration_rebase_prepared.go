package git

import (
	"context"
	"errors"

	"github.com/comisai/comis-dev-crew/internal/application"
)

func (registry *Registry) restorePreparedRebaseTarget(
	ctx context.Context,
	request application.IntegrationAdapterRequest,
	targetRef string,
	currentHead string,
	headRef string,
	attached bool,
) (bool, error) {
	proofRef := integrationRebaseProofRef(request)
	if !attached || headRef != proofRef || currentHead != request.Candidate.HeadRevision {
		return false, nil
	}
	if err := registry.ensureRebaseSequencerAbsent(ctx, request.Target.WorktreePath); err != nil {
		return false, err
	}
	proofHead, found, err := registry.integrationReceiptHeadAtPath(ctx, request.Target.WorktreePath, proofRef)
	if err != nil || !found || proofHead != request.Candidate.HeadRevision {
		return false, errors.New("apply integration candidate: prepared rebase proof differs")
	}
	branchHead, err := registry.integrationBranchHead(ctx, request.Target.WorktreePath, targetRef)
	if err != nil || branchHead != request.Target.ExpectedHead {
		return false, errors.New("apply integration candidate: prepared rebase target differs")
	}
	clean, err := registry.integrationWorktreeCleanAtCommit(
		ctx, request.Target.WorktreePath, request.Candidate.HeadRevision,
	)
	if err != nil || !clean {
		return false, errors.New("apply integration candidate: prepared rebase is not clean")
	}
	candidateTree, err := registry.integrationCommitTree(
		ctx, request.Target.WorktreePath, request.Candidate.HeadRevision,
	)
	if err != nil {
		return false, err
	}
	targetTree, err := registry.integrationCommitTree(ctx, request.Target.WorktreePath, request.Target.ExpectedHead)
	if err != nil {
		return false, err
	}
	candidateSnapshot, err := registry.loadIntegrationTreeSnapshot(ctx, request.Target.WorktreePath, candidateTree)
	if err != nil {
		return false, err
	}
	targetSnapshot, err := registry.loadIntegrationTreeSnapshot(ctx, request.Target.WorktreePath, targetTree)
	if err != nil {
		return false, err
	}
	if err := registry.validateIntegrationExecutionPolicy(ctx, request); err != nil {
		return false, err
	}
	if err := registry.validateIntegrationMutationDeadline(request); err != nil {
		return false, err
	}
	workspace, err := registry.integrationMaterializationWorkspace(ctx, request.Target.WorktreePath)
	if err != nil {
		return false, err
	}
	if _, err := runGitBytesInWorkspace(ctx, registry.gitExecutable, workspace,
		"read-tree", "--reset", request.Target.ExpectedHead); err != nil {
		return false, errors.New("apply integration candidate: prepared rebase index could not be restored")
	}
	if err := materializeIntegrationWorktree(
		request.Target.WorktreePath, candidateSnapshot, targetSnapshot,
	); err != nil {
		return false, err
	}
	if _, err := runGitBytes(ctx, registry.gitExecutable, "--no-optional-locks", "-C", request.Target.WorktreePath,
		"symbolic-ref", "HEAD", targetRef); err != nil {
		return false, errors.New("apply integration candidate: prepared rebase target could not be restored")
	}
	return true, nil
}
