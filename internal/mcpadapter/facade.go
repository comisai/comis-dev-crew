package mcpadapter

import (
	"context"
	"errors"
	"time"

	"github.com/comisai/comis-dev-crew/internal/application"
	"github.com/comisai/comis-dev-crew/internal/domain"
	"github.com/comisai/comis-dev-crew/internal/localapi"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const defaultReconcileTimeout = 2 * time.Second
const maximumReconcileTimeout = 10 * time.Second

// New creates the official-SDK facade over the durable task-control surface;
// registerTools pins its exact tool set and assertToolCatalog guards it.
func New(config Config) (*Facade, error) {
	if config.Client == nil || config.NewOperationID == nil {
		return nil, errors.New("create MCP facade: local client and operation source are required")
	}
	if err := domain.ValidateAuthorityReference("serviceInstanceId", config.ServiceInstanceID); err != nil {
		return nil, errors.New("create MCP facade: service instance identity is invalid")
	}
	timeout := config.ReconcileTimeout
	if timeout == 0 {
		timeout = defaultReconcileTimeout
	}
	if timeout < 0 || timeout > maximumReconcileTimeout {
		return nil, errors.New("create MCP facade: reconciliation timeout is invalid")
	}
	version := config.Version
	if version == "" {
		version = "dev"
	}
	facade := &Facade{
		client: config.Client, serviceInstanceID: config.ServiceInstanceID,
		newOperationID: config.NewOperationID, reconcileTimeout: timeout,
	}
	facade.server = mcp.NewServer(&mcp.Implementation{Name: "devcrew-mcp", Version: version}, nil)
	facade.registerTools()
	return facade, nil
}

// Server exposes the configured SDK server for stdio and in-memory transports.
func (facade *Facade) Server() *mcp.Server { return facade.server }

// Run serves one MCP transport until it closes or the context is canceled.
func (facade *Facade) Run(ctx context.Context, transport mcp.Transport) error {
	if ctx == nil || transport == nil {
		return errors.New("run MCP facade: context and transport are required")
	}
	return facade.server.Run(ctx, transport)
}

func (facade *Facade) registerTools() {
	mcp.AddTool(facade.server, tool(ToolPrepareTask, "Prepare one durable development task; acceptanceCriteria and constraints must be JSON arrays.", false), facade.prepareTask)
	mcp.AddTool(facade.server, tool(ToolReconcileTask, "Validate one exact clean candidate after its worker terminal ended without a candidate report.", false), facade.reconcileTask)
	mcp.AddTool(facade.server, tool(ToolHandbackTask, "Validate developer work after one safe paused worker exits.", false), facade.handbackTask)
	mcp.AddTool(facade.server, cleanupTool(), facade.cleanupTask)
	mcp.AddTool(facade.server, cancelTool(), facade.cancelTask)
	mcp.AddTool(facade.server, tool(
		ToolResumeTask,
		"Return one paused task to the worker already running it. Refused when the worktree has uncommitted changes; hand the work back instead so the edit is revalidated.",
		false,
	), facade.resumeTask)
	mcp.AddTool(facade.server, tool(
		ToolVerifyTask,
		"Validate one task now against the reviewed profile it was prepared with, refreshing its evidence. It selects no checks of its own and does not decide the outcome.",
		false,
	), facade.verifyTask)
	mcp.AddTool(facade.server, tool(
		ToolPromoteScout,
		"Create a new ship task from a scout's investigation, preserving the scout and its evidence. The repository and base revision are inherited from the scout and cannot be chosen here.",
		false,
	), facade.promoteScout)
	mcp.AddTool(facade.server, tool(
		ToolReplaceWorker,
		"Hand one paused task to a different reviewed worker from a fresh brief. The worktree and its work are preserved; only who continues changes.",
		false,
	), facade.replaceWorker)
	mcp.AddTool(facade.server, tool(
		ToolSteerTask,
		"Send one bounded instruction to a task's current worker. The worker reads it on its next report; the task does not change state.",
		false,
	), facade.steerTask)
	mcp.AddTool(facade.server, tool(
		ToolPauseTask,
		"Ask one task's worker to reach a safe boundary and stop. It carries no instruction and no interrupt, and does not itself pause the task: the worker settles and reports.",
		false,
	), facade.pauseTask)
	mcp.AddTool(facade.server, tool(ToolListTasks, "List durable development tasks.", true), facade.listTasks)
	mcp.AddTool(facade.server, tool(
		ToolWorkerProfiles,
		"List the reviewed worker profiles this deployment configured, with the shapes each accepts and whether its harness is available.",
		true,
	), facade.workerProfiles)
	mcp.AddTool(facade.server, tool(ToolGetTask, "Get one durable development task.", true), facade.getTask)
	mcp.AddTool(facade.server, tool(ToolExplainTask, "Explain one durable task posture.", true), facade.explainTask)
	mcp.AddTool(facade.server, tool(ToolGetLaunchPlan, "Get reviewed launch requirements for one ready task.", true), facade.getLaunchPlan)
	mcp.AddTool(facade.server, tool(ToolDoctor, "Report bounded service, repository, harness, and forge readiness.", true), facade.doctor)
}

func (facade *Facade) reconcileTask(
	ctx context.Context,
	request *mcp.CallToolRequest,
	input ReconcileTaskInput,
) (*mcp.CallToolResult, localapi.TaskMutationResult, error) {
	callContext, err := facade.authorize(request)
	if err != nil {
		return nil, localapi.TaskMutationResult{}, err
	}
	operationID := string(callContext.OperationID)
	localInput := localapi.ReconcileTaskInput{TaskHandle: input.TaskHandle, Action: input.Action}
	result, err := facade.client.ReconcileTask(ctx, operationID, localInput)
	if err != nil && uncertainMutation(ctx, err) {
		result, err = facade.reconcileTaskMutation(ctx, operationID, localInput, err)
	}
	return nil, result, err
}

func (facade *Facade) cleanupTask(
	ctx context.Context,
	request *mcp.CallToolRequest,
	input TaskInput,
) (*mcp.CallToolResult, localapi.TaskMutationResult, error) {
	callContext, err := facade.authorize(request)
	if err != nil {
		return nil, localapi.TaskMutationResult{}, err
	}
	operationID := string(callContext.OperationID)
	localInput := localapi.CleanupTaskInput{TaskHandle: input.TaskHandle}
	result, err := facade.client.CleanupTask(ctx, operationID, localInput)
	if err != nil && uncertainMutation(ctx, err) {
		result, err = facade.reconcileCleanup(ctx, operationID, localInput, err)
	}
	return nil, result, err
}

func (facade *Facade) handbackTask(
	ctx context.Context,
	request *mcp.CallToolRequest,
	input HandbackTaskInput,
) (*mcp.CallToolResult, localapi.TaskMutationResult, error) {
	callContext, err := facade.authorize(request)
	if err != nil {
		return nil, localapi.TaskMutationResult{}, err
	}
	operationID := string(callContext.OperationID)
	localInput := localapi.HandbackTaskInput{TaskHandle: input.TaskHandle, Action: input.Action}
	result, err := facade.client.HandbackTask(ctx, operationID, localInput)
	if err != nil && uncertainMutation(ctx, err) {
		result, err = facade.reconcileHandback(ctx, operationID, localInput, err)
	}
	return nil, result, err
}

func tool(name, description string, readOnly bool) *mcp.Tool {
	destructive, openWorld := false, false
	return &mcp.Tool{
		Name: name, Description: description,
		Annotations: &mcp.ToolAnnotations{
			ReadOnlyHint: readOnly, DestructiveHint: &destructive,
			IdempotentHint: true, OpenWorldHint: &openWorld,
		},
	}
}

// Cancel is destructive because it ends work an operator asked for and cannot
// be undone by asking again. It is not, however, removal: the worktree and every
// artifact survive, and discarding them stays behind cleanup's evidence gate.
func cancelTool() *mcp.Tool {
	destructive, openWorld := true, true
	return &mcp.Tool{
		Name: ToolCancelTask,
		Description: "Stop work on one task while preserving its worktree and artifacts. " +
			"Removal is a separate evidence-gated cleanup.",
		Annotations: &mcp.ToolAnnotations{
			ReadOnlyHint: false, DestructiveHint: &destructive,
			IdempotentHint: true, OpenWorldHint: &openWorld,
		},
	}
}

func cleanupTool() *mcp.Tool {
	destructive, openWorld := true, true
	return &mcp.Tool{
		Name: ToolCleanupTask, Description: "Release exact host authority and remove one safely delivered task worktree.",
		Annotations: &mcp.ToolAnnotations{
			ReadOnlyHint: false, DestructiveHint: &destructive,
			IdempotentHint: true, OpenWorldHint: &openWorld,
		},
	}
}

func (facade *Facade) prepareTask(ctx context.Context, request *mcp.CallToolRequest, input PrepareTaskInput) (*mcp.CallToolResult, PrepareTaskOutput, error) {
	callContext, err := facade.authorize(request)
	if err != nil {
		return nil, PrepareTaskOutput{}, err
	}
	operationID := string(callContext.OperationID)
	prepared, err := facade.client.PrepareTask(ctx, operationID, input.local())
	if err != nil && uncertainMutation(ctx, err) {
		prepared, err = facade.reconcilePreparation(ctx, operationID, input.local(), err)
	}
	if err != nil {
		return nil, PrepareTaskOutput{}, err
	}
	metadata, err := preparationMetadata(operationID, prepared)
	if err != nil {
		return nil, PrepareTaskOutput{}, err
	}
	output := PrepareTaskOutput{
		SchemaVersion: prepared.SchemaVersion, OperationID: prepared.OperationID,
		TaskHandle: prepared.TaskHandle, State: prepared.State,
		StateVersion: prepared.StateVersion, SideEffect: prepared.SideEffect,
	}
	return &mcp.CallToolResult{Meta: mcp.Meta{ManagedRunResultMetaKey: metadata}}, output, nil
}

// Promotion mints a task, so it returns the same private registration metadata
// preparation does. The scout is untouched: a worker able to change its own
// task's shape would grant itself the push and pull-request authority the scout
// shape withholds.
func (facade *Facade) promoteScout(
	ctx context.Context,
	request *mcp.CallToolRequest,
	input PromoteScoutInput,
) (*mcp.CallToolResult, PrepareTaskOutput, error) {
	callContext, err := facade.authorize(request)
	if err != nil {
		return nil, PrepareTaskOutput{}, err
	}
	operationID := string(callContext.OperationID)
	promoted, err := facade.client.PromoteScout(ctx, operationID, input.local())
	if err != nil && uncertainMutation(ctx, err) {
		promoted, err = facade.reconcilePromotion(ctx, operationID, input.local(), err)
	}
	if err != nil {
		return nil, PrepareTaskOutput{}, err
	}
	metadata, err := preparationMetadata(operationID, promoted)
	if err != nil {
		return nil, PrepareTaskOutput{}, err
	}
	return &mcp.CallToolResult{Meta: mcp.Meta{ManagedRunResultMetaKey: metadata}}, PrepareTaskOutput{
		SchemaVersion: promoted.SchemaVersion, OperationID: promoted.OperationID,
		TaskHandle: promoted.TaskHandle, State: promoted.State,
		StateVersion: promoted.StateVersion, SideEffect: promoted.SideEffect,
	}, nil
}

func (facade *Facade) replaceWorker(
	ctx context.Context,
	request *mcp.CallToolRequest,
	input ReplaceWorkerInput,
) (*mcp.CallToolResult, localapi.TaskMutationResult, error) {
	callContext, err := facade.authorize(request)
	if err != nil {
		return nil, localapi.TaskMutationResult{}, err
	}
	operationID := string(callContext.OperationID)
	localInput := localapi.ReplaceWorkerInput{
		TaskHandle: input.TaskHandle, WorkerProfileID: input.WorkerProfileID,
	}
	result, err := facade.client.ReplaceWorker(ctx, operationID, localInput)
	if err != nil && uncertainMutation(ctx, err) {
		result, err = reconcileTaskMutation(
			facade, ctx, operationID, "ReplaceWorker", localInput,
			facade.client.ReplaceWorker, err,
		)
	}
	return nil, result, err
}

func (facade *Facade) steerTask(
	ctx context.Context,
	request *mcp.CallToolRequest,
	input SteerTaskInput,
) (*mcp.CallToolResult, localapi.TaskMutationResult, error) {
	callContext, err := facade.authorize(request)
	if err != nil {
		return nil, localapi.TaskMutationResult{}, err
	}
	operationID := string(callContext.OperationID)
	localInput := localapi.SteerTaskInput{
		TaskHandle: input.TaskHandle, Instruction: input.Instruction,
	}
	result, err := facade.client.SteerTask(ctx, operationID, localInput)
	if err != nil && uncertainMutation(ctx, err) {
		// An uncertain steer is reconciled, never re-sent: re-sending would queue
		// the same instruction twice and the worker would act on it twice.
		result, err = reconcileTaskMutation(
			facade, ctx, operationID, "SteerTask", localInput, facade.client.SteerTask, err,
		)
	}
	return nil, result, err
}

func (facade *Facade) listTasks(ctx context.Context, request *mcp.CallToolRequest, _ EmptyInput) (*mcp.CallToolResult, application.TaskList, error) {
	callContext, err := facade.authorize(request)
	if err != nil {
		return nil, application.TaskList{}, err
	}
	result, err := facade.client.ListTasks(ctx, string(callContext.OperationID))
	return nil, result, err
}

// A liaison that cannot start work needs to distinguish "no profile accepts this
// shape" from "the profile exists but its harness is unavailable" — a
// preparation failure reports neither. The catalog carries identity and posture
// only: never the executable, the argument vector, or the environment
// allowlist, which are launch authority and stay with the adapter that builds
// the descriptor.
func (facade *Facade) workerProfiles(ctx context.Context, request *mcp.CallToolRequest, _ EmptyInput) (*mcp.CallToolResult, application.WorkerProfileList, error) {
	callContext, err := facade.authorize(request)
	if err != nil {
		return nil, application.WorkerProfileList{}, err
	}
	result, err := facade.client.ListWorkerProfiles(ctx, string(callContext.OperationID))
	return nil, result, err
}

// Readiness is the one read that answers "why can nothing start", so it is
// reachable from the agent surface as well as the operator CLI. It reports what
// is configured and reachable — never a credential, a path, or a secret ref.
func (facade *Facade) doctor(ctx context.Context, request *mcp.CallToolRequest, _ EmptyInput) (*mcp.CallToolResult, application.DiagnosticReport, error) {
	callContext, err := facade.authorize(request)
	if err != nil {
		return nil, application.DiagnosticReport{}, err
	}
	result, err := facade.client.Diagnose(ctx, string(callContext.OperationID))
	return nil, result, err
}

func (facade *Facade) getTask(ctx context.Context, request *mcp.CallToolRequest, input TaskInput) (*mcp.CallToolResult, application.TaskDetail, error) {
	callContext, err := facade.authorize(request)
	if err != nil {
		return nil, application.TaskDetail{}, err
	}
	result, err := facade.client.ShowTask(ctx, string(callContext.OperationID), input.TaskHandle)
	return nil, result, err
}

func (facade *Facade) explainTask(ctx context.Context, request *mcp.CallToolRequest, input TaskInput) (*mcp.CallToolResult, application.TaskExplanation, error) {
	callContext, err := facade.authorize(request)
	if err != nil {
		return nil, application.TaskExplanation{}, err
	}
	result, err := facade.client.ExplainTask(ctx, string(callContext.OperationID), input.TaskHandle)
	return nil, result, err
}

func (facade *Facade) getLaunchPlan(ctx context.Context, request *mcp.CallToolRequest, input TaskInput) (*mcp.CallToolResult, application.LaunchPlan, error) {
	callContext, err := facade.authorize(request)
	if err != nil {
		return nil, application.LaunchPlan{}, err
	}
	result, err := facade.client.GetLaunchPlan(ctx, string(callContext.OperationID), input.TaskHandle)
	return nil, result, err
}

// Pause is a request, not a transition, so a repeat is harmless and an
// uncertain send is reconciled the same way every other mutation is: by asking
// what the recorded operation actually did rather than sending again.
func (facade *Facade) pauseTask(
	ctx context.Context,
	request *mcp.CallToolRequest,
	input TaskInput,
) (*mcp.CallToolResult, localapi.TaskMutationResult, error) {
	callContext, err := facade.authorize(request)
	if err != nil {
		return nil, localapi.TaskMutationResult{}, err
	}
	operationID := string(callContext.OperationID)
	localInput := localapi.PauseTaskInput{TaskHandle: input.TaskHandle}
	result, err := facade.client.PauseTask(ctx, operationID, localInput)
	if err != nil && uncertainMutation(ctx, err) {
		result, err = facade.reconcilePause(ctx, operationID, localInput, err)
	}
	return nil, result, err
}

func (facade *Facade) cancelTask(
	ctx context.Context,
	request *mcp.CallToolRequest,
	input TaskInput,
) (*mcp.CallToolResult, localapi.TaskMutationResult, error) {
	callContext, err := facade.authorize(request)
	if err != nil {
		return nil, localapi.TaskMutationResult{}, err
	}
	operationID := string(callContext.OperationID)
	localInput := localapi.CancelTaskInput{TaskHandle: input.TaskHandle}
	result, err := facade.client.CancelTask(ctx, operationID, localInput)
	if err != nil && uncertainMutation(ctx, err) {
		result, err = facade.reconcileCancel(ctx, operationID, localInput, err)
	}
	return nil, result, err
}

func (facade *Facade) resumeTask(
	ctx context.Context,
	request *mcp.CallToolRequest,
	input TaskInput,
) (*mcp.CallToolResult, localapi.TaskMutationResult, error) {
	callContext, err := facade.authorize(request)
	if err != nil {
		return nil, localapi.TaskMutationResult{}, err
	}
	operationID := string(callContext.OperationID)
	localInput := localapi.ResumeTaskInput{TaskHandle: input.TaskHandle}
	result, err := facade.client.ResumeTask(ctx, operationID, localInput)
	if err != nil && uncertainMutation(ctx, err) {
		result, err = facade.reconcileResume(ctx, operationID, localInput, err)
	}
	return nil, result, err
}

func (facade *Facade) verifyTask(
	ctx context.Context,
	request *mcp.CallToolRequest,
	input TaskInput,
) (*mcp.CallToolResult, localapi.TaskMutationResult, error) {
	callContext, err := facade.authorize(request)
	if err != nil {
		return nil, localapi.TaskMutationResult{}, err
	}
	operationID := string(callContext.OperationID)
	localInput := localapi.VerifyTaskInput{TaskHandle: input.TaskHandle}
	result, err := facade.client.VerifyTask(ctx, operationID, localInput)
	if err != nil && uncertainMutation(ctx, err) {
		result, err = facade.reconcileVerify(ctx, operationID, localInput, err)
	}
	return nil, result, err
}

func uncertainMutation(ctx context.Context, err error) bool {
	if ctx != nil && ctx.Err() != nil {
		return true
	}
	var failure *domain.Failure
	if !errors.As(err, &failure) || !failure.Retryable {
		return false
	}
	switch failure.Code {
	case domain.ErrorUnavailable, domain.ErrorDeadlineExceeded, domain.ErrorUnknown:
		return true
	default:
		return false
	}
}
