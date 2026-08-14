package application

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/comisai/comis-dev-crew/internal/domain"
)

// Mutations owns canonical E0 prepare and bind command construction.
type Mutations struct {
	store          MutationStore
	repositories   RepositoryCatalog
	workspaces     WorkspacePreparer
	attachments    RuntimeAttachmentCoordinator
	taskIDs        TaskIDSource
	nonces         RegistrationNonceSource
	preparationTTL time.Duration
	clock          Clock
}

var registrationNoncePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._~-]{15,255}$`)
var attachmentTargetNamePattern = regexp.MustCompile(`^attachment-[a-f0-9]{32}\.sock$`)

// NewMutations creates the sole application mutation coordinator.
func NewMutations(config MutationConfig) (*Mutations, error) {
	if config.Store == nil || config.Repositories == nil || config.Workspaces == nil || config.RuntimeAttachments == nil || config.TaskIDs == nil ||
		config.RegistrationNonces == nil || config.Clock == nil {
		return nil, errors.New("create mutations: store, repositories, workspaces, runtime attachments, task IDs, registration nonces, and clock are required")
	}
	if config.PreparationTTL <= 0 || config.PreparationTTL > 24*time.Hour {
		return nil, errors.New("create mutations: preparation TTL must be within 24 hours")
	}
	return &Mutations{
		store: config.Store, repositories: config.Repositories, workspaces: config.Workspaces, attachments: config.RuntimeAttachments,
		taskIDs: config.TaskIDs, nonces: config.RegistrationNonces,
		preparationTTL: config.PreparationTTL, clock: config.Clock,
	}, nil
}

// PrepareTask validates one immutable contract, mints its service-local handle,
// pins its brief, and delegates one atomic task/operation commit.
func (mutations *Mutations) PrepareTask(ctx context.Context, command PrepareTaskCommand) (MutationResult, error) {
	if err := validMutationContext(ctx); err != nil {
		return MutationResult{}, err
	}
	if err := domain.ValidateOperationID(command.OperationID); err != nil {
		return MutationResult{}, mutationValidationFailure("operation ID is invalid")
	}
	subjectDigest, err := digestMutationSubject(command)
	if err != nil {
		return MutationResult{}, mutationValidationFailure("prepare subject cannot be encoded")
	}
	if replay, found, err := mutations.store.ReplayMutation(ctx, command.OperationID, commandPrepareTask, subjectDigest); err != nil {
		return MutationResult{}, mutationReplayFailure(err)
	} else if found {
		return replay, nil
	}
	taskHandle, err := mutations.taskIDs(command.OperationID)
	if err != nil {
		return MutationResult{}, &dependencyFailure{message: "task identity source failed", cause: err}
	}
	now := mutations.clock()
	task := domain.Task{
		SchemaVersion: 1, Handle: taskHandle, ServiceInstanceID: command.ServiceInstanceID,
		State: domain.TaskPrepared, Shape: command.Shape, RepositoryID: command.RepositoryID,
		BaseRevision: command.BaseRevision, BriefRevision: 1,
		AcceptanceCriteria: append([]string(nil), command.AcceptanceCriteria...),
		Constraints:        append([]string(nil), command.Constraints...),
		ValidationProfile:  command.ValidationProfile, DeliveryMode: command.DeliveryMode,
		WorkerProfileID: command.WorkerProfileID, StateVersion: 1, CreatedAt: now, UpdatedAt: now,
	}
	task, err = task.PinBriefRevision()
	if err != nil {
		return MutationResult{}, mutationValidationFailure("task contract is invalid")
	}
	if err := mutations.repositories.ValidateRepository(ctx, command.RepositoryID); err != nil {
		return MutationResult{}, &dependencyFailure{message: "repository validation failed", cause: err}
	}
	workspace, err := mutations.workspaces.PrepareWorkspace(ctx, WorkspacePreparationRequest{
		OperationID: command.OperationID, TaskHandle: task.Handle,
		RepositoryID: command.RepositoryID, BaseRevision: command.BaseRevision,
	})
	if err != nil {
		return MutationResult{}, &dependencyFailure{message: "workspace preparation failed", cause: err}
	}
	if workspace.CanonicalRoot != "" &&
		(!filepath.IsAbs(workspace.CanonicalRoot) || filepath.Clean(workspace.CanonicalRoot) != workspace.CanonicalRoot) {
		return MutationResult{}, &dependencyFailure{message: "workspace preparation returned an invalid root"}
	}
	brief, err := task.RenderWorkerBrief()
	if err != nil {
		return MutationResult{}, mutationValidationFailure("pinned worker brief is invalid")
	}
	attachment, err := mutations.attachments.PrepareRuntimeAttachment(ctx, RuntimeAttachmentPreparationRequest{
		OperationID: command.OperationID, TaskHandle: task.Handle,
		BriefRevision: task.BriefRevision, BriefRevisionHash: task.BriefRevisionHash,
		Brief: brief, WorkingDirectory: workspace.CanonicalRoot,
	})
	if err != nil {
		return MutationResult{}, &dependencyFailure{message: "runtime attachment preparation failed", cause: err}
	}
	if err := attachment.Validate(); err != nil {
		return MutationResult{}, &dependencyFailure{message: "runtime attachment preparation returned an invalid source"}
	}
	nonce, err := mutations.nonces()
	if err != nil {
		return MutationResult{}, &dependencyFailure{message: "registration identity source failed", cause: err}
	}
	preparation := ManagedRunPreparation{
		ExternalRunRef: task.Handle, RegistrationNonce: nonce,
		RequestedWorkspaceRoot: workspace.CanonicalRoot,
		RequestedAttachment:    attachment,
		ExpiresAt:              now.Add(mutations.preparationTTL).UTC(), State: PreparationOpen,
	}
	if err := preparation.Validate(now); err != nil {
		return MutationResult{}, mutationValidationFailure("managed-run preparation is invalid")
	}
	return mutations.store.CommitPreparedTask(ctx, PreparedTaskMutation{
		Task: task, Preparation: preparation,
		OperationID: command.OperationID, SubjectDigest: subjectDigest, At: now,
	})
}

// Validate rejects malformed, expired, or non-UTC activation joins.
func (preparation ManagedRunPreparation) Validate(createdAt time.Time) error {
	if err := domain.ValidateTaskHandle(preparation.ExternalRunRef); err != nil {
		return err
	}
	if !registrationNoncePattern.MatchString(preparation.RegistrationNonce) {
		return errors.New("registration nonce is invalid")
	}
	if preparation.ExpiresAt.Location() != time.UTC || !preparation.ExpiresAt.After(createdAt) {
		return errors.New("preparation expiry is invalid")
	}
	if preparation.RequestedWorkspaceRoot != "" &&
		(!filepath.IsAbs(preparation.RequestedWorkspaceRoot) || filepath.Clean(preparation.RequestedWorkspaceRoot) != preparation.RequestedWorkspaceRoot) {
		return errors.New("requested workspace root is invalid")
	}
	if err := preparation.RequestedAttachment.Validate(); err != nil {
		return errors.New("requested runtime attachment is invalid")
	}
	switch preparation.State {
	case PreparationOpen:
		if preparation.AbandonReason != "" || preparation.Disposition != "" || preparation.ClosedAt != nil {
			return errors.New("open preparation carries terminal fields")
		}
	case PreparationAbandoned:
		if !preparation.AbandonReason.valid() || !preparation.Disposition.valid() || preparation.ClosedAt == nil ||
			preparation.ClosedAt.Location() != time.UTC || preparation.ClosedAt.Before(createdAt) {
			return errors.New("abandoned preparation closure is invalid")
		}
	default:
		return errors.New("preparation state is invalid")
	}
	return nil
}

// Validate rejects sources that cannot be handed to Comis as one exact
// owner-controlled Unix-socket capability.
func (attachment PreparedRuntimeAttachment) Validate() error {
	if attachment.Kind != RuntimeAttachmentUnixSocket || !filepath.IsAbs(attachment.SourcePath) ||
		filepath.Clean(attachment.SourcePath) != attachment.SourcePath || filepath.Base(attachment.SourcePath) != "attachment.sock" ||
		len([]byte(attachment.SourcePath)) > 4096 || strings.ContainsAny(attachment.SourcePath, "\x00\r\n") {
		return errors.New("prepared runtime attachment is invalid")
	}
	return nil
}

func (reason AbandonReason) valid() bool {
	switch reason {
	case AbandonReasonActivationRejected, AbandonReasonOwnerCancelled,
		AbandonReasonRegistrationExpired, AbandonReasonServiceUnavailable:
		return true
	default:
		return false
	}
}

func (disposition AbandonDisposition) valid() bool {
	return disposition == AbandonDispositionReapSafe || disposition == AbandonDispositionPreserve
}

// ActivateManagedRun validates the complete host join and delegates one atomic
// private-preparation check plus prepared-to-ready commit.
func (mutations *Mutations) ActivateManagedRun(ctx context.Context, command ActivateManagedRunCommand) (MutationResult, error) {
	if err := validMutationContext(ctx); err != nil {
		return MutationResult{}, err
	}
	if err := domain.ValidateOperationID(command.OperationID); err != nil {
		return MutationResult{}, mutationValidationFailure("operation ID is invalid")
	}
	if err := domain.ValidateAuthorityReference("serviceInstanceId", command.ServiceInstanceID); err != nil {
		return MutationResult{}, mutationValidationFailure("service instance identity is invalid")
	}
	if err := domain.ValidateTaskHandle(command.ExternalRunRef); err != nil {
		return MutationResult{}, mutationValidationFailure("external run reference is invalid")
	}
	if !registrationNoncePattern.MatchString(command.RegistrationNonce) {
		return MutationResult{}, mutationValidationFailure("registration nonce is invalid")
	}
	if domain.ValidateAuthorityReference("executionAttachmentId", command.ExecutionAttachmentID) != nil ||
		!attachmentTargetNamePattern.MatchString(command.AttachmentTargetName) {
		return MutationResult{}, mutationValidationFailure("execution attachment binding is invalid")
	}
	binding := domain.TaskBinding{ManagedRunID: command.ManagedRunID, WorkspaceLeaseID: command.WorkspaceLeaseID}
	if command.WorkspaceLeaseID != "" {
		if err := binding.Validate(); err != nil {
			return MutationResult{}, mutationValidationFailure("task binding is invalid")
		}
	} else if err := domain.ValidateAuthorityReference("managedRunId", command.ManagedRunID); err != nil {
		return MutationResult{}, mutationValidationFailure("task binding is invalid")
	}
	subjectDigest, err := digestMutationSubject(command)
	if err != nil {
		return MutationResult{}, mutationValidationFailure("activation subject cannot be encoded")
	}
	if replay, found, err := mutations.store.ReplayMutation(ctx, command.OperationID, commandActivateManagedRun, subjectDigest); err != nil {
		return MutationResult{}, mutationReplayFailure(err)
	} else if found {
		if err := mutations.bindRuntimeAttachment(ctx, command); err != nil {
			return MutationResult{}, err
		}
		return replay, nil
	}
	result, err := mutations.store.CommitManagedRunActivation(ctx, ManagedRunActivationMutation{
		ServiceInstanceID: command.ServiceInstanceID, ExternalRunRef: command.ExternalRunRef,
		RegistrationNonce: command.RegistrationNonce, Binding: binding,
		ExecutionAttachmentID: command.ExecutionAttachmentID, AttachmentTargetName: command.AttachmentTargetName,
		OperationID: command.OperationID, SubjectDigest: subjectDigest, At: mutations.clock(),
	})
	if err != nil {
		return result, mutationCommitFailure(err)
	}
	if err := mutations.bindRuntimeAttachment(ctx, command); err != nil {
		return MutationResult{}, err
	}
	return result, nil
}

func (mutations *Mutations) bindRuntimeAttachment(ctx context.Context, command ActivateManagedRunCommand) error {
	launchOperationID, err := RuntimeLaunchAcknowledgementOperationID(command.ExternalRunRef)
	if err != nil {
		return mutationValidationFailure("runtime launch acknowledgement identity is invalid")
	}
	err = mutations.attachments.BindRuntimeAttachment(ctx, RuntimeAttachmentBindingRequest{
		TaskHandle: command.ExternalRunRef, ManagedRunID: command.ManagedRunID,
		WorkspaceLeaseID: command.WorkspaceLeaseID, ExecutionAttachmentID: command.ExecutionAttachmentID,
		AttachmentTargetName: command.AttachmentTargetName,
		LaunchOperationID:    launchOperationID, Acknowledger: mutations,
	})
	if err != nil {
		return &dependencyFailure{message: "runtime attachment activation binding failed", cause: err}
	}
	return nil
}

// RuntimeLaunchAcknowledgementOperationID deterministically derives the sole
// wrapper acknowledgement operation for one service-owned task.
func RuntimeLaunchAcknowledgementOperationID(taskHandle string) (string, error) {
	if err := domain.ValidateTaskHandle(taskHandle); err != nil {
		return "", err
	}
	operationDigest := fmt.Sprintf("%x", sha256.Sum256([]byte("runtime-launch-ack\x00"+taskHandle)))
	return "launch-ack-" + operationDigest[:32], nil
}

// AbandonManagedRun durably closes one exact unbound preparation. Preserve
// retains prepared task state; reap-safe enters the reversible cleanup path.
func (mutations *Mutations) AbandonManagedRun(ctx context.Context, command AbandonManagedRunCommand) (MutationResult, error) {
	if err := validMutationContext(ctx); err != nil {
		return MutationResult{}, err
	}
	if err := domain.ValidateOperationID(command.OperationID); err != nil {
		return MutationResult{}, mutationValidationFailure("operation ID is invalid")
	}
	if err := domain.ValidateAuthorityReference("serviceInstanceId", command.ServiceInstanceID); err != nil {
		return MutationResult{}, mutationValidationFailure("service instance identity is invalid")
	}
	if err := domain.ValidateTaskHandle(command.ExternalRunRef); err != nil {
		return MutationResult{}, mutationValidationFailure("external run reference is invalid")
	}
	if !registrationNoncePattern.MatchString(command.RegistrationNonce) || !command.Reason.valid() || !command.Disposition.valid() {
		return MutationResult{}, mutationValidationFailure("abandonment fields are invalid")
	}
	subjectDigest, err := digestMutationSubject(command)
	if err != nil {
		return MutationResult{}, mutationValidationFailure("abandonment subject cannot be encoded")
	}
	if replay, found, err := mutations.store.ReplayMutation(ctx, command.OperationID, commandAbandonManagedRun, subjectDigest); err != nil {
		return MutationResult{}, mutationReplayFailure(err)
	} else if found {
		return replay, nil
	}
	result, err := mutations.store.CommitManagedRunAbandon(ctx, ManagedRunAbandonMutation{
		ServiceInstanceID: command.ServiceInstanceID, ExternalRunRef: command.ExternalRunRef,
		RegistrationNonce: command.RegistrationNonce, Reason: command.Reason,
		Disposition: command.Disposition, OperationID: command.OperationID,
		SubjectDigest: subjectDigest, At: mutations.clock(),
	})
	return result, mutationCommitFailure(err)
}

// StartTask durably records launch intent before any worker can acknowledge
// its wrapper or begin work.
func (mutations *Mutations) StartTask(ctx context.Context, command StartTaskCommand) (MutationResult, error) {
	if err := validMutationContext(ctx); err != nil {
		return MutationResult{}, err
	}
	if err := domain.ValidateOperationID(command.OperationID); err != nil {
		return MutationResult{}, mutationValidationFailure("operation ID is invalid")
	}
	if err := domain.ValidateTaskHandle(command.TaskHandle); err != nil {
		return MutationResult{}, mutationValidationFailure("task handle is invalid")
	}
	subjectDigest, err := digestMutationSubject(command)
	if err != nil {
		return MutationResult{}, mutationValidationFailure("start subject cannot be encoded")
	}
	if replay, found, err := mutations.store.ReplayMutation(ctx, command.OperationID, commandStartTask, subjectDigest); err != nil {
		return MutationResult{}, mutationReplayFailure(err)
	} else if found {
		return replay, nil
	}
	return mutations.store.CommitTaskStart(ctx, TaskStartMutation{
		TaskHandle: command.TaskHandle, OperationID: command.OperationID,
		SubjectDigest: subjectDigest, At: mutations.clock(),
	})
}

// RecordTerminalEvent validates and durably cross-binds one content-free Comis
// terminal transition. Running alone never acknowledges the worker wrapper.
func (mutations *Mutations) RecordTerminalEvent(ctx context.Context, command RecordTerminalEventCommand) (MutationResult, error) {
	if replay, found, err := mutations.ReconcileTerminalEvent(ctx, command); err != nil {
		return MutationResult{}, err
	} else if found {
		return replay, nil
	}
	subjectDigest, err := terminalEventSubjectDigest(command)
	if err != nil {
		return MutationResult{}, err
	}
	result, err := mutations.store.CommitTerminalEvent(ctx, TerminalEventMutation{
		OperationID: command.OperationID, SubjectDigest: subjectDigest,
		ManagedRunID: command.ManagedRunID, WorkspaceLeaseID: command.WorkspaceLeaseID,
		TerminalSessionID: command.TerminalSessionID, Transition: command.Transition, At: mutations.clock(),
	})
	return result, mutationCommitFailure(err)
}

// ReconcileTerminalEvent checks whether an authenticated terminal operation is
// already durable before a production launch supervisor performs prerequisite
// start work. Altered operation reuse returns the closed replay conflict.
func (mutations *Mutations) ReconcileTerminalEvent(ctx context.Context, command RecordTerminalEventCommand) (MutationResult, bool, error) {
	if err := validMutationContext(ctx); err != nil {
		return MutationResult{}, false, err
	}
	subjectDigest, err := terminalEventSubjectDigest(command)
	if err != nil {
		return MutationResult{}, false, err
	}
	replay, found, err := mutations.store.ReplayMutation(ctx, command.OperationID, commandRecordTerminalEvent, subjectDigest)
	if err != nil {
		return MutationResult{}, false, mutationReplayFailure(err)
	}
	return replay, found, nil
}

func terminalEventSubjectDigest(command RecordTerminalEventCommand) (string, error) {
	if err := domain.ValidateOperationID(command.OperationID); err != nil ||
		domain.ValidateAuthorityReference("managedRunId", command.ManagedRunID) != nil ||
		domain.ValidateAuthorityReference("workspaceLeaseId", command.WorkspaceLeaseID) != nil ||
		domain.ValidateAuthorityReference("terminalSessionId", command.TerminalSessionID) != nil || !command.Transition.Valid() {
		return "", mutationValidationFailure("terminal event fields are invalid")
	}
	subjectDigest, err := digestMutationSubject(command)
	if err != nil {
		return "", mutationValidationFailure("terminal event subject cannot be encoded")
	}
	return subjectDigest, nil
}

// AcknowledgeWorkerLaunch records the wrapper's exact protected-mount echo and
// advances to working only when terminal running evidence is also durable.
func (mutations *Mutations) AcknowledgeWorkerLaunch(ctx context.Context, command AcknowledgeWorkerLaunchCommand) (MutationResult, error) {
	if err := validMutationContext(ctx); err != nil {
		return MutationResult{}, err
	}
	if err := domain.ValidateOperationID(command.OperationID); err != nil || command.Acknowledgement.Validate() != nil {
		return MutationResult{}, mutationValidationFailure("worker launch acknowledgement is invalid")
	}
	subjectDigest, err := digestMutationSubject(command)
	if err != nil {
		return MutationResult{}, mutationValidationFailure("worker launch acknowledgement cannot be encoded")
	}
	if replay, found, err := mutations.store.ReplayMutation(ctx, command.OperationID, commandAcknowledgeWorkerLaunch, subjectDigest); err != nil {
		return MutationResult{}, mutationReplayFailure(err)
	} else if found {
		return replay, nil
	}
	result, err := mutations.store.CommitWorkerLaunchAcknowledgement(ctx, WorkerLaunchAcknowledgementMutation{
		OperationID: command.OperationID, SubjectDigest: subjectDigest,
		Acknowledgement: command.Acknowledgement, At: mutations.clock(),
	})
	return result, mutationCommitFailure(err)
}

func digestMutationSubject(subject any) (string, error) {
	encoded, err := json.Marshal(subject)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", sha256.Sum256(encoded)), nil
}

func validMutationContext(ctx context.Context) error {
	if ctx == nil {
		return errors.New("mutation context is required")
	}
	return ctx.Err()
}

func mutationValidationFailure(message string) error {
	failure, err := domain.NewFailure(domain.ErrorInvalidArgument, false, message, "correct the bounded command fields", nil)
	if err != nil {
		return errors.New("mutation validation failed")
	}
	return failure
}

func mutationReplayFailure(cause error) error {
	if !errors.Is(cause, ErrConflict) {
		return cause
	}
	failure, err := domain.NewFailure(
		domain.ErrorConflict, false,
		"stable operation subject conflicts with its original request",
		"reuse the original command or choose a new operation identity",
		cause,
	)
	if err != nil {
		return errors.New("mutation replay conflict")
	}
	return failure
}

func mutationCommitFailure(cause error) error {
	if cause == nil {
		return nil
	}
	var code domain.ErrorCode
	var message, hint string
	switch {
	case errors.Is(cause, ErrConflict):
		return mutationReplayFailure(cause)
	case errors.Is(cause, ErrInvalidInput):
		code, message, hint = domain.ErrorInvalidArgument,
			"mutation fields differ from durable task authority",
			"send the exact prepared workspace and bound run identities"
	case errors.Is(cause, ErrNotFound), errors.Is(cause, ErrPrecondition), errors.Is(cause, domain.ErrInvalidTransition):
		code, message, hint = domain.ErrorPrecondition,
			"task cannot accept the requested transition",
			"reconcile the exact durable task and run binding before retrying"
	default:
		return cause
	}
	failure, err := domain.NewFailure(code, false, message, hint, cause)
	if err != nil {
		return errors.New("mutation commit failure classification failed")
	}
	return failure
}

type dependencyFailure struct {
	message string
	cause   error
}

func (failure *dependencyFailure) Error() string { return failure.message }
func (failure *dependencyFailure) Unwrap() error { return failure.cause }

// SafeDependencyMessage returns the bounded application-owned stage without
// exposing the wrapped adapter error across a transport boundary.
func (failure *dependencyFailure) SafeDependencyMessage() string { return failure.message }
