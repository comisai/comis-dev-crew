package application

import (
	"context"
	"errors"
	"path/filepath"
	"time"

	"github.com/comisai/comis-dev-crew/internal/domain"
)

// HarnessAvailability is the closed result of an exact adapter probe.
type HarnessAvailability string

const (
	HarnessAvailable   HarnessAvailability = "available"
	HarnessUnavailable HarnessAvailability = "unavailable"
	HarnessUnknown     HarnessAvailability = "unknown"
)

// HarnessReason is the closed explanation for degraded or unavailable state.
type HarnessReason string

const (
	HarnessReasonExecutableUnavailable  HarnessReason = "executable_unavailable"
	HarnessReasonVersionMismatch        HarnessReason = "version_mismatch"
	HarnessReasonCapabilityUnknown      HarnessReason = "capability_unknown"
	HarnessReasonLifecycleSignalUnknown HarnessReason = "lifecycle_signal_unknown"
)

// HarnessVersionProbe records the exact version and availability of one
// operator-selected harness installation.
type HarnessVersionProbe struct {
	Version      string
	Availability HarnessAvailability
	Reason       HarnessReason
}

// RuntimeSocketAttachment is activation-returned Comis attachment authority
// resolved to its exact protected worker-visible mount. No host source enters
// the launch descriptor.
type RuntimeSocketAttachment struct {
	ExecutionAttachmentID string
	AttachmentTargetName  string
	MountSocketPath       string
}

// WorkerLaunchRequest contains the exact launch binding. Authority fields are
// used only for expected acknowledgement and must not enter process argv.
type WorkerLaunchRequest struct {
	ProfileID         string
	Shape             domain.TaskShape
	WorkingDirectory  string
	TaskHandle        string
	ManagedRunID      string
	WorkspaceLeaseID  string
	BriefRevision     int64
	BriefRevisionHash string
	Attachment        RuntimeSocketAttachment
}

// LaunchAcknowledgement is the complete terminal fact required before a task
// may move from launching to working.
type LaunchAcknowledgement struct {
	TaskHandle        string `json:"taskHandle"`
	ManagedRunID      string `json:"managedRunId"`
	WorkspaceLeaseID  string `json:"workspaceLeaseId"`
	WorkingDirectory  string `json:"workingDirectory"`
	BriefRevision     int64  `json:"briefRevision"`
	BriefRevisionHash string `json:"briefRevisionHash"`
}

// Validate rejects any partial or non-canonical launch echo.
func (acknowledgement LaunchAcknowledgement) Validate() error {
	if err := domain.ValidateTaskHandle(acknowledgement.TaskHandle); err != nil {
		return errors.New("launch acknowledgement task is invalid")
	}
	if err := domain.ValidateAuthorityReference("managedRunId", acknowledgement.ManagedRunID); err != nil {
		return errors.New("launch acknowledgement managed run is invalid")
	}
	if err := domain.ValidateAuthorityReference("workspaceLeaseId", acknowledgement.WorkspaceLeaseID); err != nil {
		return errors.New("launch acknowledgement workspace lease is invalid")
	}
	if !filepath.IsAbs(acknowledgement.WorkingDirectory) || filepath.Clean(acknowledgement.WorkingDirectory) != acknowledgement.WorkingDirectory {
		return errors.New("launch acknowledgement working directory is invalid")
	}
	if acknowledgement.BriefRevision < 1 || domain.ValidateBriefRevisionHash(acknowledgement.BriefRevisionHash) != nil {
		return errors.New("launch acknowledgement brief pin is invalid")
	}
	return nil
}

// WorkerLaunchAcknowledger is the application-owned protected wrapper sink.
// Implementations durably deduplicate the service-selected operation ID.
type WorkerLaunchAcknowledger interface {
	AcknowledgeWorkerLaunch(context.Context, AcknowledgeWorkerLaunchCommand) (MutationResult, error)
}

// WorkerLaunchDescriptor is a no-shell executable/argv contract plus protected
// runtime attachment and generic stdin bootstrap.
type WorkerLaunchDescriptor struct {
	ProfileID               string
	Harness                 string
	Executable              string
	Arguments               []string
	WorkingDirectory        string
	EnvironmentKeys         []string
	EnvironmentBindings     map[string]string
	Model                   string
	Effort                  string
	TerminalAllowEntry      string
	Network                 string
	ConcurrencyLimit        int
	Unattended              bool
	DegradedReason          HarnessReason
	Attachment              RuntimeSocketAttachment
	StandardInput           []byte
	ExpectedAcknowledgement LaunchAcknowledgement
}

// ProcessObservation is a task-correlated process fact.
type ProcessObservation string

const (
	ProcessAlive   ProcessObservation = "alive"
	ProcessExited  ProcessObservation = "exited"
	ProcessUnknown ProcessObservation = "unknown"
)

// HarnessObservation is the strongest current Codex JSONL event plus process
// and freshness facts. Missing or conflicting fields fail to unknown.
type HarnessObservation struct {
	EventJSON    []byte
	Process      ProcessObservation
	ObservedAt   time.Time
	Now          time.Time
	FreshnessTTL time.Duration
}

// SemanticActivity is the normalized, non-success lifecycle observation.
type SemanticActivity string

const (
	ActivityBusy          SemanticActivity = "busy"
	ActivityAwaitingInput SemanticActivity = "awaiting_input"
	ActivityIdle          SemanticActivity = "idle"
	ActivityExited        SemanticActivity = "exited"
	ActivityUnknown       SemanticActivity = "unknown"
)

// SemanticReason explains fail-closed unknown activity.
type SemanticReason string

const (
	SemanticReasonMissing              SemanticReason = "missing"
	SemanticReasonStale                SemanticReason = "stale"
	SemanticReasonMalformed            SemanticReason = "malformed"
	SemanticReasonUnsupported          SemanticReason = "unsupported"
	SemanticReasonSettledWithoutReport SemanticReason = "settled_without_report"
)

// SemanticActivityResult is an observed state, never a completion claim.
type SemanticActivityResult struct {
	State  SemanticActivity
	Reason SemanticReason
}

// WorkerHarnessAdapter is the application-owned essential adapter contract.
type WorkerHarnessAdapter interface {
	ID() string
	ProbeVersion(context.Context) (HarnessVersionProbe, error)
	BuildLaunchDescriptor(context.Context, WorkerLaunchRequest) (WorkerLaunchDescriptor, error)
	ClassifySemanticActivity(HarnessObservation) SemanticActivityResult
}

// WorkerHarnessResolver returns only the exact operator-reviewed adapter for a
// selected profile. It never ranks profiles or falls back to another harness.
type WorkerHarnessResolver interface {
	ResolveWorkerHarness(string) (WorkerHarnessAdapter, error)
}
