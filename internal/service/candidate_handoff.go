package service

import (
	"context"
	"errors"

	"github.com/comisai/comis-dev-crew/internal/application"
	"github.com/comisai/comis-dev-crew/internal/domain"
	devgit "github.com/comisai/comis-dev-crew/internal/git"
)

type candidateGitInspector interface {
	InspectCandidate(context.Context, devgit.CandidateSnapshotRequest) (devgit.CandidateSnapshot, error)
	PromoteReconciliationCandidate(context.Context, application.ReconciliationWorkspaceRequest) (application.WorkspaceSnapshot, error)
}

func (supervisor *candidateSupervisor) promoteCandidate(
	ctx context.Context,
	task domain.Task,
	preparation application.ManagedRunPreparation,
) (application.WorkspaceSnapshot, error) {
	authority, err := supervisor.config.Store.ReadCandidateHandoffAuthority(ctx, task.Handle)
	if err != nil || domain.ValidateOperationID(authority.PreparationOperationID) != nil ||
		authority.Task.Handle != task.Handle || authority.Task.RepositoryID != task.RepositoryID ||
		authority.Task.BaseRevision != task.BaseRevision ||
		authority.Preparation.RequestedWorkspaceRoot != preparation.RequestedWorkspaceRoot {
		return application.WorkspaceSnapshot{}, errors.New("validate task candidate: candidate handoff authority is unavailable")
	}
	return supervisor.config.Git.PromoteReconciliationCandidate(ctx, application.ReconciliationWorkspaceRequest{
		PreparationOperationID: authority.PreparationOperationID,
		TaskHandle:             task.Handle,
		RepositoryID:           task.RepositoryID,
		WorktreePath:           preparation.RequestedWorkspaceRoot,
		BaseRevision:           task.BaseRevision,
	})
}
