package application

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"reflect"
	"time"

	"github.com/comisai/comis-dev-crew/internal/domain"
)

// TaskCleanupStage is the durable crash-recovery point for exact cleanup.
type TaskCleanupStage string

const (
	CleanupPrepared          TaskCleanupStage = "prepared"
	CleanupHostReleased      TaskCleanupStage = "host_released"
	CleanupRemovalAuthorized TaskCleanupStage = "removal_authorized"
	CleanupCompleted         TaskCleanupStage = "completed"
)

const (
	// CleanupOpenHoldMessage is the content-free operator-visible blocker
	// consumed by the protected campaign oracle.
	CleanupOpenHoldMessage = "cleanup is blocked by an open task hold"
	// CleanupOpenDecisionMessage is the content-free operator-visible blocker
	// consumed by the protected campaign oracle.
	CleanupOpenDecisionMessage = "cleanup is blocked by an unresolved task decision"
	// CleanupUnattestedScoutMessage is the content-free operator-visible blocker
	// for a scout whose decision inventory is missing or still unresolved.
	CleanupUnattestedScoutMessage = "cleanup is blocked by a missing or unresolved scout decision inventory"
	// CleanupActiveExecutionMessage is the content-free operator-visible blocker
	// consumed by the protected campaign oracle.
	CleanupActiveExecutionMessage = "cleanup is blocked by active task execution"
	// CleanupUnknownExecutionMessage is the content-free operator-visible blocker
	// consumed by the protected campaign oracle.
	CleanupUnknownExecutionMessage = "cleanup requires settled task execution evidence"
	// CleanupDirtyWorkspaceMessage is the content-free operator-visible blocker
	// consumed by the protected campaign oracle.
	CleanupDirtyWorkspaceMessage = "cleanup requires a clean task worktree"
	// CleanupStaleForgeTruthMessage is the content-free operator-visible blocker
	// consumed by the protected campaign oracle.
	CleanupStaleForgeTruthMessage = "cleanup requires current matching pull request truth"
)

// ManagedRunReleaseDisposition is the closed host release behavior.
type ManagedRunReleaseDisposition string

const ManagedRunReleaseReapSafe ManagedRunReleaseDisposition = "reap_safe"

// ManagedRunReleaseState is the exact terminal host acknowledgement.
type ManagedRunReleaseState string

const ManagedRunReleased ManagedRunReleaseState = "released"

// ForgeCheckTruth is one current required-check observation.
type ForgeCheckTruth struct {
	Name       string
	Conclusion domain.CheckConclusion
}

// PullRequestDeliveryVerification selects exact recorded delivery truth.
type PullRequestDeliveryVerification struct {
	RepositoryID   string
	PullRequestID  string
	Branch         string
	HeadRevision   string
	RequiredChecks []string
}

// PullRequestDeliveryTruth is current read-only forge truth.
type PullRequestDeliveryTruth struct {
	RepositoryID  string
	PullRequestID string
	HeadRevision  string
	Checks        []ForgeCheckTruth
}

// ManagedRunReleaseRequest revokes exact run capabilities and its lease.
type ManagedRunReleaseRequest struct {
	OperationID      string
	ManagedRunID     string
	WorkspaceLeaseID string
	Disposition      ManagedRunReleaseDisposition
	ReleasedAt       time.Time
}

// ManagedRunReleaseReceipt is the exact host release acknowledgement.
type ManagedRunReleaseReceipt struct {
	ManagedRunID     string
	WorkspaceLeaseID string
	Disposition      ManagedRunReleaseDisposition
	ReleasedAt       time.Time
	State            ManagedRunReleaseState
}

// DeliveredWorkspaceRemoval carries only a previously authorized Git identity.
type DeliveredWorkspaceRemoval struct {
	PreparationOperationID string
	TaskHandle             string
	RepositoryID           string
	WorktreePath           string
	Branch                 string
	HeadRevision           string
}

// TaskCleanupRecord is one durable staged cleanup operation.
type TaskCleanupRecord struct {
	OperationID            string
	SubjectDigest          string
	TaskHandle             string
	PreparationOperationID string
	ManagedRunID           string
	WorkspaceLeaseID       string
	RepositoryID           string
	WorktreePath           string
	HeadRevision           string
	EvidenceDigest         string
	PullRequestID          string
	RequiredForgeChecks    []string
	ReportArtifactHash     string
	Stage                  TaskCleanupStage
	ReleaseOperationID     string
	ReleasedAt             time.Time
	Snapshot               WorkspaceSnapshot
	// Discard marks a removal of work that was never delivered. Its safety proof
	// is an operator's explicit acknowledgement rather than delivery evidence,
	// because by definition there is none to check.
	Discard bool
}

// CleanupTaskCommand is the stable operator mutation.
type CleanupTaskCommand struct {
	OperationID string
	TaskHandle  string
}

// TaskCleanupMutation begins or replays one durable hold.
type TaskCleanupMutation struct {
	OperationID        string
	SubjectDigest      string
	TaskHandle         string
	ReleaseOperationID string
	ReleasedAt         time.Time
	At                 time.Time
}

// TaskCleanupHostReleaseMutation records exact host acknowledgement only
// after fresh workspace and delivery verification.
type TaskCleanupHostReleaseMutation struct {
	OperationID   string
	SubjectDigest string
	Snapshot      WorkspaceSnapshot
	DeliveryTruth PullRequestDeliveryTruth
	Receipt       ManagedRunReleaseReceipt
	At            time.Time
}

// TaskCleanupRemovalAuthorization records the second fresh safety proof after
// host capability and lease release.
type TaskCleanupRemovalAuthorization struct {
	OperationID   string
	SubjectDigest string
	Snapshot      WorkspaceSnapshot
	DeliveryTruth PullRequestDeliveryTruth
	At            time.Time
}

// TaskCleanupCompletion records convergence after exact Git removal.
type TaskCleanupCompletion struct {
	OperationID          string
	SubjectDigest        string
	RequestOperationID   string
	RequestSubjectDigest string
	At                   time.Time
}

// TaskCleanupStore owns durable stage changes and database safety proofs.
type TaskCleanupStore interface {
	BeginTaskCleanup(context.Context, TaskCleanupMutation) (TaskCleanupRecord, error)
	RecordTaskCleanupHostRelease(context.Context, TaskCleanupHostReleaseMutation) (TaskCleanupRecord, error)
	AuthorizeTaskCleanupRemoval(context.Context, TaskCleanupRemovalAuthorization) (TaskCleanupRecord, error)
	CompleteTaskCleanup(context.Context, TaskCleanupCompletion) (MutationResult, error)
}

// PullRequestDeliveryVerifier re-reads delivery with no mutation authority.
type PullRequestDeliveryVerifier interface {
	VerifyPullRequestDelivery(context.Context, PullRequestDeliveryVerification) (PullRequestDeliveryTruth, error)
}

// ManagedRunReleaser revokes host authority using one stable request.
type ManagedRunReleaser interface {
	ReleaseManagedRun(context.Context, ManagedRunReleaseRequest) (ManagedRunReleaseReceipt, error)
}

// RuntimeAttachmentReleaser revokes the service-owned task reporter endpoint.
type RuntimeAttachmentReleaser interface {
	ReleaseRuntimeAttachment(context.Context, string) error
}

// DeliveredWorkspaceRemover removes one previously authorized exact workspace.
type DeliveredWorkspaceRemover interface {
	RemoveDeliveredWorkspace(context.Context, DeliveredWorkspaceRemoval) error
}

// CleanupCoordinatorConfig supplies the complete E0 cleanup authority set.
type CleanupCoordinatorConfig struct {
	Store       TaskCleanupStore
	Workspaces  WorkspaceInspector
	Forge       PullRequestDeliveryVerifier
	Releaser    ManagedRunReleaser
	Attachments RuntimeAttachmentReleaser
	Remover     DeliveredWorkspaceRemover
	Clock       Clock
}

// CleanupCoordinator executes the durable release-before-remove sequence.
type CleanupCoordinator struct {
	config CleanupCoordinatorConfig
}

// NewCleanupCoordinator rejects partial cleanup authority.
func NewCleanupCoordinator(config CleanupCoordinatorConfig) (*CleanupCoordinator, error) {
	if config.Store == nil || config.Workspaces == nil || config.Forge == nil || config.Releaser == nil ||
		config.Attachments == nil || config.Remover == nil || config.Clock == nil {
		return nil, errors.New("create cleanup coordinator: complete cleanup authority is required")
	}
	return &CleanupCoordinator{config: config}, nil
}

// CleanupTask holds the task, releases exact host authority, re-proves current
// safety, removes the exact worktree, and records completion.
func (coordinator *CleanupCoordinator) CleanupTask(ctx context.Context, command CleanupTaskCommand) (MutationResult, error) {
	if coordinator == nil || ctx == nil {
		return MutationResult{}, errors.New("cleanup task: coordinator and context are required")
	}
	if err := ctx.Err(); err != nil {
		return MutationResult{}, err
	}
	if domain.ValidateOperationID(command.OperationID) != nil || domain.ValidateTaskHandle(command.TaskHandle) != nil {
		return MutationResult{}, mutationValidationFailure("cleanup command identity is invalid")
	}
	digest, err := digestMutationSubject(command)
	if err != nil {
		return MutationResult{}, mutationValidationFailure("cleanup subject cannot be encoded")
	}
	observed := coordinator.config.Clock()
	now := time.UnixMilli(observed.UnixMilli()).UTC()
	record, err := coordinator.config.Store.BeginTaskCleanup(ctx, TaskCleanupMutation{
		OperationID: command.OperationID, SubjectDigest: digest, TaskHandle: command.TaskHandle,
		ReleaseOperationID: cleanupReleaseOperationID(command.OperationID, command.TaskHandle),
		ReleasedAt:         now, At: now,
	})
	if err != nil {
		return MutationResult{}, cleanupCommitFailure(err)
	}
	return coordinator.runRemovalStages(ctx, record, command.OperationID, command.TaskHandle, digest)
}

// runRemovalStages drives the release-before-remove sequence to completion.
//
// Cleanup and discard differ only in what they must prove before entering it —
// delivery evidence for one, an operator's explicit acknowledgement for the
// other. The sequence itself is identical and stays written once: releasing host
// authority before removing a worktree, and recording each stage so a crash
// resumes rather than repeats, is exactly the part that must not diverge
// between two commands that both end in an irreversible deletion.
func (coordinator *CleanupCoordinator) runRemovalStages(
	ctx context.Context,
	record TaskCleanupRecord,
	commandOperationID string,
	commandTaskHandle string,
	digest string,
) (MutationResult, error) {
	var err error
	command := CleanupTaskCommand{OperationID: commandOperationID, TaskHandle: commandTaskHandle}
	for {
		if record.TaskHandle != command.TaskHandle {
			return MutationResult{}, errors.New("cleanup task: durable operation identity differs")
		}
		switch record.Stage {
		case CleanupPrepared:
			snapshot, truth, verifyErr := coordinator.verifyCurrentSafety(ctx, record)
			if verifyErr != nil {
				return MutationResult{}, cleanupVerificationFailure(verifyErr)
			}
			receipt, releaseErr := coordinator.config.Releaser.ReleaseManagedRun(ctx, ManagedRunReleaseRequest{
				OperationID: record.ReleaseOperationID, ManagedRunID: record.ManagedRunID,
				WorkspaceLeaseID: record.WorkspaceLeaseID, Disposition: ManagedRunReleaseReapSafe,
				ReleasedAt: record.ReleasedAt,
			})
			if releaseErr != nil {
				return MutationResult{}, cleanupDependencyFailure(
					"managed run release failed",
					"inspect the exact managed run and workspace lease in Comis before retrying",
					releaseErr,
				)
			}
			if receipt.ManagedRunID != record.ManagedRunID || receipt.WorkspaceLeaseID != record.WorkspaceLeaseID ||
				receipt.Disposition != ManagedRunReleaseReapSafe || !receipt.ReleasedAt.Equal(record.ReleasedAt) ||
				receipt.State != ManagedRunReleased {
				return MutationResult{}, errors.New("cleanup task: host release acknowledgement differs")
			}
			record, err = coordinator.config.Store.RecordTaskCleanupHostRelease(ctx, TaskCleanupHostReleaseMutation{
				OperationID: record.OperationID, SubjectDigest: record.SubjectDigest, Snapshot: snapshot,
				DeliveryTruth: truth, Receipt: receipt, At: coordinator.config.Clock(),
			})
		case CleanupHostReleased:
			if releaseErr := coordinator.config.Attachments.ReleaseRuntimeAttachment(ctx, record.TaskHandle); releaseErr != nil {
				return MutationResult{}, cleanupDependencyFailure(
					"runtime attachment release failed",
					"inspect the exact task runtime attachment before retrying cleanup",
					releaseErr,
				)
			}
			snapshot, truth, verifyErr := coordinator.verifyCurrentSafety(ctx, record)
			if verifyErr != nil {
				return MutationResult{}, cleanupVerificationFailure(verifyErr)
			}
			record, err = coordinator.config.Store.AuthorizeTaskCleanupRemoval(ctx, TaskCleanupRemovalAuthorization{
				OperationID: record.OperationID, SubjectDigest: record.SubjectDigest, Snapshot: snapshot,
				DeliveryTruth: truth, At: coordinator.config.Clock(),
			})
		case CleanupRemovalAuthorized:
			if err := coordinator.config.Remover.RemoveDeliveredWorkspace(ctx, DeliveredWorkspaceRemoval{
				PreparationOperationID: record.PreparationOperationID, TaskHandle: record.TaskHandle,
				RepositoryID: record.RepositoryID, WorktreePath: record.Snapshot.WorktreePath,
				Branch: record.Snapshot.Branch, HeadRevision: record.Snapshot.HeadRevision,
			}); err != nil {
				return MutationResult{}, cleanupDependencyFailure(
					"delivered workspace removal failed",
					"inspect the operation-bound worktree and Git repository before retrying",
					err,
				)
			}
			return coordinator.config.Store.CompleteTaskCleanup(ctx, TaskCleanupCompletion{
				OperationID: record.OperationID, SubjectDigest: record.SubjectDigest,
				RequestOperationID: command.OperationID, RequestSubjectDigest: digest,
				At: coordinator.config.Clock(),
			})
		case CleanupCompleted:
			return coordinator.config.Store.CompleteTaskCleanup(ctx, TaskCleanupCompletion{
				OperationID: record.OperationID, SubjectDigest: record.SubjectDigest,
				RequestOperationID: command.OperationID, RequestSubjectDigest: digest,
				At: coordinator.config.Clock(),
			})
		default:
			return MutationResult{}, errors.New("cleanup task: durable stage is invalid")
		}
		if err != nil {
			return MutationResult{}, mutationCommitFailure(err)
		}
	}
}

func (coordinator *CleanupCoordinator) verifyCurrentSafety(
	ctx context.Context,
	record TaskCleanupRecord,
) (WorkspaceSnapshot, PullRequestDeliveryTruth, error) {
	snapshot, err := coordinator.config.Workspaces.InspectWorkspace(ctx, WorkspaceSnapshotRequest{
		TaskHandle: record.TaskHandle, RepositoryID: record.RepositoryID, WorktreePath: record.WorktreePath,
	})
	if err != nil {
		return WorkspaceSnapshot{}, PullRequestDeliveryTruth{}, cleanupDependencyFailure(
			"cleanup workspace inspection failed",
			"inspect the exact task worktree and configured repository before retrying",
			err,
		)
	}
	// A discard has no recorded head to match: the work was never delivered, so
	// nothing pinned one. Its identity proof is the task, repository and worktree.
	headMatches := record.Discard || snapshot.HeadRevision == record.HeadRevision
	if snapshot.Validate() != nil || snapshot.TaskHandle != record.TaskHandle ||
		snapshot.RepositoryID != record.RepositoryID ||
		snapshot.WorktreePath != record.WorktreePath || !headMatches {
		return WorkspaceSnapshot{}, PullRequestDeliveryTruth{}, fmt.Errorf("cleanup workspace safety differs: %w", ErrPrecondition)
	}
	if record.Discard {
		// A discard removes work nobody delivered, so there is no delivery truth
		// to verify and a dirty tree is the ordinary case — uncommitted work is
		// usually the thing being thrown away. The operator acknowledged that
		// when they asked; re-checking cleanliness here would refuse exactly the
		// case the command exists for.
		return snapshot, PullRequestDeliveryTruth{}, nil
	}
	if snapshot.Cleanliness != WorkspaceClean {
		return WorkspaceSnapshot{}, PullRequestDeliveryTruth{}, cleanupDirtyWorkspaceFailure()
	}
	if record.PullRequestID == "" {
		if record.ReportArtifactHash == "" {
			return WorkspaceSnapshot{}, PullRequestDeliveryTruth{}, errors.New("cleanup delivery evidence is unavailable")
		}
		return snapshot, PullRequestDeliveryTruth{}, nil
	}
	truth, err := coordinator.config.Forge.VerifyPullRequestDelivery(ctx, PullRequestDeliveryVerification{
		RepositoryID: record.RepositoryID, PullRequestID: record.PullRequestID, Branch: snapshot.Branch,
		HeadRevision: record.HeadRevision, RequiredChecks: append([]string(nil), record.RequiredForgeChecks...),
	})
	if err != nil {
		if errors.Is(err, ErrCleanupStaleForgeTruth) {
			return WorkspaceSnapshot{}, PullRequestDeliveryTruth{}, err
		}
		return WorkspaceSnapshot{}, PullRequestDeliveryTruth{}, cleanupDependencyFailure(
			"cleanup pull request verification failed",
			"inspect the recorded pull request, head, and required checks before retrying",
			err,
		)
	}
	wantChecks := make([]ForgeCheckTruth, len(record.RequiredForgeChecks))
	for index, name := range record.RequiredForgeChecks {
		wantChecks[index] = ForgeCheckTruth{Name: name, Conclusion: domain.CheckPassed}
	}
	if truth.RepositoryID != record.RepositoryID || truth.PullRequestID != record.PullRequestID ||
		truth.HeadRevision != record.HeadRevision || !reflect.DeepEqual(truth.Checks, wantChecks) {
		return WorkspaceSnapshot{}, PullRequestDeliveryTruth{}, fmt.Errorf(
			"cleanup pull request truth differs: %w", ErrCleanupStaleForgeTruth,
		)
	}
	return snapshot, truth, nil
}

func cleanupVerificationFailure(cause error) error {
	var failure *domain.Failure
	if errors.As(cause, &failure) {
		return cause
	}
	return cleanupCommitFailure(cause)
}

func cleanupCommitFailure(cause error) error {
	var message, hint string
	switch {
	case errors.Is(cause, ErrCleanupOpenHold):
		message = CleanupOpenHoldMessage
		hint = "close the exact task cleanup hold, then retry cleanup"
	case errors.Is(cause, ErrCleanupOpenDecision):
		message = CleanupOpenDecisionMessage
		hint = "resolve the exact open task decision, then retry cleanup"
	case errors.Is(cause, ErrCleanupUnattestedScout):
		message = CleanupUnattestedScoutMessage
		hint = "record the scout's decision inventory with an attestation that names its open decisions or states there are none, then retry cleanup"
	case errors.Is(cause, ErrCleanupActiveExecution):
		message = CleanupActiveExecutionMessage
		hint = "wait for the exact task execution and validation processes to settle, then retry cleanup"
	case errors.Is(cause, ErrCleanupUnknownExecution):
		message = CleanupUnknownExecutionMessage
		hint = "reconcile the exact terminal, managed run, and workspace lease before retrying cleanup"
	case errors.Is(cause, ErrCleanupStaleForgeTruth):
		message = CleanupStaleForgeTruthMessage
		hint = "refresh the exact pull request head and required checks, then retry cleanup"
	default:
		return mutationCommitFailure(cause)
	}
	failure, err := domain.NewFailure(
		domain.ErrorPrecondition,
		true,
		message,
		hint,
		cause,
	)
	if err != nil {
		return errors.New("cleanup blocker failure classification failed")
	}
	return failure
}

func cleanupDirtyWorkspaceFailure() error {
	failure, err := domain.NewFailure(
		domain.ErrorPrecondition,
		true,
		CleanupDirtyWorkspaceMessage,
		"remove uncommitted changes from the exact task worktree, then retry cleanup",
		ErrPrecondition,
	)
	if err != nil {
		return errors.New("cleanup dirty workspace failure classification failed")
	}
	return failure
}

func cleanupDependencyFailure(message, hint string, cause error) error {
	failure, err := domain.NewFailure(domain.ErrorUnavailable, true, message, hint, cause)
	if err != nil {
		return errors.New("cleanup dependency failure classification failed")
	}
	return failure
}

func cleanupReleaseOperationID(operationID, taskHandle string) string {
	digest := fmt.Sprintf("%x", sha256.Sum256([]byte(operationID+"\x00"+taskHandle)))
	return "release-" + digest[:32]
}
