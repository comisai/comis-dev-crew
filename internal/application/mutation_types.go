package application

import (
	"context"
	"time"

	"github.com/comisai/comis-dev-crew/internal/domain"
)

const (
	commandPrepareTask        = "PrepareTask"
	commandActivateManagedRun = "ActivateManagedRun"
	commandAbandonManagedRun  = "AbandonManagedRun"
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

// ActivateManagedRunCommand supplies the complete private activation join and
// exact host-owned authority identities for one prepared task.
type ActivateManagedRunCommand struct {
	OperationID       string
	ServiceInstanceID string
	ManagedRunID      string
	ExternalRunRef    string
	RegistrationNonce string
	WorkspaceLeaseID  string
}

// AbandonManagedRunCommand closes one unbound private preparation without
// inferring any worker or host outcome.
type AbandonManagedRunCommand struct {
	OperationID       string
	ServiceInstanceID string
	ExternalRunRef    string
	RegistrationNonce string
	Reason            AbandonReason
	Disposition       AbandonDisposition
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

// ManagedRunActivationMutation is the fully validated activation transaction
// input. The store rechecks the private join and expiry under the write lock.
type ManagedRunActivationMutation struct {
	ServiceInstanceID string
	ExternalRunRef    string
	RegistrationNonce string
	Binding           domain.TaskBinding
	OperationID       string
	SubjectDigest     string
	At                time.Time
}

// ManagedRunAbandonMutation is the fully validated preparation-close
// transaction input.
type ManagedRunAbandonMutation struct {
	ServiceInstanceID string
	ExternalRunRef    string
	RegistrationNonce string
	Reason            AbandonReason
	Disposition       AbandonDisposition
	OperationID       string
	SubjectDigest     string
	At                time.Time
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
	CommitManagedRunActivation(context.Context, ManagedRunActivationMutation) (MutationResult, error)
	CommitManagedRunAbandon(context.Context, ManagedRunAbandonMutation) (MutationResult, error)
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
	ExternalRunRef         string             `json:"externalRunRef"`
	RegistrationNonce      string             `json:"registrationNonce"`
	RequestedWorkspaceRoot string             `json:"requestedWorkspaceRoot,omitempty"`
	ExpiresAt              time.Time          `json:"expiresAt"`
	State                  PreparationState   `json:"state"`
	AbandonReason          AbandonReason      `json:"abandonReason,omitempty"`
	Disposition            AbandonDisposition `json:"disposition,omitempty"`
	ClosedAt               *time.Time         `json:"closedAt,omitempty"`
}

// PreparationState is the closed private lifecycle for a two-phase join.
type PreparationState string

const (
	PreparationOpen      PreparationState = "open"
	PreparationAbandoned PreparationState = "abandoned"
)

// AbandonReason mirrors the closed protocol vocabulary without importing a
// transport package into the application layer.
type AbandonReason string

const (
	AbandonReasonActivationRejected  AbandonReason = "activation_rejected"
	AbandonReasonOwnerCancelled      AbandonReason = "owner_cancelled"
	AbandonReasonRegistrationExpired AbandonReason = "registration_expired"
	AbandonReasonServiceUnavailable  AbandonReason = "service_unavailable"
)

// AbandonDisposition selects the closed reversible preparation outcome.
type AbandonDisposition string

const (
	AbandonDispositionReapSafe AbandonDisposition = "reap_safe"
	AbandonDispositionPreserve AbandonDisposition = "preserve"
)

// MutationConfig supplies every deterministic dependency.
type MutationConfig struct {
	Store                  MutationStore
	Repositories           RepositoryCatalog
	TaskIDs                TaskIDSource
	RegistrationNonces     RegistrationNonceSource
	RequestedWorkspaceRoot string
	PreparationTTL         time.Duration
	Clock                  Clock
}
