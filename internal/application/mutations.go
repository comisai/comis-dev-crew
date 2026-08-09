package application

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"time"

	"github.com/comisai/comis-dev-crew/internal/domain"
)

// Mutations owns canonical E0 prepare and bind command construction.
type Mutations struct {
	store                  MutationStore
	repositories           RepositoryCatalog
	taskIDs                TaskIDSource
	nonces                 RegistrationNonceSource
	preparationTTL         time.Duration
	requestedWorkspaceRoot string
	clock                  Clock
}

var registrationNoncePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._~-]{15,255}$`)

// NewMutations creates the sole application mutation coordinator.
func NewMutations(config MutationConfig) (*Mutations, error) {
	if config.Store == nil || config.Repositories == nil || config.TaskIDs == nil ||
		config.RegistrationNonces == nil || config.Clock == nil {
		return nil, errors.New("create mutations: store, repositories, task IDs, registration nonces, and clock are required")
	}
	if config.PreparationTTL <= 0 || config.PreparationTTL > 24*time.Hour {
		return nil, errors.New("create mutations: preparation TTL must be within 24 hours")
	}
	if config.RequestedWorkspaceRoot != "" &&
		(!filepath.IsAbs(config.RequestedWorkspaceRoot) || filepath.Clean(config.RequestedWorkspaceRoot) != config.RequestedWorkspaceRoot) {
		return nil, errors.New("create mutations: requested workspace root must be absolute and canonical")
	}
	return &Mutations{
		store: config.Store, repositories: config.Repositories,
		taskIDs: config.TaskIDs, nonces: config.RegistrationNonces,
		preparationTTL: config.PreparationTTL, requestedWorkspaceRoot: config.RequestedWorkspaceRoot,
		clock: config.Clock,
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
	taskHandle, err := mutations.taskIDs()
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
	nonce, err := mutations.nonces()
	if err != nil {
		return MutationResult{}, &dependencyFailure{message: "registration identity source failed", cause: err}
	}
	preparation := ManagedRunPreparation{
		ExternalRunRef: task.Handle, RegistrationNonce: nonce,
		RequestedWorkspaceRoot: mutations.requestedWorkspaceRoot,
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
		return replay, nil
	}
	result, err := mutations.store.CommitManagedRunActivation(ctx, ManagedRunActivationMutation{
		ServiceInstanceID: command.ServiceInstanceID, ExternalRunRef: command.ExternalRunRef,
		RegistrationNonce: command.RegistrationNonce, Binding: binding,
		OperationID: command.OperationID, SubjectDigest: subjectDigest, At: mutations.clock(),
	})
	return result, mutationCommitFailure(err)
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

// StartTask durably records launch intent before any fixture worker can emit a
// report or begin work.
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
			"activation fields differ from the prepared workspace request",
			"send the lease exactly when the preparation requested a workspace"
	case errors.Is(cause, ErrNotFound), errors.Is(cause, ErrPrecondition), errors.Is(cause, domain.ErrInvalidTransition):
		code, message, hint = domain.ErrorPrecondition,
			"managed-run preparation cannot accept this transition",
			"reconcile the exact private preparation before retrying"
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
