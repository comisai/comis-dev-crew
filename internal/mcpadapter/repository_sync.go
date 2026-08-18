package mcpadapter

import (
	"context"

	"github.com/comisai/comis-dev-crew/internal/application"
	"github.com/comisai/comis-dev-crew/internal/localapi"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Synchronization carries no reconciliation retry. Re-running it is inherently
// safe: a checkout already at its upstream reports already_current, so an
// uncertain outcome is resolved by asking again rather than by a replay lookup.
func (facade *Facade) syncPrimary(
	ctx context.Context,
	request *mcp.CallToolRequest,
	input SyncPrimaryInput,
) (*mcp.CallToolResult, application.PrimarySyncReport, error) {
	callContext, err := facade.authorize(request)
	if err != nil {
		return nil, application.PrimarySyncReport{}, err
	}
	report, err := facade.client.SyncPrimary(ctx, string(callContext.OperationID), localapi.SyncPrimaryInput{
		RepositoryID: input.RepositoryID,
	})
	return nil, report, err
}
