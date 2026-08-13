package application

import (
	"context"
	"errors"
	"time"

	"github.com/comisai/comis-dev-crew/internal/domain"
)

const commandReconcileTask = "ReconcileTask"

// ReconcileTaskAction is the closed recovery action for a task whose worker
// terminal ended without authoritative candidate evidence.
type ReconcileTaskAction string

const (
	// ReconcileValidateCleanCandidate authorizes only fresh validation of the
	// exact clean non-base candidate resolved from durable task authority.
	ReconcileValidateCleanCandidate ReconcileTaskAction = "validate-clean-candidate"
)

// ReconcileTaskCommand selects one server-owned task and closed recovery
// action. It deliberately contains no path, branch, head, run, or lease input.
type ReconcileTaskCommand struct {
	OperationID string              `json:"operationId"`
	TaskHandle  string              `json:"taskHandle"`
	Action      ReconcileTaskAction `json:"action"`
}

// TaskReconciliationAuthority is the durable server-side evidence required
// before Git inspection. None of these fields is caller-selectable.
type TaskReconciliationAuthority struct {
	Task                   domain.Task
	Preparation            ManagedRunPreparation
	PreparationOperationID string
	TerminalSessionID      string
	TerminalTransition     TerminalTransition
	TerminalObservedAt     time.Time
}

// ReconciliationWorkspaceRequest asks the Git adapter to resolve and verify
// the operation-bound worktree without accepting filesystem authority from a
// caller.
type ReconciliationWorkspaceRequest struct {
	PreparationOperationID string
	TaskHandle             string
	RepositoryID           string
	WorktreePath           string
	BaseRevision           string
}

// ReconciliationWorkspaceInspector proves current repository, worktree,
// branch, head, and cleanliness identity for the durable preparation.
type ReconciliationWorkspaceInspector interface {
	InspectReconciliationCandidate(context.Context, ReconciliationWorkspaceRequest) (WorkspaceSnapshot, error)
}

// TaskCandidateReconciliationMutation is the complete atomic recovery input.
// It records fresh evidence but never creates a worker report.
type TaskCandidateReconciliationMutation struct {
	OperationID            string
	SubjectDigest          string
	TaskHandle             string
	Action                 ReconcileTaskAction
	PreparationOperationID string
	Snapshot               WorkspaceSnapshot
	TerminalSessionID      string
	TerminalTransition     TerminalTransition
	TerminalObservedAt     time.Time
	ExpectedTaskVersion    int64
	At                     time.Time
}

// TaskCandidateReconciliationStore is the narrow durable recovery port.
type TaskCandidateReconciliationStore interface {
	ReplayMutation(context.Context, string, string, string) (MutationResult, bool, error)
	ReadTaskReconciliationAuthority(context.Context, string) (TaskReconciliationAuthority, error)
	CommitTaskCandidateReconciliation(context.Context, TaskCandidateReconciliationMutation) (MutationResult, error)
}

// TaskCandidateReconcilerConfig supplies durable and fresh Git authority.
type TaskCandidateReconcilerConfig struct {
	Store      TaskCandidateReconciliationStore
	Workspaces ReconciliationWorkspaceInspector
	Clock      Clock
}

// TaskCandidateReconciler coordinates unknown-to-validating recovery without
// inferring success from terminal exit or synthesizing a worker report.
type TaskCandidateReconciler struct {
	store      TaskCandidateReconciliationStore
	workspaces ReconciliationWorkspaceInspector
	clock      Clock
}

// NewTaskCandidateReconciler creates the canonical unknown-task recovery
// application service.
func NewTaskCandidateReconciler(config TaskCandidateReconcilerConfig) (*TaskCandidateReconciler, error) {
	if config.Store == nil || config.Workspaces == nil || config.Clock == nil {
		return nil, errors.New("create task candidate reconciler: store, workspaces, and clock are required")
	}
	return &TaskCandidateReconciler{store: config.Store, workspaces: config.Workspaces, clock: config.Clock}, nil
}

// ReconcileTask proves fresh server-owned terminal and Git evidence, then
// atomically enters normal candidate validation.
func (reconciler *TaskCandidateReconciler) ReconcileTask(
	ctx context.Context,
	command ReconcileTaskCommand,
) (MutationResult, error) {
	if err := validMutationContext(ctx); err != nil {
		return MutationResult{}, err
	}
	if domain.ValidateOperationID(command.OperationID) != nil ||
		domain.ValidateTaskHandle(command.TaskHandle) != nil ||
		command.Action != ReconcileValidateCleanCandidate {
		return MutationResult{}, mutationValidationFailure("task reconciliation fields are invalid")
	}
	subjectDigest, err := digestMutationSubject(command)
	if err != nil {
		return MutationResult{}, mutationValidationFailure("task reconciliation subject cannot be encoded")
	}
	if replay, found, replayErr := reconciler.store.ReplayMutation(
		ctx, command.OperationID, commandReconcileTask, subjectDigest,
	); replayErr != nil {
		return MutationResult{}, mutationReplayFailure(replayErr)
	} else if found {
		return replay, nil
	}
	authority, err := reconciler.store.ReadTaskReconciliationAuthority(ctx, command.TaskHandle)
	if err != nil {
		if errors.Is(err, ErrNotFound) || errors.Is(err, ErrPrecondition) {
			return MutationResult{}, reconciliationPreconditionFailure(
				"task reconciliation authority is incomplete",
				"inspect the exact task preparation and terminal binding before retrying",
				err,
			)
		}
		return MutationResult{}, reconciliationUnavailableFailure(
			"task reconciliation authority is unavailable",
			"inspect durable task, preparation, and terminal state before retrying the same operation",
			err,
		)
	}
	if err := validateTaskReconciliationAuthority(authority, command.TaskHandle); err != nil {
		return MutationResult{}, err
	}
	snapshot, err := reconciler.workspaces.InspectReconciliationCandidate(ctx, ReconciliationWorkspaceRequest{
		PreparationOperationID: authority.PreparationOperationID,
		TaskHandle:             authority.Task.Handle, RepositoryID: authority.Task.RepositoryID,
		WorktreePath: authority.Preparation.RequestedWorkspaceRoot,
		BaseRevision: authority.Task.BaseRevision,
	})
	if err != nil {
		return MutationResult{}, reconciliationUnavailableFailure(
			"candidate workspace could not be verified",
			"restore the exact registered worktree and repository identity, then retry",
			err,
		)
	}
	if snapshot.Validate() != nil || snapshot.TaskHandle != authority.Task.Handle ||
		snapshot.RepositoryID != authority.Task.RepositoryID ||
		snapshot.WorktreePath != authority.Preparation.RequestedWorkspaceRoot {
		return MutationResult{}, reconciliationPreconditionFailure(
			"candidate workspace identity differs from durable task authority",
			"preserve the worktree and inspect its registered repository identity",
			nil,
		)
	}
	if snapshot.Cleanliness != WorkspaceClean {
		return MutationResult{}, reconciliationPreconditionFailure(
			"candidate worktree is not clean",
			"commit or remove the intended worktree changes before retrying reconciliation",
			nil,
		)
	}
	if snapshot.HeadRevision == authority.Task.BaseRevision {
		return MutationResult{}, reconciliationPreconditionFailure(
			"candidate head matches the pinned base",
			"preserve the task and prepare a replacement worker when no candidate commit exists",
			nil,
		)
	}
	now := reconciler.clock()
	if now.IsZero() || now.Location() != time.UTC || now.Before(authority.Task.UpdatedAt) ||
		now.Before(authority.TerminalObservedAt) {
		return MutationResult{}, reconciliationUnavailableFailure(
			"task reconciliation time is unavailable",
			"restore the service UTC clock before retrying the same operation",
			nil,
		)
	}
	result, err := reconciler.store.CommitTaskCandidateReconciliation(ctx, TaskCandidateReconciliationMutation{
		OperationID: command.OperationID, SubjectDigest: subjectDigest,
		TaskHandle: authority.Task.Handle, Action: command.Action,
		PreparationOperationID: authority.PreparationOperationID, Snapshot: snapshot,
		TerminalSessionID: authority.TerminalSessionID, TerminalTransition: authority.TerminalTransition,
		TerminalObservedAt:  authority.TerminalObservedAt,
		ExpectedTaskVersion: authority.Task.StateVersion, At: now,
	})
	if err != nil {
		if errors.Is(err, ErrConflict) {
			return MutationResult{}, mutationReplayFailure(err)
		}
		if errors.Is(err, ErrPrecondition) || errors.Is(err, domain.ErrInvalidTransition) {
			return MutationResult{}, reconciliationPreconditionFailure(
				"task reconciliation authority changed before commit",
				"inspect the exact task and retry with a new operation identity only after the authority is stable",
				err,
			)
		}
		return MutationResult{}, reconciliationUnavailableFailure(
			"durable task reconciliation did not complete",
			"query the stable operation identity before retrying",
			err,
		)
	}
	if result.Task.Handle != "" && result.Task.Handle != authority.Task.Handle {
		return MutationResult{}, reconciliationUnavailableFailure(
			"task reconciliation result identity differs",
			"inspect durable service state before retrying",
			nil,
		)
	}
	return result, nil
}

func validateTaskReconciliationAuthority(authority TaskReconciliationAuthority, taskHandle string) error {
	task := authority.Task
	if task.Validate() != nil || task.Handle != taskHandle || task.State != domain.TaskUnknown ||
		task.ManagedRunID == "" || task.WorkspaceLeaseID == "" || task.ExecutionAttachmentID == "" ||
		domain.ValidateOperationID(authority.PreparationOperationID) != nil ||
		authority.Preparation.Validate(task.CreatedAt) != nil ||
		authority.Preparation.ExternalRunRef != task.Handle || authority.Preparation.State != PreparationOpen ||
		authority.Preparation.RequestedWorkspaceRoot == "" {
		return reconciliationPreconditionFailure(
			"task is not eligible for clean-candidate reconciliation",
			"inspect the unknown task preparation and exact managed-run binding before retrying",
			nil,
		)
	}
	if domain.ValidateAuthorityReference("terminalSessionId", authority.TerminalSessionID) != nil ||
		(authority.TerminalTransition != TerminalExited && authority.TerminalTransition != TerminalReleased) ||
		authority.TerminalObservedAt.IsZero() || authority.TerminalObservedAt.Location() != time.UTC {
		return reconciliationPreconditionFailure(
			"terminal settlement is not proven",
			"wait for an authenticated exited or released terminal observation before retrying",
			nil,
		)
	}
	return nil
}

func reconciliationPreconditionFailure(message, hint string, cause error) error {
	failure, err := domain.NewFailure(domain.ErrorPrecondition, false, message, hint, cause)
	if err != nil {
		return errors.New("task reconciliation precondition classification failed")
	}
	return failure
}

func reconciliationUnavailableFailure(message, hint string, cause error) error {
	failure, err := domain.NewFailure(domain.ErrorUnavailable, true, message, hint, cause)
	if err != nil {
		return errors.New("task reconciliation availability classification failed")
	}
	return failure
}
