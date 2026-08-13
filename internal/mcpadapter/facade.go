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

// New creates the exact eight-tool official-SDK facade.
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
	mcp.AddTool(facade.server, tool(ToolListTasks, "List durable development tasks.", true), facade.listTasks)
	mcp.AddTool(facade.server, tool(ToolGetTask, "Get one durable development task.", true), facade.getTask)
	mcp.AddTool(facade.server, tool(ToolExplainTask, "Explain one durable task posture.", true), facade.explainTask)
	mcp.AddTool(facade.server, tool(ToolGetLaunchPlan, "Get reviewed launch requirements for one ready task.", true), facade.getLaunchPlan)
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

func (facade *Facade) listTasks(ctx context.Context, request *mcp.CallToolRequest, _ EmptyInput) (*mcp.CallToolResult, application.TaskList, error) {
	callContext, err := facade.authorize(request)
	if err != nil {
		return nil, application.TaskList{}, err
	}
	result, err := facade.client.ListTasks(ctx, string(callContext.OperationID))
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
