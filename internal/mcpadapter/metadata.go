package mcpadapter

import (
	"encoding/json"
	"errors"
	"time"

	"github.com/comisai/comis-dev-crew/internal/comiswire"
	"github.com/comisai/comis-dev-crew/internal/domain"
	"github.com/comisai/comis-dev-crew/internal/localapi"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func (facade *Facade) authorize(request *mcp.CallToolRequest) (comiswire.MCPCallContext, error) {
	if request == nil || request.Params == nil {
		return comiswire.MCPCallContext{}, authorizationFailure()
	}
	value, found := request.Params.Meta[CallContextMetaKey]
	if !found {
		return comiswire.MCPCallContext{}, authorizationFailure()
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return comiswire.MCPCallContext{}, authorizationFailure()
	}
	canonical, err := comiswire.CanonicalizePayload(comiswire.PayloadMCPCallContext, encoded)
	if err != nil {
		return comiswire.MCPCallContext{}, authorizationFailure()
	}
	var callContext comiswire.MCPCallContext
	if err := json.Unmarshal(canonical, &callContext); err != nil {
		return comiswire.MCPCallContext{}, authorizationFailure()
	}
	if string(callContext.ServiceInstanceID) != facade.serviceInstanceID {
		return comiswire.MCPCallContext{}, authorizationFailure()
	}
	if err := domain.ValidateOperationID(string(callContext.OperationID)); err != nil {
		return comiswire.MCPCallContext{}, invalidOperationFailure()
	}
	return callContext, nil
}

func preparationMetadata(operationID string, prepared localapi.PrepareTaskResult) (any, error) {
	if prepared.OperationID != operationID || prepared.TaskHandle == "" ||
		prepared.ManagedRun.ExternalRunRef != prepared.TaskHandle || prepared.State != domain.TaskPrepared ||
		prepared.SideEffect != localapi.SideEffectMutate || prepared.ManagedRun.ExpiresAt.Location() != time.UTC {
		return nil, internalResultFailure()
	}
	extension := comiswire.MCPManagedRunResult{
		State:             comiswire.ManagedRunStatePrepared,
		ExternalRunRef:    comiswire.ExternalRunRef(prepared.ManagedRun.ExternalRunRef),
		RegistrationNonce: comiswire.RegistrationNonce(prepared.ManagedRun.RegistrationNonce),
		ExpiresAt:         prepared.ManagedRun.ExpiresAt.Format(time.RFC3339Nano),
	}
	if prepared.ManagedRun.RequestedWorkspaceRoot != "" {
		extension.RequestedWorkspace = &comiswire.MCPManagedRunResultRequestedWorkspace{
			RootHint: prepared.ManagedRun.RequestedWorkspaceRoot,
		}
	}
	encoded, err := json.Marshal(extension)
	if err != nil || comiswire.ValidatePayload(comiswire.PayloadMCPManagedRunResult, encoded) != nil {
		return nil, internalResultFailure()
	}
	var metadata any
	if err := json.Unmarshal(encoded, &metadata); err != nil {
		return nil, internalResultFailure()
	}
	return metadata, nil
}

func authorizationFailure() error {
	return safeFailure(domain.ErrorUnauthorized, false, "MCP call context is invalid", "use authenticated Comis tool metadata")
}

func invalidOperationFailure() error {
	return safeFailure(domain.ErrorInvalidArgument, false, "MCP operation identity is invalid", "use a bounded local operation identity")
}

func internalResultFailure() error {
	return safeFailure(domain.ErrorInternal, false, "local preparation result is invalid", "inspect service health before retrying")
}

func safeFailure(code domain.ErrorCode, retryable bool, message, hint string) error {
	failure, err := domain.NewFailure(code, retryable, message, hint, nil)
	if err != nil {
		return errors.New("internal: safe MCP failure is unavailable")
	}
	return failure
}
