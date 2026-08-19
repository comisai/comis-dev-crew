package git

import (
	"context"
	"errors"

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

// RemoveDeliveredWorkspace implements the application cleanup port using the
// exact operation-bound Git remover.
func (registry *Registry) RemoveDeliveredWorkspace(
	ctx context.Context,
	request application.DeliveredWorkspaceRemoval,
) error {
	return registry.RemoveDeliveredWorktree(ctx, DeliveredWorktreeCleanupRequest{
		PreparationOperationID: request.PreparationOperationID, TaskHandle: request.TaskHandle,
		RepositoryID: request.RepositoryID, WorktreePath: request.WorktreePath,
		Branch: request.Branch, HeadRevision: request.HeadRevision,
	})
}

// SynchronizePrimary implements the application synchronization port. The
// adapter's own closed vocabularies are mapped rather than shared, so the
// application never depends on this adapter's types and an unmapped outcome
// fails loudly instead of travelling as an empty string.
func (registry *Registry) SynchronizePrimary(
	ctx context.Context,
	command application.PrimarySyncCommand,
) (application.PrimarySyncReport, error) {
	result, err := registry.SynchronizePrimaryCheckout(ctx, PrimarySyncRequest{RepositoryID: command.RepositoryID})
	if err != nil {
		return application.PrimarySyncReport{}, err
	}
	outcome, mapped := primarySyncOutcomes[result.Outcome]
	if !mapped {
		return application.PrimarySyncReport{}, errors.New("synchronize primary checkout: outcome is unknown")
	}
	report := application.PrimarySyncReport{
		RepositoryID: result.RepositoryID, Branch: result.Branch,
		PreviousHead: result.PreviousHead, Head: result.Head, Outcome: outcome,
	}
	if outcome != application.PrimarySyncRefused {
		return report, nil
	}
	refusal, mapped := primarySyncRefusals[result.Refusal]
	if !mapped {
		return application.PrimarySyncReport{}, errors.New("synchronize primary checkout: refusal is unknown")
	}
	report.Refusal = refusal
	return report, nil
}

var primarySyncOutcomes = map[PrimarySyncOutcome]application.PrimarySyncOutcome{
	PrimarySyncUpdated:        application.PrimarySyncUpdated,
	PrimarySyncAlreadyCurrent: application.PrimarySyncAlreadyCurrent,
	PrimarySyncRefused:        application.PrimarySyncRefused,
}

var primarySyncRefusals = map[PrimarySyncRefusal]application.PrimarySyncRefusal{
	PrimarySyncRefusalDirty:          application.PrimarySyncRefusalDirty,
	PrimarySyncRefusalDivergent:      application.PrimarySyncRefusalDivergent,
	PrimarySyncRefusalDetached:       application.PrimarySyncRefusalDetached,
	PrimarySyncRefusalNonDefault:     application.PrimarySyncRefusalNonDefault,
	PrimarySyncRefusalUpstreamAbsent: application.PrimarySyncRefusalUpstreamAbsent,
	PrimarySyncRefusalAmbiguous:      application.PrimarySyncRefusalAmbiguous,
}

var _ application.WorkspacePreparer = (*Registry)(nil)
var _ application.PrimarySynchronizer = (*Registry)(nil)
var _ application.WorkspaceInspector = (*Registry)(nil)
var _ application.DeliveredWorkspaceRemover = (*Registry)(nil)

// InspectTaskDiff implements the application diff observation port using the
// same exact worktree identity checks as candidate validation.
func (registry *Registry) InspectTaskDiff(
	ctx context.Context,
	request application.TaskDiffRequest,
) (application.TaskDiffView, error) {
	diff, err := registry.InspectCandidateDiff(ctx, CandidateDiffRequest{
		TaskHandle: request.TaskHandle, RepositoryID: request.RepositoryID,
		WorktreePath: request.WorktreePath, BaseRevision: request.BaseRevision,
	})
	if err != nil {
		return application.TaskDiffView{}, err
	}
	return application.TaskDiffView{
		BaseRevision: diff.BaseRevision, HeadRevision: diff.HeadRevision,
		Committed: portFileChanges(diff.Committed), Uncommitted: portFileChanges(diff.Uncommitted),
		CommittedTotals:   portDiffTotals(diff.CommittedTotals),
		UncommittedTotals: portDiffTotals(diff.UncommittedTotals),
		FileListTruncated: diff.FileListTruncated,
	}, nil
}

func portFileChanges(changes []CandidateFileChange) []application.TaskFileChange {
	if len(changes) == 0 {
		return nil
	}
	ported := make([]application.TaskFileChange, 0, len(changes))
	for _, change := range changes {
		ported = append(ported, application.TaskFileChange{
			Path: change.Path, PreviousPath: change.PreviousPath,
			Added: change.Added, Deleted: change.Deleted, Binary: change.Binary,
		})
	}
	return ported
}

func portDiffTotals(totals CandidateDiffTotals) application.TaskDiffTotals {
	return application.TaskDiffTotals{
		Files: totals.Files, Added: totals.Added,
		Deleted: totals.Deleted, BinaryFiles: totals.BinaryFiles,
	}
}
