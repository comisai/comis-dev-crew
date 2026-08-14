// Package application owns the use-case ports and canonical application services.
package application

import (
	"context"
	"errors"

	"github.com/comisai/comis-dev-crew/internal/domain"
)

var (
	// ErrNotFound means the requested durable record does not exist.
	ErrNotFound = errors.New("record not found")
	// ErrConflict means a stable record identity is already in use.
	ErrConflict = errors.New("record identity conflict")
	// ErrPrecondition means current durable state cannot authorize the request.
	ErrPrecondition = errors.New("durable precondition failed")
	// ErrInvalidInput means a state-dependent closed request invariant failed.
	ErrInvalidInput = errors.New("mutation input is invalid")
)

type cleanupBlockerError string

func (blocker cleanupBlockerError) Error() string { return string(blocker) }

func (cleanupBlockerError) Is(target error) bool { return target == ErrPrecondition }

// ErrCleanupOpenHold identifies the closed cleanup blocker without exposing
// the operator-authored hold reason across a transport boundary.
var ErrCleanupOpenHold error = cleanupBlockerError("cleanup hold remains open")

// ErrCleanupOpenDecision identifies an unresolved decision without exposing
// the decision prompt or response across an operator transport.
var ErrCleanupOpenDecision error = cleanupBlockerError("cleanup decision remains unresolved")

// ErrCleanupActiveExecution identifies positively active task execution or
// validation authority that must settle before cleanup.
var ErrCleanupActiveExecution error = cleanupBlockerError("cleanup execution remains active")

// ErrCleanupUnknownExecution identifies missing, lost, or mismatched terminal
// authority that cannot authorize destructive cleanup.
var ErrCleanupUnknownExecution error = cleanupBlockerError("cleanup execution authority is unknown")

// ErrCleanupStaleForgeTruth identifies current pull-request truth that no longer
// matches the exact delivered head and required checks.
var ErrCleanupStaleForgeTruth error = cleanupBlockerError("cleanup forge truth is stale")

// Repository is the consumer-owned durable port used by canonical handlers.
// Its mutations are wired only into the service-owned mutation path.
type Repository interface {
	CreateTask(context.Context, domain.Task) error
	RecordOperation(context.Context, domain.OperationRecord) error
	ListTasks(context.Context) ([]domain.Task, error)
	TaskSnapshot(context.Context) ([]domain.Task, int64, error)
	GetTask(context.Context, string) (domain.Task, error)
	GetManagedRunPreparation(context.Context, string) (ManagedRunPreparation, error)
	GetOperation(context.Context, string) (domain.OperationRecord, error)
	CurrentStateVersion(context.Context) (int64, error)
}
