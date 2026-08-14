package application

import (
	"context"
	"time"

	"github.com/comisai/comis-dev-crew/internal/domain"
)

const (
	commandPrepareTask             = "PrepareTask"
	commandActivateManagedRun      = "ActivateManagedRun"
	commandAbandonManagedRun       = "AbandonManagedRun"
	commandStartTask               = "StartTask"
	commandRecordTerminalEvent     = "RecordTerminalEvent"
	commandAcknowledgeWorkerLaunch = "AcknowledgeWorkerLaunch"
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
	OperationID           string
	ServiceInstanceID     string
	ManagedRunID          string
	ExternalRunRef        string
	RegistrationNonce     string
	WorkspaceLeaseID      string
	ExecutionAttachmentID string
	AttachmentTargetName  string
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

// TerminalTransition is the content-free Comis terminal lifecycle vocabulary.
type TerminalTransition string

const (
	TerminalCreated     TerminalTransition = "created"
	TerminalRunning     TerminalTransition = "running"
	TerminalInputNeeded TerminalTransition = "input_needed"
	TerminalStuck       TerminalTransition = "stuck"
	TerminalExited      TerminalTransition = "exited"
	TerminalLost        TerminalTransition = "lost"
	TerminalRecovered   TerminalTransition = "recovered"
	TerminalReleased    TerminalTransition = "released"
)

// Valid reports whether the transition belongs to the pinned closed vocabulary.
func (transition TerminalTransition) Valid() bool {
	switch transition {
	case TerminalCreated, TerminalRunning, TerminalInputNeeded, TerminalStuck,
		TerminalExited, TerminalLost, TerminalRecovered, TerminalReleased:
		return true
	default:
		return false
	}
}

// RecordTerminalEventCommand cross-binds one authenticated Comis event.
type RecordTerminalEventCommand struct {
	OperationID       string
	ManagedRunID      string
	WorkspaceLeaseID  string
	TerminalSessionID string
	Transition        TerminalTransition
}

// AcknowledgeWorkerLaunchCommand carries the wrapper's exact protected-mount
// launch echo. Its operation identity is service-owned, not worker-selected.
type AcknowledgeWorkerLaunchCommand struct {
	OperationID     string
	Acknowledgement LaunchAcknowledgement
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
	ServiceInstanceID     string
	ExternalRunRef        string
	RegistrationNonce     string
	Binding               domain.TaskBinding
	ExecutionAttachmentID string
	AttachmentTargetName  string
	OperationID           string
	SubjectDigest         string
	At                    time.Time
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

// TerminalEventMutation is the validated durable terminal-event transaction.
type TerminalEventMutation struct {
	OperationID       string
	SubjectDigest     string
	ManagedRunID      string
	WorkspaceLeaseID  string
	TerminalSessionID string
	Transition        TerminalTransition
	At                time.Time
}

// WorkerLaunchAcknowledgementMutation is the validated protected wrapper echo.
type WorkerLaunchAcknowledgementMutation struct {
	OperationID     string
	SubjectDigest   string
	Acknowledgement LaunchAcknowledgement
	At              time.Time
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
	CommitTerminalEvent(context.Context, TerminalEventMutation) (MutationResult, error)
	CommitWorkerLaunchAcknowledgement(context.Context, WorkerLaunchAcknowledgementMutation) (MutationResult, error)
}

// RepositoryCatalog validates an operator-configured repository ID without
// exposing adapter paths to the application layer.
type RepositoryCatalog interface {
	ValidateRepository(context.Context, string) error
}

// WorkerProfileValidator validates an exact operator-reviewed worker profile
// and task shape before any workspace side effect.
type WorkerProfileValidator func(string, domain.TaskShape) error

// ValidationProfileValidator validates an exact operator-reviewed validation
// profile before any workspace side effect.
type ValidationProfileValidator func(string) error

// WorkspacePreparationRequest binds allocation to the stable preparation
// operation and the already-validated immutable task contract.
type WorkspacePreparationRequest struct {
	OperationID  string
	TaskHandle   string
	RepositoryID string
	BaseRevision string
}

// PreparedWorkspace is the verified canonical root that Comis must lease.
type PreparedWorkspace struct {
	CanonicalRoot string
}

// WorkspacePreparer allocates or exactly adopts the operation-bound workspace.
type WorkspacePreparer interface {
	PrepareWorkspace(context.Context, WorkspacePreparationRequest) (PreparedWorkspace, error)
}

// RuntimeAttachmentKind is the closed source kind DevCrew can declare during
// managed-run preparation.
type RuntimeAttachmentKind string

const (
	// RuntimeAttachmentUnixSocket declares one owner-only task reporter socket.
	RuntimeAttachmentUnixSocket RuntimeAttachmentKind = "unix_socket"
)

// PreparedRuntimeAttachment is the exact source Comis must validate and bind.
type PreparedRuntimeAttachment struct {
	Kind       RuntimeAttachmentKind `json:"kind"`
	SourcePath string                `json:"sourcePath"`
}

// RuntimeAttachmentPreparationRequest binds the socket to the immutable brief
// and operation-selected workspace before any registration nonce is minted.
type RuntimeAttachmentPreparationRequest struct {
	OperationID       string
	TaskHandle        string
	BriefRevision     int64
	BriefRevisionHash string
	Brief             domain.WorkerBrief
	WorkingDirectory  string
}

// RuntimeAttachmentBindingRequest carries only activation-returned authority
// and the service-selected durable wrapper acknowledgement operation.
type RuntimeAttachmentBindingRequest struct {
	TaskHandle            string
	ManagedRunID          string
	WorkspaceLeaseID      string
	ExecutionAttachmentID string
	AttachmentTargetName  string
	LaunchOperationID     string
	Acknowledger          WorkerLaunchAcknowledger
}

// RuntimeAttachmentCoordinator owns per-task reporter listeners and binds the
// activation identity to the same protected socket without replacing it.
type RuntimeAttachmentCoordinator interface {
	PrepareRuntimeAttachment(context.Context, RuntimeAttachmentPreparationRequest) (PreparedRuntimeAttachment, error)
	BindRuntimeAttachment(context.Context, RuntimeAttachmentBindingRequest) error
}

// TaskIDSource deterministically derives one opaque service-local task handle
// from a stable preparation operation.
type TaskIDSource func(string) (string, error)

// RegistrationNonceSource mints one private expiring activation join secret.
type RegistrationNonceSource func() (string, error)

// ManagedRunPreparation is the durable service-owned half of the two-phase
// Comis activation join. It is private adapter metadata, not model authority.
type ManagedRunPreparation struct {
	ExternalRunRef         string                    `json:"externalRunRef"`
	RegistrationNonce      string                    `json:"registrationNonce"`
	RequestedWorkspaceRoot string                    `json:"requestedWorkspaceRoot,omitempty"`
	RequestedAttachment    PreparedRuntimeAttachment `json:"requestedAttachment"`
	ExpiresAt              time.Time                 `json:"expiresAt"`
	State                  PreparationState          `json:"state"`
	AbandonReason          AbandonReason             `json:"abandonReason,omitempty"`
	Disposition            AbandonDisposition        `json:"disposition,omitempty"`
	ClosedAt               *time.Time                `json:"closedAt,omitempty"`
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
	Store              MutationStore
	Repositories       RepositoryCatalog
	WorkerProfiles     WorkerProfileValidator
	ValidationProfiles ValidationProfileValidator
	Workspaces         WorkspacePreparer
	RuntimeAttachments RuntimeAttachmentCoordinator
	TaskIDs            TaskIDSource
	RegistrationNonces RegistrationNonceSource
	PreparationTTL     time.Duration
	Clock              Clock
}
