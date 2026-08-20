package application

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/comisai/comis-dev-crew/internal/domain"
)

// MergeApprovalState is the closed authenticated host-receipt posture.
type MergeApprovalState string

const (
	MergeApprovalConsumed        MergeApprovalState = "consumed"
	MergeApprovalIdenticalReplay MergeApprovalState = "identical_replay"
)

// MergeApprovalConsumeRequest asks the host to consume authority already bound
// to the exact managed MCP operation. No caller-supplied fingerprint is trusted.
type MergeApprovalConsumeRequest struct {
	OperationID       string
	ManagedRunID      string
	ApprovalRequestID string
	MCPOperationID    string
}

// MergeApprovalReceipt is the application-owned form of one authenticated
// Comis approval receipt.
type MergeApprovalReceipt struct {
	State                MergeApprovalState
	ApprovalRequestID    string
	ManagedRunID         string
	MCPOperationID       string
	ResolvingPrincipalID string
	OperationFingerprint string
	ApprovedAt           time.Time
	ExpiresAt            time.Time
	ConsumedAt           time.Time
}

func (receipt MergeApprovalReceipt) domain(taskHandle, head string, operatorEnabled bool) domain.MergeApproval {
	return domain.MergeApproval{
		TaskHandle: taskHandle, ApprovalID: receipt.ApprovalRequestID,
		ManagedRunID: receipt.ManagedRunID, MCPOperationID: receipt.MCPOperationID,
		ResolvingPrincipal: receipt.ResolvingPrincipalID, OperationFingerprint: receipt.OperationFingerprint,
		ApprovedHead: head, ApprovedAt: receipt.ApprovedAt, ExpiresAt: receipt.ExpiresAt,
		ConsumedAt: receipt.ConsumedAt, OperatorEnabled: operatorEnabled,
	}
}

// MergeApprovalConsumer obtains an exact one-shot host receipt.
type MergeApprovalConsumer interface {
	ConsumeMergeApproval(context.Context, MergeApprovalConsumeRequest) (MergeApprovalReceipt, error)
}

// PullRequestMergeMethod is the closed operator-owned forge merge strategy.
type PullRequestMergeMethod string

const (
	PullRequestMergeCommit PullRequestMergeMethod = "merge"
	PullRequestMergeSquash PullRequestMergeMethod = "squash"
	PullRequestMergeRebase PullRequestMergeMethod = "rebase"
)

// PullRequestMergeRequest carries only store-resolved, approval-bound forge
// identity. The caller cannot choose a repository or merge method.
type PullRequestMergeRequest struct {
	OperationID    string
	RepositoryID   string
	PullRequestID  string
	Branch         string
	HeadRevision   string
	RequiredChecks []string
}

// PullRequestMergeReceipt is exact post-mutation forge truth.
type PullRequestMergeReceipt struct {
	RepositoryID        string
	PullRequestID       string
	HeadRevision        string
	MergeCommitRevision string
	Method              PullRequestMergeMethod
}

// ApprovedPullRequestMerger owns the separately credentialed forge mutation.
type ApprovedPullRequestMerger interface {
	MergeApprovedPullRequest(context.Context, PullRequestMergeRequest) (PullRequestMergeReceipt, error)
}

// TaskMergeState is the durable crash-recovery posture of one command.
type TaskMergeState string

const (
	TaskMergeAwaitingApproval    TaskMergeState = "awaiting_approval"
	TaskMergeExecutionAuthorized TaskMergeState = "execution_authorized"
	TaskMergeCompleted           TaskMergeState = "completed"
)

// MergeTaskCommand is the one canonical service command. Approval fields are
// absent for CLI requests and present only from private managed MCP metadata.
type MergeTaskCommand struct {
	OperationID       string
	TaskHandle        string
	ApprovalRequestID string
	MCPOperationID    string
}

// TaskMergeReservation asks the single writer to resolve exact current evidence.
type TaskMergeReservation struct {
	OperationID   string
	TaskHandle    string
	SubjectDigest string
	At            time.Time
}

// TaskMergeRecord is the durable authority and result for one merge operation.
type TaskMergeRecord struct {
	OperationID         string
	SubjectDigest       string
	TaskHandle          string
	ManagedRunID        string
	RepositoryID        string
	PullRequestID       string
	Branch              string
	HeadRevision        string
	EvidenceDigest      string
	RequiredChecks      []string
	State               TaskMergeState
	Approval            domain.MergeApproval
	MergeCommitRevision string
	Method              PullRequestMergeMethod
	ReservedAt          time.Time
	CompletedAt         time.Time
}

// TaskMergeAuthorization persists the authenticated receipt before forge mutation.
type TaskMergeAuthorization struct {
	OperationID string
	Approval    domain.MergeApproval
	At          time.Time
}

// TaskMergeCompletion joins the durable authority to fresh post-merge truth.
type TaskMergeCompletion struct {
	OperationID string
	Receipt     PullRequestMergeReceipt
	At          time.Time
}

// TaskMergeStore owns reservation, approval persistence, and exact completion.
type TaskMergeStore interface {
	BeginTaskMerge(context.Context, TaskMergeReservation) (TaskMergeRecord, error)
	AuthorizeTaskMerge(context.Context, TaskMergeAuthorization) (TaskMergeRecord, error)
	CompleteTaskMerge(context.Context, TaskMergeCompletion) (TaskMergeRecord, error)
}

// MergeTaskResult is the bounded replayable service outcome.
type MergeTaskResult struct {
	OperationID          string                 `json:"operationId"`
	TaskHandle           string                 `json:"taskHandle"`
	State                TaskMergeState         `json:"state"`
	RepositoryID         string                 `json:"repositoryId"`
	PullRequestID        string                 `json:"pullRequestId"`
	HeadRevision         string                 `json:"headRevision"`
	ApprovalRequestID    string                 `json:"approvalRequestId,omitempty"`
	ResolvingPrincipalID string                 `json:"resolvingPrincipalId,omitempty"`
	MergeCommitRevision  string                 `json:"mergeCommitRevision,omitempty"`
	Method               PullRequestMergeMethod `json:"method,omitempty"`
	CompletedAt          time.Time              `json:"completedAt,omitempty"`
}

// MergeCoordinatorConfig supplies the complete approval-to-forge authority chain.
type MergeCoordinatorConfig struct {
	Store           TaskMergeStore
	Approvals       MergeApprovalConsumer
	Forge           ApprovedPullRequestMerger
	Clock           Clock
	OperatorEnabled bool
}

// MergeCoordinator executes one durable approval-bound merge transaction.
type MergeCoordinator struct {
	config MergeCoordinatorConfig
}

// NewMergeCoordinator validates the complete merge composition.
func NewMergeCoordinator(config MergeCoordinatorConfig) (*MergeCoordinator, error) {
	if config.Store == nil || config.Approvals == nil || config.Forge == nil || config.Clock == nil {
		return nil, errors.New("create merge coordinator: store, approval consumer, forge, and clock are required")
	}
	return &MergeCoordinator{config: config}, nil
}

// MergeTask reserves exact evidence, persists authenticated approval, executes
// or reconciles the forge mutation, and records only post-merge truth.
func (coordinator *MergeCoordinator) MergeTask(
	ctx context.Context,
	command MergeTaskCommand,
) (MergeTaskResult, error) {
	if coordinator == nil {
		return MergeTaskResult{}, errors.New("merge task: coordinator is required")
	}
	if err := validMutationContext(ctx); err != nil {
		return MergeTaskResult{}, err
	}
	if domain.ValidateOperationID(command.OperationID) != nil || domain.ValidateTaskHandle(command.TaskHandle) != nil ||
		(command.ApprovalRequestID == "") != (command.MCPOperationID == "") ||
		(command.MCPOperationID != "" && command.MCPOperationID != command.OperationID) {
		return MergeTaskResult{}, mutationValidationFailure("merge command identity is invalid")
	}
	subjectDigest, err := digestMutationSubject(struct {
		TaskHandle string `json:"taskHandle"`
	}{TaskHandle: command.TaskHandle})
	if err != nil {
		return MergeTaskResult{}, mutationValidationFailure("merge subject cannot be encoded")
	}
	now := coordinator.config.Clock()
	if now.IsZero() || now.Location() != time.UTC {
		return MergeTaskResult{}, errors.New("merge task: clock returned invalid time")
	}
	record, err := coordinator.config.Store.BeginTaskMerge(ctx, TaskMergeReservation{
		OperationID: command.OperationID, TaskHandle: command.TaskHandle,
		SubjectDigest: subjectDigest, At: now,
	})
	if err != nil {
		return MergeTaskResult{}, mutationCommitFailure(err)
	}
	if err := validateTaskMergeRecord(record, command.OperationID, command.TaskHandle, subjectDigest); err != nil {
		return MergeTaskResult{}, err
	}
	if record.State == TaskMergeCompleted {
		return mergeResult(record), nil
	}
	if record.State == TaskMergeAwaitingApproval {
		if command.ApprovalRequestID == "" {
			return mergeResult(record), nil
		}
		receipt, consumeErr := coordinator.config.Approvals.ConsumeMergeApproval(ctx, MergeApprovalConsumeRequest{
			OperationID: command.OperationID, ManagedRunID: record.ManagedRunID,
			ApprovalRequestID: command.ApprovalRequestID, MCPOperationID: command.MCPOperationID,
		})
		if consumeErr != nil {
			return MergeTaskResult{}, &dependencyFailure{message: "merge approval receipt is unavailable", cause: consumeErr}
		}
		if receipt.State != MergeApprovalConsumed && receipt.State != MergeApprovalIdenticalReplay ||
			receipt.ApprovalRequestID != command.ApprovalRequestID {
			return MergeTaskResult{}, newSafeFailure(
				domain.ErrorPrecondition, false, "merge approval receipt differs from the requested operation",
				"request a fresh approval for the exact task head", ErrPrecondition,
			)
		}
		approval := receipt.domain(record.TaskHandle, record.HeadRevision, coordinator.config.OperatorEnabled)
		if authorizeErr := approval.AuthorizeMerge(domain.MergeAuthorization{
			ObservedHead: record.HeadRevision, ManagedRunID: record.ManagedRunID,
			MCPOperationID: command.MCPOperationID, Now: now,
		}); authorizeErr != nil {
			return MergeTaskResult{}, newSafeFailure(
				domain.ErrorPrecondition, false, "merge approval is not current for this operation",
				"request a fresh approval for the exact task head", authorizeErr,
			)
		}
		record, err = coordinator.config.Store.AuthorizeTaskMerge(ctx, TaskMergeAuthorization{
			OperationID: command.OperationID, Approval: approval, At: now,
		})
		if err != nil {
			return MergeTaskResult{}, mutationCommitFailure(err)
		}
		if err := validateTaskMergeRecord(record, command.OperationID, command.TaskHandle, subjectDigest); err != nil ||
			record.State != TaskMergeExecutionAuthorized {
			return MergeTaskResult{}, errors.New("merge task: stored approval authority differs")
		}
	}
	if !coordinator.config.OperatorEnabled {
		return MergeTaskResult{}, newSafeFailure(
			domain.ErrorPrecondition, false, "merge operation is disabled",
			"enable merge authority in operator configuration and request a fresh approval", ErrPrecondition,
		)
	}
	forgeReceipt, err := coordinator.config.Forge.MergeApprovedPullRequest(ctx, PullRequestMergeRequest{
		OperationID: record.OperationID, RepositoryID: record.RepositoryID,
		PullRequestID: record.PullRequestID, Branch: record.Branch, HeadRevision: record.HeadRevision,
		RequiredChecks: append([]string(nil), record.RequiredChecks...),
	})
	if err != nil {
		return MergeTaskResult{}, &dependencyFailure{message: "merge forge truth is unavailable", cause: err}
	}
	completed, err := coordinator.config.Store.CompleteTaskMerge(ctx, TaskMergeCompletion{
		OperationID: record.OperationID, Receipt: forgeReceipt, At: coordinator.config.Clock(),
	})
	if err != nil {
		return MergeTaskResult{}, mutationCommitFailure(err)
	}
	if err := validateTaskMergeRecord(completed, command.OperationID, command.TaskHandle, subjectDigest); err != nil ||
		completed.State != TaskMergeCompleted {
		return MergeTaskResult{}, errors.New("merge task: completed durable receipt differs")
	}
	return mergeResult(completed), nil
}

func validateTaskMergeRecord(record TaskMergeRecord, operationID, taskHandle, subjectDigest string) error {
	if record.OperationID != operationID || record.TaskHandle != taskHandle || record.SubjectDigest != subjectDigest ||
		domain.ValidateAuthorityReference("managedRunId", record.ManagedRunID) != nil ||
		domain.ValidateRepositoryID(record.RepositoryID) != nil ||
		domain.ValidateAuthorityReference("pullRequestId", record.PullRequestID) != nil ||
		record.Branch == "" || len([]byte(record.Branch)) > 256 || strings.ContainsAny(record.Branch, "\x00\r\n\t ") ||
		domain.ValidateGitRevision(record.HeadRevision) != nil ||
		domain.ValidateBriefRevisionHash(record.EvidenceDigest) != nil || len(record.RequiredChecks) == 0 {
		return errors.New("merge task: durable reservation is invalid")
	}
	seen := make(map[string]struct{}, len(record.RequiredChecks))
	for _, check := range record.RequiredChecks {
		if check == "" || strings.TrimSpace(check) != check || len([]byte(check)) > 128 {
			return errors.New("merge task: durable required check is invalid")
		}
		if _, duplicate := seen[check]; duplicate {
			return errors.New("merge task: durable required check is duplicated")
		}
		seen[check] = struct{}{}
	}
	switch record.State {
	case TaskMergeAwaitingApproval:
		if record.Approval.ApprovalID != "" || record.MergeCommitRevision != "" || record.Method != "" || !record.CompletedAt.IsZero() {
			return errors.New("merge task: awaiting approval record carries later authority")
		}
	case TaskMergeExecutionAuthorized:
		if record.MergeCommitRevision != "" || record.Method != "" || !record.CompletedAt.IsZero() {
			return errors.New("merge task: authorized record carries a completion")
		}
		if err := validateStoredMergeApproval(record); err != nil {
			return err
		}
	case TaskMergeCompleted:
		if validateStoredMergeApproval(record) != nil || domain.ValidateGitRevision(record.MergeCommitRevision) != nil ||
			!validPullRequestMergeMethod(record.Method) || record.CompletedAt.IsZero() || record.CompletedAt.Location() != time.UTC {
			return errors.New("merge task: completed record is invalid")
		}
	default:
		return errors.New("merge task: durable state is invalid")
	}
	return nil
}

func validateStoredMergeApproval(record TaskMergeRecord) error {
	return record.Approval.AuthorizeMerge(domain.MergeAuthorization{
		ObservedHead: record.HeadRevision, ManagedRunID: record.ManagedRunID,
		MCPOperationID: record.Approval.MCPOperationID, Now: record.Approval.ConsumedAt,
	})
}

func validPullRequestMergeMethod(method PullRequestMergeMethod) bool {
	return method == PullRequestMergeCommit || method == PullRequestMergeSquash || method == PullRequestMergeRebase
}

func mergeResult(record TaskMergeRecord) MergeTaskResult {
	return MergeTaskResult{
		OperationID: record.OperationID, TaskHandle: record.TaskHandle, State: record.State,
		RepositoryID: record.RepositoryID, PullRequestID: record.PullRequestID, HeadRevision: record.HeadRevision,
		ApprovalRequestID: record.Approval.ApprovalID, ResolvingPrincipalID: record.Approval.ResolvingPrincipal,
		MergeCommitRevision: record.MergeCommitRevision, Method: record.Method, CompletedAt: record.CompletedAt,
	}
}
