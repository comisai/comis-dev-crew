package git

import (
	"context"

	"github.com/comisai/comis-dev-crew/internal/application"
)

// PrepareWorkspace implements the application allocation port while keeping
// repository paths and Git identity details inside the adapter.
func (registry *Registry) PrepareWorkspace(
	ctx context.Context,
	request application.WorkspacePreparationRequest,
) (application.PreparedWorkspace, error) {
	prepared, err := registry.PrepareWorktree(ctx, PrepareWorktreeRequest{
		OperationID: request.OperationID, TaskHandle: request.TaskHandle,
		RepositoryID: request.RepositoryID, BaseRevision: request.BaseRevision,
	})
	if err != nil {
		return application.PreparedWorkspace{}, err
	}
	return application.PreparedWorkspace{CanonicalRoot: prepared.CanonicalPath}, nil
}

// InspectWorkspace implements the application handback observation port using
// the same exact worktree identity checks as candidate validation.
func (registry *Registry) InspectWorkspace(
	ctx context.Context,
	request application.WorkspaceSnapshotRequest,
) (application.WorkspaceSnapshot, error) {
	snapshot, err := registry.InspectCandidate(ctx, CandidateSnapshotRequest{
		TaskHandle: request.TaskHandle, RepositoryID: request.RepositoryID, WorktreePath: request.WorktreePath,
	})
	if err != nil {
		return application.WorkspaceSnapshot{}, err
	}
	cleanliness := application.WorkspaceClean
	if snapshot.Cleanliness == CandidateDirty {
		cleanliness = application.WorkspaceDirty
	}
	return application.WorkspaceSnapshot{
		TaskHandle: request.TaskHandle, RepositoryID: snapshot.RepositoryID,
		WorktreePath: snapshot.WorktreePath, Branch: snapshot.Branch,
		HeadRevision: snapshot.HeadRevision, Cleanliness: cleanliness,
	}, nil
}

var _ application.WorkspacePreparer = (*Registry)(nil)
var _ application.WorkspaceInspector = (*Registry)(nil)
