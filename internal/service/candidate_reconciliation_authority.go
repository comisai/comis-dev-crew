package service

import (
	"github.com/comisai/comis-dev-crew/internal/application"
	"github.com/comisai/comis-dev-crew/internal/domain"
	devgit "github.com/comisai/comis-dev-crew/internal/git"
)

func candidateMatchesReconciledSnapshot(
	task domain.Task,
	observed devgit.CandidateSnapshot,
	durable application.WorkspaceSnapshot,
) bool {
	return durable.Validate() == nil && durable.Cleanliness == application.WorkspaceClean &&
		durable.TaskHandle == task.Handle && durable.RepositoryID == task.RepositoryID &&
		observed.RepositoryID == durable.RepositoryID && observed.WorktreePath == durable.WorktreePath &&
		observed.Branch == durable.Branch && observed.HeadRevision == durable.HeadRevision &&
		observed.Cleanliness == devgit.CandidateClean
}
