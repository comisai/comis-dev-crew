package application

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"time"

	"github.com/comisai/comis-dev-crew/internal/domain"
)

// Mutations owns canonical E0 prepare and bind command construction.
type Mutations struct {
	store          MutationStore
	repositories   RepositoryCatalog
	taskIDs        TaskIDSource
	nonces         RegistrationNonceSource
	preparationTTL time.Duration
	clock          Clock
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
	return &Mutations{
		store: config.Store, repositories: config.Repositories,
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
		return MutationResult{}, err
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
		ExpiresAt: now.Add(mutations.preparationTTL).UTC(),
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
	return nil
}

// AcknowledgeBinding validates exact host references and delegates one atomic
// prepared-to-ready task/operation commit.
func (mutations *Mutations) AcknowledgeBinding(ctx context.Context, command AcknowledgeBindingCommand) (MutationResult, error) {
	if err := validMutationContext(ctx); err != nil {
		return MutationResult{}, err
	}
	if err := domain.ValidateOperationID(command.OperationID); err != nil {
		return MutationResult{}, mutationValidationFailure("operation ID is invalid")
	}
	if err := domain.ValidateTaskHandle(command.TaskHandle); err != nil {
		return MutationResult{}, mutationValidationFailure("task handle is invalid")
	}
	binding := domain.TaskBinding{ManagedRunID: command.ManagedRunID, WorkspaceLeaseID: command.WorkspaceLeaseID}
	if err := binding.Validate(); err != nil {
		return MutationResult{}, mutationValidationFailure("task binding is invalid")
	}
	subjectDigest, err := digestMutationSubject(command)
	if err != nil {
		return MutationResult{}, mutationValidationFailure("binding subject cannot be encoded")
	}
	if replay, found, err := mutations.store.ReplayMutation(ctx, command.OperationID, commandAcknowledgeBinding, subjectDigest); err != nil {
		return MutationResult{}, err
	} else if found {
		return replay, nil
	}
	return mutations.store.CommitTaskBinding(ctx, TaskBindingMutation{
		TaskHandle: command.TaskHandle, Binding: binding, OperationID: command.OperationID,
		SubjectDigest: subjectDigest, At: mutations.clock(),
	})
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
		return MutationResult{}, err
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

type dependencyFailure struct {
	message string
	cause   error
}

func (failure *dependencyFailure) Error() string { return failure.message }
func (failure *dependencyFailure) Unwrap() error { return failure.cause }
