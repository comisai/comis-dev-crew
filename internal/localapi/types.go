// Package localapi implements the canonical owner-only local service boundary.
package localapi

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/comisai/comis-dev-crew/internal/application"
	"github.com/comisai/comis-dev-crew/internal/domain"
)

const (
	// ProtocolVersion identifies the strict local service contract.
	ProtocolVersion = "devcrew.local.v1"
	// MaxRequestBytes bounds one newline-delimited request before decoding.
	MaxRequestBytes = 64 * 1024
	// MaxResponseBytes bounds one newline-delimited response before decoding.
	MaxResponseBytes = 1024 * 1024
)

// CallerClass is derived from the protected endpoint, never from request content.
type CallerClass string

const (
	CallerOperatorCLI  CallerClass = "operator_cli"
	CallerMCPFacade    CallerClass = "mcp_facade"
	CallerWorkerReport CallerClass = "worker_report"
	CallerComisControl CallerClass = "comis_control"
)

func (caller CallerClass) valid() bool {
	switch caller {
	case CallerOperatorCLI, CallerMCPFacade, CallerWorkerReport, CallerComisControl:
		return true
	default:
		return false
	}
}

// Method is the closed canonical local command and query catalog.
type Method string

const (
	MethodDiagnose       Method = "Diagnose"
	MethodFleet          Method = "FleetStatus"
	MethodListTasks      Method = "ListTasks"
	MethodWorkerProfiles Method = "ListWorkerProfiles"
	MethodShowTask       Method = "ShowTask"
	MethodExplainTask    Method = "ExplainTask"
	MethodGetLaunchPlan  Method = "GetLaunchPlan"
	MethodOperation      Method = "GetOperation"
	MethodPrepareTask    Method = "PrepareTask"
	MethodReconcileTask  Method = "ReconcileTask"
	MethodHandbackTask   Method = "HandbackTask"
	MethodCleanupTask    Method = "CleanupTask"
	MethodPauseTask      Method = "PauseTask"
	MethodCancelTask     Method = "CancelTask"
	MethodResumeTask     Method = "ResumeTask"
	MethodVerifyTask     Method = "VerifyTask"
	MethodPromoteScout   Method = "PromoteScout"
	MethodReplaceWorker  Method = "ReplaceWorker"
	MethodSteerTask      Method = "SteerTask"
	MethodDiscardTask    Method = "DiscardTask"
	MethodSyncPrimary    Method = "SyncPrimary"
	MethodAttestScout    Method = "AttestScoutDecisions"
	MethodListDecisions  Method = "ListTaskDecisions"
	MethodShowDecision   Method = "ShowTaskDecision"
	MethodDiffTask       Method = "DiffTask"
	MethodSurveyRepairs  Method = "SurveyRepairs"
)

func (method Method) valid() bool {
	switch method {
	case MethodDiagnose, MethodFleet, MethodListTasks, MethodWorkerProfiles, MethodShowTask, MethodExplainTask, MethodGetLaunchPlan,
		MethodOperation, MethodPrepareTask, MethodReconcileTask, MethodHandbackTask, MethodCleanupTask,
		MethodPauseTask, MethodCancelTask, MethodResumeTask, MethodVerifyTask, MethodPromoteScout, MethodReplaceWorker, MethodSteerTask, MethodDiscardTask,
		MethodSyncPrimary, MethodAttestScout, MethodListDecisions, MethodShowDecision, MethodDiffTask, MethodSurveyRepairs:
		return true
	default:
		return false
	}
}

// SideEffectClass is the closed parity classification shared by local clients
// and adapter metadata.
type SideEffectClass string

const (
	SideEffectRead   SideEffectClass = "read"
	SideEffectMutate SideEffectClass = "mutate"
)

// SideEffect returns the authoritative method classification.
func (method Method) SideEffect() SideEffectClass {
	switch method {
	case MethodPrepareTask, MethodReconcileTask, MethodHandbackTask, MethodCleanupTask,
		MethodPauseTask, MethodCancelTask, MethodResumeTask, MethodVerifyTask, MethodPromoteScout, MethodReplaceWorker, MethodSteerTask, MethodDiscardTask,
		MethodSyncPrimary, MethodAttestScout:
		return SideEffectMutate
	default:
		return SideEffectRead
	}
}

// ListDecisionsInput scopes the open-decision inventory. An absent task handle
// reads the whole fleet.
type ListDecisionsInput struct {
	TaskHandle string `json:"taskHandle,omitempty"`
}

// SurveyRepairsInput scopes the repair survey. An absent task handle surveys the
// whole fleet.
type SurveyRepairsInput struct {
	TaskHandle string `json:"taskHandle,omitempty"`
}

// ShowDecisionInput names exactly one keyed decision.
type ShowDecisionInput struct {
	TaskHandle  string `json:"taskHandle"`
	ExternalKey string `json:"externalKey"`
}

// Request is one strict newline-delimited local service request.
type Request struct {
	ProtocolVersion string          `json:"protocolVersion"`
	OperationID     string          `json:"operationId"`
	Method          Method          `json:"method"`
	Payload         json.RawMessage `json:"payload"`
	DeadlineAtMs    *int64          `json:"deadlineAtMs,omitempty"`
}

// WireError is safe to return to a local client.
type WireError struct {
	Code      domain.ErrorCode `json:"code"`
	Message   string           `json:"message"`
	Retryable bool             `json:"retryable"`
	Hint      string           `json:"hint"`
}

// Outcome is one strict local service result.
type Outcome struct {
	ProtocolVersion string                 `json:"protocolVersion"`
	OperationID     string                 `json:"operationId"`
	Status          domain.OperationStatus `json:"status"`
	StateVersion    *int64                 `json:"stateVersion,omitempty"`
	Result          json.RawMessage        `json:"result,omitempty"`
	Error           *WireError             `json:"error,omitempty"`
}

// operatorOnly reports whether a method carries private task detail that §20.3
// keeps off the model surface.
//
// The boundary lives here rather than only in the set of tools the facade
// exposes, so a facade that later grows a tool cannot thereby gain an authority
// the operator console was meant to hold alone.
func (method Method) operatorOnly() bool {
	switch method {
	case MethodListDecisions, MethodShowDecision, MethodDiffTask, MethodSurveyRepairs:
		return true
	default:
		return false
	}
}

// ReadQueries is the narrow application surface consumed by the local boundary.
type ReadQueries interface {
	DiffTask(context.Context, string) (application.TaskDiffView, error)
	SurveyRepairs(context.Context, string) (application.RepairSurvey, error)
	ListDecisions(context.Context, string) (application.DecisionList, error)
	ShowDecision(context.Context, string, string) (application.TaskDecision, error)
	Diagnose(context.Context) (application.DiagnosticReport, error)
	Fleet(context.Context) (application.FleetSnapshot, error)
	ListTasks(context.Context) (application.TaskList, error)
	ListWorkerProfiles(context.Context) (application.WorkerProfileList, error)
	ShowTask(context.Context, string) (application.TaskDetail, error)
	ExplainTask(context.Context, string) (application.TaskExplanation, error)
	GetLaunchPlan(context.Context, string) (application.LaunchPlan, error)
	Operation(context.Context, string) (application.OperationView, error)
}

// TaskMutations is the sole canonical mutation surface used by the local API.
type TaskMutations interface {
	PrepareTask(context.Context, application.PrepareTaskCommand) (application.MutationResult, error)
	PauseTask(context.Context, application.PauseTaskCommand) (application.MutationResult, error)
	VerifyTask(context.Context, application.VerifyTaskCommand) (application.MutationResult, error)
	SteerTask(context.Context, application.SteerTaskCommand) (application.MutationResult, error)
	PromoteScout(context.Context, application.PromoteScoutCommand) (application.MutationResult, error)
	CancelTask(context.Context, application.CancelTaskCommand) (application.MutationResult, error)
}

// TaskInterventions is the canonical paused-worktree handback surface.
type TaskInterventions interface {
	ResumeTask(context.Context, application.ResumeTaskCommand) (application.MutationResult, error)
	ReplaceWorker(context.Context, application.ReplaceWorkerCommand) (application.MutationResult, error)
	HandbackTask(context.Context, application.HandbackTaskCommand) (application.MutationResult, error)
}

// TaskReconciliation is the canonical unknown-task recovery surface.
type TaskReconciliation interface {
	ReconcileTask(context.Context, application.ReconcileTaskCommand) (application.MutationResult, error)
}

// TaskCleanup is the canonical release-before-removal mutation surface.
type TaskCleanup interface {
	CleanupTask(context.Context, application.CleanupTaskCommand) (application.MutationResult, error)
	DiscardTask(context.Context, application.DiscardTaskCommand) (application.MutationResult, error)
}

// ScoutReviewAttestation is the canonical review-completion surface.
type ScoutReviewAttestation interface {
	AttestScoutDecisions(context.Context, application.AttestScoutDecisionsCommand) (application.MutationResult, error)
}

// PrimaryCheckoutSync is the canonical repository synchronization surface. It
// is separate from the task surfaces because it moves no task: it advances the
// developer's own checkout and touches no durable task state.
type PrimaryCheckoutSync interface {
	SyncPrimary(context.Context, application.PrimarySyncCommand) (application.PrimarySyncReport, error)
}

// HandlerConfig binds local endpoint authority to canonical application seams.
type HandlerConfig struct {
	Queries           ReadQueries
	Mutations         TaskMutations
	Reconciliation    TaskReconciliation
	Interventions     TaskInterventions
	Cleanup           TaskCleanup
	PrimaryCheckouts  PrimaryCheckoutSync
	ScoutReviews      ScoutReviewAttestation
	ServiceInstanceID string
	Clock             application.Clock
}

// HandbackTaskInput selects one paused task and closed E0 action.
type HandbackTaskInput struct {
	TaskHandle string                     `json:"taskHandle"`
	Action     application.HandbackAction `json:"action"`
}

// ReconcileTaskInput selects one unknown task and the closed clean-candidate
// validation action. Filesystem and execution authority are intentionally absent.
type ReconcileTaskInput struct {
	TaskHandle string                          `json:"taskHandle"`
	Action     application.ReconcileTaskAction `json:"action"`
}

// PauseTaskInput selects one task whose worker should reach a safe boundary. It
// carries no instruction and no interrupt: pause asks the worker to stop
// cleanly, and anything more is a different command.
type PauseTaskInput = taskHandleMutationInput

// DiscardTaskInput removes the worktree of one task that never delivered. The
// acknowledgement is the only gate such a removal has, so it travels explicitly
// rather than being implied by calling the method at all.
type DiscardTaskInput struct {
	TaskHandle   string `json:"taskHandle"`
	Acknowledged bool   `json:"acknowledged"`
}

// SyncPrimaryInput names the configured repository whose primary checkout is
// synchronized. It carries no path: the checkout comes from operator
// configuration, so a caller cannot aim the update at another tree.
type SyncPrimaryInput struct {
	RepositoryID string `json:"repositoryId"`
}

// AttestScoutDecisionsInput records one liaison inventory of a scout's still
// open human decisions. The finding is stated rather than inferred from an
// empty list, so "nothing was open" can never be reached by omission.
type AttestScoutDecisionsInput struct {
	TaskHandle       string                              `json:"taskHandle"`
	Finding          application.ScoutAttestationFinding `json:"finding"`
	OpenDecisionKeys []string                            `json:"openDecisionKeys"`
}

// SteerTaskInput carries one bounded instruction for a task's current worker.
type SteerTaskInput struct {
	TaskHandle  string `json:"taskHandle"`
	Instruction string `json:"instruction"`
}

// ReplaceWorkerInput names the task and the reviewed profile that takes over.
// It carries no disposition: replacement preserves the work, and stopping or
// discarding it are separate commands that say so plainly.
type ReplaceWorkerInput struct {
	TaskHandle      string `json:"taskHandle"`
	WorkerProfileID string `json:"workerProfileId"`
}

// VerifyTaskInput selects one task to validate now. It selects no profile:
// validation runs the reviewed profile the task was prepared with.
type VerifyTaskInput = taskHandleMutationInput

// ResumeTaskInput selects one paused task to continue. It names no worker:
// resume continues what was already running, and choosing a different worker is
// replacement.
type ResumeTaskInput = taskHandleMutationInput

// CancelTaskInput selects one task to stop. It names no disposition: cancel
// preserves artifacts, and removing them is cleanup's separate, evidence-gated
// decision.
type CancelTaskInput = taskHandleMutationInput

// CleanupTaskInput selects one durably delivered task.
type CleanupTaskInput struct {
	TaskHandle string `json:"taskHandle"`
}

// TaskMutationResult is the common public projection for post-prepare task mutations.
type TaskMutationResult struct {
	SchemaVersion int              `json:"schemaVersion"`
	OperationID   string           `json:"operationId"`
	TaskHandle    string           `json:"taskHandle"`
	State         domain.TaskState `json:"state"`
	StateVersion  int64            `json:"stateVersion"`
	SideEffect    SideEffectClass  `json:"sideEffect"`
}

// PrepareTaskInput is the strict public task contract. Service identity and
// operation identity are derived from the endpoint and request envelope.
type PrepareTaskInput struct {
	Shape              domain.TaskShape    `json:"shape"`
	RepositoryID       string              `json:"repositoryId"`
	BaseRevision       string              `json:"baseRevision"`
	AcceptanceCriteria []string            `json:"acceptanceCriteria"`
	Constraints        []string            `json:"constraints"`
	ValidationProfile  string              `json:"validationProfile"`
	DeliveryMode       domain.DeliveryMode `json:"deliveryMode"`
	WorkerProfileID    string              `json:"workerProfileId"`
}

// PromoteScoutInput names the scout and the ship contract. It carries no
// repository or base revision: both are inherited from the scout, so a promotion
// cannot point the ship task at different ground than the investigation covered
// while still carrying that investigation as its justification.
type PromoteScoutInput struct {
	ScoutTaskHandle    string              `json:"scoutTaskHandle"`
	AcceptanceCriteria []string            `json:"acceptanceCriteria"`
	Constraints        []string            `json:"constraints"`
	ValidationProfile  string              `json:"validationProfile"`
	DeliveryMode       domain.DeliveryMode `json:"deliveryMode"`
	WorkerProfileID    string              `json:"workerProfileId"`
}

// PrepareTaskResult is the stable local mutation projection. ManagedRun stays
// private when an MCP adapter renders the model-visible result.
type PrepareTaskResult struct {
	SchemaVersion int                               `json:"schemaVersion"`
	OperationID   string                            `json:"operationId"`
	TaskHandle    string                            `json:"taskHandle"`
	State         domain.TaskState                  `json:"state"`
	StateVersion  int64                             `json:"stateVersion"`
	SideEffect    SideEffectClass                   `json:"sideEffect"`
	ManagedRun    application.ManagedRunPreparation `json:"managedRun"`
}

// DecodePrepareTaskInput applies the canonical strict local payload decoder.
func DecodePrepareTaskInput(data []byte) (PrepareTaskInput, error) {
	var input PrepareTaskInput
	if len(data) == 0 || len(data) > MaxRequestBytes {
		return PrepareTaskInput{}, errors.New("prepare task input exceeds its bound")
	}
	if err := decodeObject(data, &input); err != nil {
		return PrepareTaskInput{}, err
	}
	return input, nil
}

// DecodePromoteScoutInput reads one strict bounded promotion contract. The
// scout handle travels on the command line and the contract carries only what
// the ship revision must achieve, so a contract naming a different scout than
// the operator typed cannot be accepted.
func DecodePromoteScoutInput(data []byte) (PromoteScoutInput, error) {
	var input PromoteScoutInput
	if len(data) == 0 || len(data) > MaxRequestBytes {
		return PromoteScoutInput{}, errors.New("promote scout input exceeds its bound")
	}
	if err := decodeObject(data, &input); err != nil {
		return PromoteScoutInput{}, err
	}
	if input.ScoutTaskHandle != "" {
		return PromoteScoutInput{}, errors.New("promotion contract must not name its own scout")
	}
	return input, nil
}

type emptyPayload struct{}

type taskPayload struct {
	TaskHandle string `json:"taskHandle"`
}

type operationPayload struct {
	OperationID string `json:"operationId"`
}
