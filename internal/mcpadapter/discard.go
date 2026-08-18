package mcpadapter

import (
	"context"

	"github.com/comisai/comis-dev-crew/internal/localapi"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Discard is the only mutation that removes work nothing can point at: a task
// that stopped without delivering has no evidence for cleanup's gate and no
// artifact a later command could recover. Cancel preserves the worktree and
// cleanup requires delivery, so the description has to separate discard from
// both or a model will reach for it as the tidier of the three.
func discardTool() *mcp.Tool {
	destructive, openWorld := true, true
	return &mcp.Tool{
		Name: ToolDiscardTask,
		Description: "Remove the worktree of one task that never delivered, permanently discarding its " +
			"uncommitted work. Requires the operator's explicit acknowledgement. Cancel stops work while " +
			"preserving it, and cleanup removes only safely delivered work.",
		Annotations: &mcp.ToolAnnotations{
			ReadOnlyHint: false, DestructiveHint: &destructive,
			IdempotentHint: true, OpenWorldHint: &openWorld,
		},
	}
}

// The acknowledgement is forwarded rather than re-decided here. The canonical
// coordinator owns the gate, and a second check in the adapter would be a
// parallel authority that could drift from it.
func (facade *Facade) discardTask(
	ctx context.Context,
	request *mcp.CallToolRequest,
	input DiscardTaskInput,
) (*mcp.CallToolResult, localapi.TaskMutationResult, error) {
	callContext, err := facade.authorize(request)
	if err != nil {
		return nil, localapi.TaskMutationResult{}, err
	}
	operationID := string(callContext.OperationID)
	localInput := localapi.DiscardTaskInput{
		TaskHandle: input.TaskHandle, Acknowledged: input.Acknowledged,
	}
	result, err := facade.client.DiscardTask(ctx, operationID, localInput)
	if err != nil && uncertainMutation(ctx, err) {
		result, err = facade.reconcileDiscard(ctx, operationID, localInput, err)
	}
	return nil, result, err
}
