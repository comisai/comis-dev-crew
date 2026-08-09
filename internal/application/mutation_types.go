package application

import (
	"context"
	"time"

	"github.com/comisai/comis-dev-crew/internal/domain"
)

const (
	commandPrepareTask        = "PrepareTask"
	commandAcknowledgeBinding = "AcknowledgeBinding"
	commandStartTask          = "StartTask"
)

// PrepareTaskCommand contains immutable E0 contract fields. The service mints
// the task handle and computes the replay digest.
type PrepareTaskCommand struct {
	OperationID        string
	ServiceInstanceID  string
	Shape              domain.TaskShape
	RepositoryID       string
	BaseRevision       string
	AcceptanceCriteria []string
	Constraints        []string
	ValidationProfile  string
	DeliveryMode       domain.DeliveryMode
	WorkerProfileID    string
}

// AcknowledgeBindingCommand supplies exact host-owned authority identities for
// one prepared task.
type AcknowledgeBindingCommand struct {
	OperationID      string
	TaskHandle       string
	ManagedRunID     string
	WorkspaceLeaseID string
}

// StartTaskCommand requests the sole durable ready-to-launching transition.
type StartTaskCommand struct {
	OperationID string
	TaskHandle  string
}

// PreparedTaskMutation is the fully validated store transaction input.
type PreparedTaskMutation struct {
	Task          domain.Task
	Preparation   ManagedRunPreparation
	OperationID   string
	SubjectDigest string
	At            time.Time
}

// TaskBindingMutation is the fully validated bind transaction input.
type TaskBindingMutation struct {
	TaskHandle    string
	Binding       domain.TaskBinding
	OperationID   string
	SubjectDigest string
	At            time.Time
}

// TaskStartMutation is the fully validated launch-intent transaction input.
type TaskStartMutation struct {
	TaskHandle    string
	OperationID   string
	SubjectDigest string
	At            time.Time
}

// MutationResult joins one canonical task state and its replay outcome at the
// same durable state version.
type MutationResult struct {
	Task        domain.Task
	Operation   domain.OperationRecord
	Preparation *ManagedRunPreparation
}

// MutationStore is the service-owned transactional mutation port.
type MutationStore interface {
	ReplayMutation(context.Context, string, string, string) (MutationResult, bool, error)
	CommitPreparedTask(context.Context, PreparedTaskMutation) (MutationResult, error)
	CommitTaskBinding(context.Context, TaskBindingMutation) (MutationResult, error)
	CommitTaskStart(context.Context, TaskStartMutation) (MutationResult, error)
}

// RepositoryCatalog validates an operator-configured repository ID without
// exposing adapter paths to the application layer.
type RepositoryCatalog interface {
	ValidateRepository(context.Context, string) error
}

// TaskIDSource mints one opaque service-local task handle.
type TaskIDSource func() (string, error)

// RegistrationNonceSource mints one private expiring activation join secret.
type RegistrationNonceSource func() (string, error)

// ManagedRunPreparation is the durable service-owned half of the two-phase
// Comis activation join. It is private adapter metadata, not model authority.
type ManagedRunPreparation struct {
	ExternalRunRef    string    `json:"externalRunRef"`
	RegistrationNonce string    `json:"registrationNonce"`
	ExpiresAt         time.Time `json:"expiresAt"`
}

// MutationConfig supplies every deterministic dependency.
type MutationConfig struct {
	Store              MutationStore
	Repositories       RepositoryCatalog
	TaskIDs            TaskIDSource
	RegistrationNonces RegistrationNonceSource
	PreparationTTL     time.Duration
	Clock              Clock
}
