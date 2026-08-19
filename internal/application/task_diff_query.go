package application

import (
	"context"
	"errors"
	"time"

	"github.com/comisai/comis-dev-crew/internal/domain"
)

// TaskDiffRequest names the exact task-owned worktree and the revision its work
// is measured from. Both are read from durable state, never from the caller.
type TaskDiffRequest struct {
	TaskHandle   string
	RepositoryID string
	WorktreePath string
	BaseRevision string
}

// TaskFileChange is one changed path with its numeric extent.
type TaskFileChange struct {
	Path         string `json:"path"`
	PreviousPath string `json:"previousPath,omitempty"`
	Added        int    `json:"added"`
	Deleted      int    `json:"deleted"`
	Binary       bool   `json:"binary,omitempty"`
}

// TaskDiffTotals is the bounded extent of one change set.
type TaskDiffTotals struct {
	Files       int `json:"files"`
	Added       int `json:"added"`
	Deleted     int `json:"deleted"`
	BinaryFiles int `json:"binaryFiles"`
}

// TaskDiffView is the bounded operator summary of one task's work.
//
// Committed and uncommitted work are separate because they mean different
// things: one is what the worker stands behind, the other is what a handback
// would land in a developer's editor.
type TaskDiffView struct {
	SchemaVersion     int              `json:"schemaVersion"`
	CapturedAt        time.Time        `json:"capturedAt"`
	StateVersion      int64            `json:"stateVersion"`
	TaskHandle        string           `json:"taskHandle"`
	RepositoryID      string           `json:"repositoryId"`
	BaseRevision      string           `json:"baseRevision"`
	HeadRevision      string           `json:"headRevision"`
	Committed         []TaskFileChange `json:"committed,omitempty"`
	Uncommitted       []TaskFileChange `json:"uncommitted,omitempty"`
	CommittedTotals   TaskDiffTotals   `json:"committedTotals"`
	UncommittedTotals TaskDiffTotals   `json:"uncommittedTotals"`
	// FileListTruncated states the change set outgrew this bounded read, so a
	// partial listing is never presented as a complete one.
	FileListTruncated bool `json:"fileListTruncated,omitempty"`
}

// TaskDiffInspector independently observes what one task changed in Git.
type TaskDiffInspector interface {
	InspectTaskDiff(context.Context, TaskDiffRequest) (TaskDiffView, error)
}

// DiffTask summarizes what one task changed inside its own worktree.
//
// The base revision and workspace root come from durable state rather than the
// request, so a caller cannot aim the read at a tree the task does not own or
// measure the work from a revision nobody pinned.
func (queries *Queries) DiffTask(ctx context.Context, taskHandle string) (TaskDiffView, error) {
	if err := domain.ValidateTaskHandle(taskHandle); err != nil {
		return TaskDiffView{}, invalidReferenceFailure("task reference", err)
	}
	if queries.taskDiffs == nil {
		return TaskDiffView{}, translateReadError(nil, "task diff")
	}
	task, err := queries.repository.GetTask(ctx, taskHandle)
	if err != nil {
		return TaskDiffView{}, translateReadError(err, "task")
	}
	stateVersion, err := queries.repository.CurrentStateVersion(ctx)
	if err != nil {
		return TaskDiffView{}, translateReadError(err, "task diff")
	}
	preparation, err := queries.repository.GetManagedRunPreparation(ctx, taskHandle)
	if err != nil {
		return TaskDiffView{}, translateReadError(err, "task workspace")
	}
	// A task with no recorded workspace root has nothing to read. Returning an
	// empty change set would claim the worker changed nothing.
	if preparation.RequestedWorkspaceRoot == "" {
		return TaskDiffView{}, translateReadError(errors.New("task workspace root is unavailable"), "task workspace")
	}
	view, err := queries.taskDiffs.InspectTaskDiff(ctx, TaskDiffRequest{
		TaskHandle: taskHandle, RepositoryID: task.RepositoryID,
		WorktreePath: preparation.RequestedWorkspaceRoot, BaseRevision: task.BaseRevision,
	})
	if err != nil {
		return TaskDiffView{}, translateReadError(err, "task diff")
	}
	view.SchemaVersion = 1
	view.CapturedAt = queries.now()
	view.StateVersion = stateVersion
	view.TaskHandle = taskHandle
	view.RepositoryID = task.RepositoryID
	return view, nil
}
