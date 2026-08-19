package mcpadapter

import (
	"context"

	"github.com/comisai/comis-dev-crew/internal/localapi"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// The finding and its keys are forwarded exactly as stated. The canonical
// coordinator decides whether they form a complete inventory; deciding it here
// as well would be a second authority free to disagree with the one that
// actually refuses.
func (facade *Facade) attestScoutDecisions(
	ctx context.Context,
	request *mcp.CallToolRequest,
	input AttestScoutDecisionsInput,
) (*mcp.CallToolResult, localapi.TaskMutationResult, error) {
	callContext, err := facade.authorize(request)
	if err != nil {
		return nil, localapi.TaskMutationResult{}, err
	}
	result, err := facade.client.AttestScoutDecisions(ctx, string(callContext.OperationID), localapi.AttestScoutDecisionsInput{
		TaskHandle: input.TaskHandle, Finding: input.Finding,
		OpenDecisionKeys: append([]string(nil), input.OpenDecisionKeys...),
	})
	return nil, result, err
}
