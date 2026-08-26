package git

import (
	"context"
	"errors"

	"github.com/comisai/comis-dev-crew/internal/application"
)

func (registry *Registry) preflightIntegrationWorktrees(
	ctx context.Context,
	request application.IntegrationAdapterRequest,
) error {
	for _, reference := range []CandidateSnapshotRequest{
		{
			TaskHandle: request.Target.TaskHandle, RepositoryID: request.Target.RepositoryID,
			WorktreePath: request.Target.WorktreePath,
		},
		{
			TaskHandle: request.Candidate.TaskHandle, RepositoryID: request.Candidate.RepositoryID,
			WorktreePath: request.Candidate.WorktreePath,
		},
	} {
		if _, err := registry.inspectCandidateWorktreeIdentity(ctx, reference); err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return errors.New("apply integration candidate: worktree identity is unavailable")
		}
	}
	return nil
}
