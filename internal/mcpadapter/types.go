// Package mcpadapter implements the stateless official-SDK MCP facade.
package mcpadapter

import (
	"context"
	"time"

	"github.com/comisai/comis-dev-crew/internal/application"
	"github.com/comisai/comis-dev-crew/internal/domain"
	"github.com/comisai/comis-dev-crew/internal/localapi"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	ToolPrepareTask    = "prepare_task"
	ToolReconcileTask  = "reconcile_task"
	ToolHandbackTask   = "handback_task"
	ToolCleanupTask    = "cleanup_task"
	ToolPauseTask      = "pause_task"
	ToolCancelTask     = "cancel_task"
	ToolResumeTask     = "resume_task"
	ToolVerifyTask     = "verify_task"
	ToolPromoteScout   = "promote_scout"
	ToolReplaceWorker  = "replace_worker"
	ToolListTasks      = "list_tasks"
	ToolWorkerProfiles = "worker_profiles"
	ToolGetTask        = "get_task"
	ToolExplainTask    = "explain_task"
	ToolGetLaunchPlan  = "get_launch_plan"
	ToolDoctor         = "doctor"

	CallContextMetaKey      = "comis.callContext"
	ManagedRunResultMetaKey = "comis.managedRun"
)

// Client is the sole canonical local-service surface used by the facade.
type Client interface {
	PrepareTask(context.Context, string, localapi.PrepareTaskInput) (localapi.PrepareTaskResult, error)
	ReconcileTask(context.Context, string, localapi.ReconcileTaskInput) (localapi.TaskMutationResult, error)
	HandbackTask(context.Context, string, localapi.HandbackTaskInput) (localapi.TaskMutationResult, error)
	CleanupTask(context.Context, string, localapi.CleanupTaskInput) (localapi.TaskMutationResult, error)
	Diagnose(context.Context, string) (application.DiagnosticReport, error)
	ListTasks(context.Context, string) (application.TaskList, error)
	ListWorkerProfiles(context.Context, string) (application.WorkerProfileList, error)
	PauseTask(context.Context, string, localapi.PauseTaskInput) (localapi.TaskMutationResult, error)
	CancelTask(context.Context, string, localapi.CancelTaskInput) (localapi.TaskMutationResult, error)
	ResumeTask(context.Context, string, localapi.ResumeTaskInput) (localapi.TaskMutationResult, error)
	VerifyTask(context.Context, string, localapi.VerifyTaskInput) (localapi.TaskMutationResult, error)
	PromoteScout(context.Context, string, localapi.PromoteScoutInput) (localapi.PrepareTaskResult, error)
	ReplaceWorker(context.Context, string, localapi.ReplaceWorkerInput) (localapi.TaskMutationResult, error)
	ShowTask(context.Context, string, string) (application.TaskDetail, error)
	ExplainTask(context.Context, string, string) (application.TaskExplanation, error)
	GetLaunchPlan(context.Context, string, string) (application.LaunchPlan, error)
	Operation(context.Context, string, string) (application.OperationView, error)
}

// Config injects the local client and bounded reconciliation dependencies.
type Config struct {
	Client            Client
	ServiceInstanceID string
	Version           string
	NewOperationID    func() (string, error)
	ReconcileTimeout  time.Duration
}

// EmptyInput is the closed argument object for list_tasks.
type EmptyInput struct{}

// TaskInput selects one service-owned opaque task reference.
type TaskInput struct {
	TaskHandle string `json:"taskHandle" jsonschema:"opaque task handle"`
}

// PrepareTaskInput is the public model-visible task contract.
type PrepareTaskInput struct {
	Shape              domain.TaskShape    `json:"shape" jsonschema:"task shape; use exactly ship or scout"`
	RepositoryID       string              `json:"repositoryId" jsonschema:"operator-configured repository catalog identity"`
	BaseRevision       string              `json:"baseRevision" jsonschema:"exact 40-character lowercase hexadecimal Git revision"`
	AcceptanceCriteria []string            `json:"acceptanceCriteria" jsonschema:"ordered acceptance criteria; provide a JSON array of strings"`
	Constraints        []string            `json:"constraints" jsonschema:"ordered task constraints; provide a JSON array of strings, using an empty array when there are none"`
	ValidationProfile  string              `json:"validationProfile" jsonschema:"operator-configured validation profile identity"`
	DeliveryMode       domain.DeliveryMode `json:"deliveryMode" jsonschema:"use exactly pull_request for ship or report for scout"`
	WorkerProfileID    string              `json:"workerProfileId" jsonschema:"operator-configured worker profile identity"`
}

// HandbackTaskInput selects one safe paused task for developer-work validation.
type HandbackTaskInput struct {
	TaskHandle string                     `json:"taskHandle" jsonschema:"opaque task handle"`
	Action     application.HandbackAction `json:"action" jsonschema:"validate-developer-work"`
}

// ReconcileTaskInput selects one unknown task for exact clean-candidate
// validation without accepting caller-provided filesystem or Git authority.
type ReconcileTaskInput struct {
	TaskHandle string                          `json:"taskHandle" jsonschema:"opaque task handle"`
	Action     application.ReconcileTaskAction `json:"action" jsonschema:"validate-clean-candidate"`
}

func (input PrepareTaskInput) local() localapi.PrepareTaskInput {
	return localapi.PrepareTaskInput{
		Shape: input.Shape, RepositoryID: input.RepositoryID, BaseRevision: input.BaseRevision,
		AcceptanceCriteria: append([]string(nil), input.AcceptanceCriteria...),
		Constraints:        append([]string(nil), input.Constraints...),
		ValidationProfile:  input.ValidationProfile, DeliveryMode: input.DeliveryMode,
		WorkerProfileID: input.WorkerProfileID,
	}
}

// PromoteScoutInput names the scout and states the ship contract. It has no
// repository or base-revision field on purpose: both are inherited from the
// scout, so a promotion cannot aim the ship task at code the investigation never
// covered while still carrying that investigation as its justification.
type PromoteScoutInput struct {
	ScoutTaskHandle    string              `json:"scoutTaskHandle" jsonschema:"opaque handle of the scout task being promoted"`
	AcceptanceCriteria []string            `json:"acceptanceCriteria" jsonschema:"ordered acceptance criteria for the ship revision; provide a JSON array of strings"`
	Constraints        []string            `json:"constraints" jsonschema:"ordered constraints; provide a JSON array of strings, using an empty array when there are none"`
	ValidationProfile  string              `json:"validationProfile" jsonschema:"operator-configured validation profile identity"`
	DeliveryMode       domain.DeliveryMode `json:"deliveryMode" jsonschema:"use exactly pull_request"`
	WorkerProfileID    string              `json:"workerProfileId" jsonschema:"operator-configured worker profile identity"`
}

func (input PromoteScoutInput) local() localapi.PromoteScoutInput {
	return localapi.PromoteScoutInput{
		ScoutTaskHandle:    input.ScoutTaskHandle,
		AcceptanceCriteria: append([]string(nil), input.AcceptanceCriteria...),
		Constraints:        append([]string(nil), input.Constraints...),
		ValidationProfile:  input.ValidationProfile, DeliveryMode: input.DeliveryMode,
		WorkerProfileID: input.WorkerProfileID,
	}
}

// ReplaceWorkerInput names the task and the reviewed profile that takes over.
type ReplaceWorkerInput struct {
	TaskHandle      string `json:"taskHandle" jsonschema:"opaque task handle"`
	WorkerProfileID string `json:"workerProfileId" jsonschema:"operator-configured worker profile identity that takes over"`
}

// PrepareTaskOutput is deliberately free of private Comis registration data.
type PrepareTaskOutput struct {
	SchemaVersion int                      `json:"schemaVersion"`
	OperationID   string                   `json:"operationId"`
	TaskHandle    string                   `json:"taskHandle"`
	State         domain.TaskState         `json:"state"`
	StateVersion  int64                    `json:"stateVersion"`
	SideEffect    localapi.SideEffectClass `json:"sideEffect"`
}

// Facade owns no durable state; it binds typed handlers to one SDK server.
type Facade struct {
	client            Client
	serviceInstanceID string
	newOperationID    func() (string, error)
	reconcileTimeout  time.Duration
	server            *mcp.Server
}
