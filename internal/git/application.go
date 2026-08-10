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

var _ application.WorkspacePreparer = (*Registry)(nil)
