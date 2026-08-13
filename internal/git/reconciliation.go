package git

import (
	"context"
	"errors"

	"github.com/comisai/comis-dev-crew/internal/application"
)

// InspectReconciliationCandidate proves that the exact operation-bound task
// branch contains one clean candidate descended from, but different to, its
// pinned base.
func (registry *Registry) InspectReconciliationCandidate(
	ctx context.Context,
	request application.ReconciliationWorkspaceRequest,
) (application.WorkspaceSnapshot, error) {
	if registry == nil || ctx == nil {
		return application.WorkspaceSnapshot{}, errors.New("inspect reconciliation candidate: registry and context are required")
	}
	if err := ctx.Err(); err != nil {
		return application.WorkspaceSnapshot{}, err
	}
	if !repositoryIDPattern.MatchString(request.PreparationOperationID) ||
		!repositoryIDPattern.MatchString(request.TaskHandle) ||
		!repositoryIDPattern.MatchString(request.RepositoryID) ||
		!gitRevisionPattern.MatchString(request.BaseRevision) {
		return application.WorkspaceSnapshot{}, errors.New("inspect reconciliation candidate: request identity is invalid")
	}
	repository, err := registry.Resolve(request.RepositoryID)
	if err != nil {
		return application.WorkspaceSnapshot{}, errors.New("inspect reconciliation candidate: repository is unavailable")
	}
	expectedBranch, operationSuffix := preparedBranch(
		request.RepositoryID, request.TaskHandle, request.PreparationOperationID,
	)
	if err := registry.validateOperationBranch(ctx, repository, expectedBranch, operationSuffix); err != nil {
		return application.WorkspaceSnapshot{}, errors.New("inspect reconciliation candidate: preparation branch is ambiguous")
	}
	snapshot, err := registry.InspectCandidate(ctx, CandidateSnapshotRequest{
		TaskHandle: request.TaskHandle, RepositoryID: request.RepositoryID,
		WorktreePath: request.WorktreePath,
	})
	if err != nil {
		return application.WorkspaceSnapshot{}, errors.New("inspect reconciliation candidate: worktree identity is unavailable")
	}
	if snapshot.Branch != expectedBranch {
		return application.WorkspaceSnapshot{}, errors.New("inspect reconciliation candidate: branch differs from preparation")
	}
	if snapshot.Cleanliness != CandidateClean {
		return application.WorkspaceSnapshot{}, errors.New("inspect reconciliation candidate: worktree is not clean")
	}
	if snapshot.HeadRevision == request.BaseRevision {
		return application.WorkspaceSnapshot{}, errors.New("inspect reconciliation candidate: head matches pinned base")
	}
	resolvedBase, err := runGit(ctx, registry.gitExecutable, "--no-optional-locks", "-C", repository.PrimaryCheckout,
		"rev-parse", "--verify", request.BaseRevision+"^{commit}")
	if err != nil || resolvedBase != request.BaseRevision {
		return application.WorkspaceSnapshot{}, errors.New("inspect reconciliation candidate: pinned base is unavailable")
	}
	descendsFromBase, err := gitPredicate(ctx, registry.gitExecutable, "--no-optional-locks", "-C", request.WorktreePath,
		"merge-base", "--is-ancestor", request.BaseRevision, snapshot.HeadRevision)
	if err != nil || !descendsFromBase {
		return application.WorkspaceSnapshot{}, errors.New("inspect reconciliation candidate: head diverges from pinned base")
	}
	return application.WorkspaceSnapshot{
		TaskHandle: request.TaskHandle, RepositoryID: snapshot.RepositoryID,
		WorktreePath: snapshot.WorktreePath, Branch: snapshot.Branch,
		HeadRevision: snapshot.HeadRevision, Cleanliness: application.WorkspaceClean,
	}, nil
}

var _ application.ReconciliationWorkspaceInspector = (*Registry)(nil)
