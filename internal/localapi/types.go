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
	MethodDiagnose      Method = "Diagnose"
	MethodFleet         Method = "FleetStatus"
	MethodListTasks     Method = "ListTasks"
	MethodShowTask      Method = "ShowTask"
	MethodExplainTask   Method = "ExplainTask"
	MethodGetLaunchPlan Method = "GetLaunchPlan"
	MethodOperation     Method = "GetOperation"
	MethodPrepareTask   Method = "PrepareTask"
	MethodReconcileTask Method = "ReconcileTask"
	MethodHandbackTask  Method = "HandbackTask"
	MethodCleanupTask   Method = "CleanupTask"
)

func (method Method) valid() bool {
	switch method {
	case MethodDiagnose, MethodFleet, MethodListTasks, MethodShowTask, MethodExplainTask, MethodGetLaunchPlan,
		MethodOperation, MethodPrepareTask, MethodReconcileTask, MethodHandbackTask, MethodCleanupTask:
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
	if method == MethodPrepareTask || method == MethodReconcileTask || method == MethodHandbackTask || method == MethodCleanupTask {
		return SideEffectMutate
	}
	return SideEffectRead
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

// ReadQueries is the narrow application surface consumed by the local boundary.
type ReadQueries interface {
	Diagnose(context.Context) (application.DiagnosticReport, error)
	Fleet(context.Context) (application.FleetSnapshot, error)
	ListTasks(context.Context) (application.TaskList, error)
	ShowTask(context.Context, string) (application.TaskDetail, error)
	ExplainTask(context.Context, string) (application.TaskExplanation, error)
	GetLaunchPlan(context.Context, string) (application.LaunchPlan, error)
	Operation(context.Context, string) (application.OperationView, error)
}

// TaskMutations is the sole canonical mutation surface used by the local API.
type TaskMutations interface {
	PrepareTask(context.Context, application.PrepareTaskCommand) (application.MutationResult, error)
}

// TaskInterventions is the canonical paused-worktree handback surface.
type TaskInterventions interface {
	HandbackTask(context.Context, application.HandbackTaskCommand) (application.MutationResult, error)
}

// TaskReconciliation is the canonical unknown-task recovery surface.
type TaskReconciliation interface {
	ReconcileTask(context.Context, application.ReconcileTaskCommand) (application.MutationResult, error)
}

// TaskCleanup is the canonical release-before-removal mutation surface.
type TaskCleanup interface {
	CleanupTask(context.Context, application.CleanupTaskCommand) (application.MutationResult, error)
}

// HandlerConfig binds local endpoint authority to canonical application seams.
type HandlerConfig struct {
	Queries           ReadQueries
	Mutations         TaskMutations
	Reconciliation    TaskReconciliation
	Interventions     TaskInterventions
	Cleanup           TaskCleanup
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

type emptyPayload struct{}

type taskPayload struct {
	TaskHandle string `json:"taskHandle"`
}

type operationPayload struct {
	OperationID string `json:"operationId"`
}
