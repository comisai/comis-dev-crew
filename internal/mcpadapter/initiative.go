package mcpadapter

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/comisai/comis-dev-crew/internal/application"
	"github.com/comisai/comis-dev-crew/internal/comiswire"
	"github.com/comisai/comis-dev-crew/internal/domain"
	"github.com/comisai/comis-dev-crew/internal/localapi"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func (facade *Facade) prepareInitiative(
	ctx context.Context,
	request *mcp.CallToolRequest,
	input PrepareInitiativeInput,
) (*mcp.CallToolResult, PrepareInitiativeOutput, error) {
	callContext, err := facade.authorize(request)
	if err != nil {
		return nil, PrepareInitiativeOutput{}, err
	}
	operationID := string(callContext.OperationID)
	localInput := input.local()
	prepared, err := facade.client.PrepareInitiative(ctx, operationID, localInput)
	if err != nil && uncertainMutation(ctx, err) {
		prepared, err = facade.reconcileInitiativePreparation(ctx, operationID, localInput, err)
	}
	if err != nil {
		return nil, PrepareInitiativeOutput{}, err
	}
	metadata, err := initiativePreparationMetadata(operationID, prepared)
	if err != nil {
		return nil, PrepareInitiativeOutput{}, err
	}
	output := PrepareInitiativeOutput{
		SchemaVersion: prepared.SchemaVersion, OperationID: prepared.OperationID,
		InitiativeHandle: prepared.InitiativeHandle, State: prepared.State,
		StateVersion: prepared.StateVersion, SideEffect: prepared.SideEffect,
		TaskHandles: append([]string(nil), prepared.TaskHandles...),
	}
	return &mcp.CallToolResult{Meta: mcp.Meta{ManagedRunResultMetaKey: metadata}}, output, nil
}

func (facade *Facade) getInitiative(
	ctx context.Context,
	request *mcp.CallToolRequest,
	input InitiativeInput,
) (*mcp.CallToolResult, application.InitiativeDetail, error) {
	callContext, err := facade.authorize(request)
	if err != nil {
		return nil, application.InitiativeDetail{}, err
	}
	result, err := facade.client.GetInitiative(ctx, string(callContext.OperationID), input.InitiativeHandle)
	return nil, result, err
}

func (facade *Facade) listBacklog(
	ctx context.Context,
	request *mcp.CallToolRequest,
	input BacklogListInput,
) (*mcp.CallToolResult, application.BacklogList, error) {
	callContext, err := facade.authorize(request)
	if err != nil {
		return nil, application.BacklogList{}, err
	}
	result, err := facade.client.ListBacklog(ctx, string(callContext.OperationID), localapi.ListBacklogInput{
		RepositoryID: input.RepositoryID, Readiness: input.Readiness,
	})
	return nil, result, err
}

func (facade *Facade) reconcileInitiativePreparation(
	ctx context.Context,
	operationID string,
	input localapi.PrepareInitiativeInput,
	original error,
) (localapi.PrepareInitiativeResult, error) {
	if ctx == nil {
		return localapi.PrepareInitiativeResult{}, original
	}
	reconcileContext, cancel := context.WithTimeout(context.WithoutCancel(ctx), facade.reconcileTimeout)
	defer cancel()
	requestID, err := facade.newOperationID()
	if err != nil || domain.ValidateOperationID(requestID) != nil {
		return localapi.PrepareInitiativeResult{}, original
	}
	operation, err := facade.client.Operation(reconcileContext, requestID, operationID)
	if err != nil || operation.OperationID != operationID || operation.Command != "PrepareInitiative" {
		return localapi.PrepareInitiativeResult{}, original
	}
	switch operation.Status {
	case domain.OperationCompleted:
		return facade.client.PrepareInitiative(reconcileContext, operationID, input)
	case domain.OperationRejected:
		if operation.ErrorCode.Valid() {
			return localapi.PrepareInitiativeResult{}, safeFailure(
				operation.ErrorCode, false, "initiative preparation was rejected",
				"correct the initiative contract before retrying",
			)
		}
		return localapi.PrepareInitiativeResult{}, original
	case domain.OperationAccepted, domain.OperationUnknown:
		return localapi.PrepareInitiativeResult{}, original
	default:
		return localapi.PrepareInitiativeResult{}, errors.New("unknown initiative reconciliation status")
	}
}

func initiativePreparationMetadata(operationID string, prepared localapi.PrepareInitiativeResult) (any, error) {
	group := prepared.ManagedRunGroup
	if prepared.OperationID != operationID || prepared.InitiativeHandle == "" ||
		prepared.State != domain.InitiativePreparing || prepared.SideEffect != localapi.SideEffectMutate ||
		group.ExternalGroupRef != prepared.InitiativeHandle || group.ExpiresAt.Location() != time.UTC ||
		len(prepared.TaskHandles) == 0 || len(prepared.TaskHandles) != len(group.Members) {
		return nil, internalResultFailure()
	}
	extension := comiswire.MCPManagedRunGroupResult{
		State:             comiswire.ManagedRunStatePrepared,
		RegistrationNonce: comiswire.RegistrationNonce(group.RegistrationNonce),
		ExpiresAt:         group.ExpiresAt.Format(time.RFC3339Nano),
		Members:           make([]comiswire.MCPManagedRunGroupResultMembersItem, 0, len(group.Members)),
	}
	for index, member := range group.Members {
		if member.ExternalRunRef != prepared.TaskHandles[index] || member.State != application.PreparationOpen ||
			member.ExpiresAt.Location() != time.UTC || !member.ExpiresAt.Equal(group.ExpiresAt) ||
			member.RequestedAttachment.Validate() != nil {
			return nil, internalResultFailure()
		}
		item := comiswire.MCPManagedRunGroupResultMembersItem{
			State:             comiswire.ManagedRunStatePrepared,
			ExternalRunRef:    comiswire.ExternalRunRef(member.ExternalRunRef),
			RegistrationNonce: comiswire.RegistrationNonce(member.RegistrationNonce),
			ExpiresAt:         member.ExpiresAt.Format(time.RFC3339Nano),
			RequestedAttachment: &comiswire.MCPManagedRunGroupResultMembersItemRequestedAttachment{
				Kind: string(member.RequestedAttachment.Kind), SourcePath: member.RequestedAttachment.SourcePath,
			},
		}
		if member.RequestedWorkspaceRoot != "" {
			item.RequestedWorkspace = &comiswire.MCPManagedRunGroupResultMembersItemRequestedWorkspace{
				RootHint: member.RequestedWorkspaceRoot,
			}
		}
		extension.Members = append(extension.Members, item)
	}
	encoded, err := json.Marshal(extension)
	if err != nil || comiswire.ValidatePayload(comiswire.PayloadMCPManagedRunGroup, encoded) != nil {
		return nil, internalResultFailure()
	}
	var metadata any
	if err := json.Unmarshal(encoded, &metadata); err != nil {
		return nil, internalResultFailure()
	}
	return metadata, nil
}
