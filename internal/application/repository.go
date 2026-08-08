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
)

// Repository is the consumer-owned durable port used by canonical handlers.
// Its mutations are wired only into the service-owned mutation path.
type Repository interface {
	CreateTask(context.Context, domain.Task) error
	RecordOperation(context.Context, domain.OperationRecord) error
	ListTasks(context.Context) ([]domain.Task, error)
	GetTask(context.Context, string) (domain.Task, error)
	GetOperation(context.Context, string) (domain.OperationRecord, error)
	CurrentStateVersion(context.Context) (int64, error)
}
