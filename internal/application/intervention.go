package application

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/comisai/comis-dev-crew/internal/domain"
)

const commandHandbackTask = "HandbackTask"

// HandbackAction is the closed E0 intervention result requested after a worker
// has reported a safe pause and released its terminal.
type HandbackAction string

const (
	HandbackValidateDeveloperWork HandbackAction = "validate-developer-work"
)

// WorkspaceCleanliness is the fresh tracked and untracked worktree posture.
type WorkspaceCleanliness string

const (
	WorkspaceClean WorkspaceCleanliness = "clean"
	WorkspaceDirty WorkspaceCleanliness = "dirty"
)

// WorkspaceSnapshot is independently observed Git identity captured at
// handback. It contains no diff or repository content.
type WorkspaceSnapshot struct {
	TaskHandle   string               `json:"taskHandle"`
	RepositoryID string               `json:"repositoryId"`
	WorktreePath string               `json:"worktreePath"`
	Branch       string               `json:"branch"`
	HeadRevision string               `json:"headRevision"`
	Cleanliness  WorkspaceCleanliness `json:"cleanliness"`
}

// Validate rejects incomplete, escaped, or content-bearing snapshot fields.
func (snapshot WorkspaceSnapshot) Validate() error {
	if domain.ValidateTaskHandle(snapshot.TaskHandle) != nil ||
		domain.ValidateAuthorityReference("repositoryId", snapshot.RepositoryID) != nil ||
		domain.ValidateGitRevision(snapshot.HeadRevision) != nil ||
		!filepath.IsAbs(snapshot.WorktreePath) || filepath.Clean(snapshot.WorktreePath) != snapshot.WorktreePath ||
		snapshot.Branch == "" || len(snapshot.Branch) > 255 || strings.ContainsAny(snapshot.Branch, "\x00\r\n\t ") ||
		(snapshot.Cleanliness != WorkspaceClean && snapshot.Cleanliness != WorkspaceDirty) {
		return errors.New("workspace snapshot is invalid")
	}
	return nil
}

// TaskHandbackMutation is the exact durable paused-to-validation transaction.
type TaskHandbackMutation struct {
	OperationID   string
	SubjectDigest string
	TaskHandle    string
	Action        HandbackAction
	Snapshot      WorkspaceSnapshot
	At            time.Time
}

// HandbackTaskCommand requests one closed E0 intervention action.
type HandbackTaskCommand struct {
	OperationID string         `json:"operationId"`
	TaskHandle  string         `json:"taskHandle"`
	Action      HandbackAction `json:"action"`
}

// WorkspaceSnapshotRequest selects the exact durable task root to inspect.
type WorkspaceSnapshotRequest struct {
	TaskHandle   string
	RepositoryID string
	WorktreePath string
}

// WorkspaceInspector independently observes current Git identity and status.
type WorkspaceInspector interface {
	InspectWorkspace(context.Context, WorkspaceSnapshotRequest) (WorkspaceSnapshot, error)
}

// InterventionStore is the narrow durable port for paused-worktree handback.
type InterventionStore interface {
	ReplayMutation(context.Context, string, string, string) (MutationResult, bool, error)
	GetTask(context.Context, string) (domain.Task, error)
	GetManagedRunPreparation(context.Context, string) (ManagedRunPreparation, error)
	CommitTaskHandback(context.Context, TaskHandbackMutation) (MutationResult, error)
}

// InterventionConfig supplies independent workspace truth and durable state.
type InterventionConfig struct {
	Store      InterventionStore
	Workspaces WorkspaceInspector
	Clock      Clock
}

// Interventions coordinates E0 pause/edit/revalidate without terminal custody.
type Interventions struct {
	store      InterventionStore
	workspaces WorkspaceInspector
	clock      Clock
}

// NewInterventions creates the canonical handback application service.
func NewInterventions(config InterventionConfig) (*Interventions, error) {
	if config.Store == nil || config.Workspaces == nil || config.Clock == nil {
		return nil, errors.New("create interventions: store, workspaces, and clock are required")
	}
	return &Interventions{store: config.Store, workspaces: config.Workspaces, clock: config.Clock}, nil
}

// HandbackTask captures fresh Git truth and durably starts normal validation.
func (interventions *Interventions) HandbackTask(
	ctx context.Context,
	command HandbackTaskCommand,
) (MutationResult, error) {
	if err := validMutationContext(ctx); err != nil {
		return MutationResult{}, err
	}
	if domain.ValidateOperationID(command.OperationID) != nil ||
		domain.ValidateTaskHandle(command.TaskHandle) != nil || command.Action != HandbackValidateDeveloperWork {
		return MutationResult{}, mutationValidationFailure("handback fields are invalid")
	}
	subjectDigest, err := digestMutationSubject(command)
	if err != nil {
		return MutationResult{}, mutationValidationFailure("handback subject cannot be encoded")
	}
	if replay, found, err := interventions.store.ReplayMutation(ctx, command.OperationID, commandHandbackTask, subjectDigest); err != nil {
		return MutationResult{}, mutationReplayFailure(err)
	} else if found {
		return replay, nil
	}
	task, err := interventions.store.GetTask(ctx, command.TaskHandle)
	if err != nil {
		return MutationResult{}, mutationCommitFailure(err)
	}
	if task.State != domain.TaskPaused {
		return MutationResult{}, mutationCommitFailure(ErrPrecondition)
	}
	preparation, err := interventions.store.GetManagedRunPreparation(ctx, task.Handle)
	if err != nil || preparation.RequestedWorkspaceRoot == "" {
		return MutationResult{}, &dependencyFailure{message: "handback workspace is unavailable", cause: err}
	}
	snapshot, err := interventions.workspaces.InspectWorkspace(ctx, WorkspaceSnapshotRequest{
		TaskHandle: task.Handle, RepositoryID: task.RepositoryID, WorktreePath: preparation.RequestedWorkspaceRoot,
	})
	if err != nil {
		return MutationResult{}, &dependencyFailure{message: "handback workspace inspection failed", cause: err}
	}
	if snapshot.Validate() != nil || snapshot.TaskHandle != task.Handle || snapshot.RepositoryID != task.RepositoryID ||
		snapshot.WorktreePath != preparation.RequestedWorkspaceRoot {
		return MutationResult{}, &dependencyFailure{message: "handback workspace inspection returned different authority"}
	}
	result, err := interventions.store.CommitTaskHandback(ctx, TaskHandbackMutation{
		OperationID: command.OperationID, SubjectDigest: subjectDigest,
		TaskHandle: task.Handle, Action: command.Action, Snapshot: snapshot, At: interventions.clock(),
	})
	if err != nil {
		return MutationResult{}, mutationCommitFailure(err)
	}
	if result.Task.Handle != "" && result.Task.Handle != task.Handle {
		return MutationResult{}, fmt.Errorf("handback result task identity differs")
	}
	return result, nil
}
