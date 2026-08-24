package mcpadapter

import (
	"context"
	"errors"

	"github.com/comisai/comis-dev-crew/internal/domain"
	"github.com/comisai/comis-dev-crew/internal/localapi"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// AddBacklogInput is model-visible bounded intake. Provenance is deliberately
// absent and comes from the authenticated MCP call context.
type AddBacklogInput struct {
	RepositoryID     string                  `json:"repositoryId" jsonschema:"operator-configured repository catalog identity"`
	Shape            domain.TaskShape        `json:"shape" jsonschema:"task shape; use exactly ship or scout"`
	RequestedOutcome string                  `json:"requestedOutcome" jsonschema:"bounded desired outcome for later task preparation"`
	DependsOn        []string                `json:"dependsOn" jsonschema:"existing backlog handles that must be promoted first; use an empty JSON array when there are none"`
	Priority         domain.BacklogPriority  `json:"priority" jsonschema:"use exactly low, normal, or high"`
	Readiness        domain.BacklogReadiness `json:"readiness" jsonschema:"use exactly ready or needs_refinement"`
}

func (input AddBacklogInput) local(sourceConversationRef string) localapi.AddBacklogInput {
	return localapi.AddBacklogInput{
		RepositoryID: input.RepositoryID, Shape: input.Shape, RequestedOutcome: input.RequestedOutcome,
		DependsOn: append([]string(nil), input.DependsOn...), Priority: input.Priority,
		Readiness: input.Readiness, SourceConversationRef: sourceConversationRef,
	}
}

// PromoteBacklogInput completes a ready item's task contract without exposing
// repository, shape, or host authority.
type PromoteBacklogInput struct {
	BacklogHandle      string              `json:"backlogHandle" jsonschema:"opaque handle of the ready backlog item"`
	BaseRevision       string              `json:"baseRevision" jsonschema:"exact 40-character lowercase hexadecimal Git revision"`
	AcceptanceCriteria []string            `json:"acceptanceCriteria" jsonschema:"ordered criteria appended after the backlog requested outcome"`
	Constraints        []string            `json:"constraints" jsonschema:"ordered task constraints; use an empty JSON array when there are none"`
	ValidationProfile  string              `json:"validationProfile" jsonschema:"operator-configured validation profile identity"`
	DeliveryMode       domain.DeliveryMode `json:"deliveryMode" jsonschema:"use a change-bearing delivery mode for ship or report for scout"`
	WorkerProfileID    string              `json:"workerProfileId" jsonschema:"operator-configured worker profile identity"`
}

func (input PromoteBacklogInput) local() localapi.PromoteBacklogInput {
	return localapi.PromoteBacklogInput{
		BacklogHandle: input.BacklogHandle, BaseRevision: input.BaseRevision,
		AcceptanceCriteria: append([]string(nil), input.AcceptanceCriteria...),
		Constraints:        append([]string(nil), input.Constraints...), ValidationProfile: input.ValidationProfile,
		DeliveryMode: input.DeliveryMode, WorkerProfileID: input.WorkerProfileID,
	}
}

// AddBacklogOutput omits private conversation provenance.
type AddBacklogOutput struct {
	SchemaVersion int                      `json:"schemaVersion"`
	OperationID   string                   `json:"operationId"`
	BacklogHandle string                   `json:"backlogHandle"`
	RepositoryID  string                   `json:"repositoryId"`
	Shape         domain.TaskShape         `json:"shape"`
	Readiness     domain.BacklogReadiness  `json:"readiness"`
	StateVersion  int64                    `json:"stateVersion"`
	SideEffect    localapi.SideEffectClass `json:"sideEffect"`
}

// PromoteBacklogOutput omits the private preparation carried in result meta.
type PromoteBacklogOutput struct {
	SchemaVersion    int                      `json:"schemaVersion"`
	OperationID      string                   `json:"operationId"`
	BacklogHandle    string                   `json:"backlogHandle"`
	Readiness        domain.BacklogReadiness  `json:"readiness"`
	TaskHandle       string                   `json:"taskHandle"`
	State            domain.TaskState         `json:"state"`
	TaskStateVersion int64                    `json:"taskStateVersion"`
	StateVersion     int64                    `json:"stateVersion"`
	SideEffect       localapi.SideEffectClass `json:"sideEffect"`
}

func (facade *Facade) addBacklog(
	ctx context.Context,
	request *mcp.CallToolRequest,
	input AddBacklogInput,
) (*mcp.CallToolResult, AddBacklogOutput, error) {
	callContext, err := facade.authorize(request)
	if err != nil {
		return nil, AddBacklogOutput{}, err
	}
	operationID := string(callContext.OperationID)
	localInput := input.local(callContext.ConversationRef)
	added, err := facade.client.AddBacklog(ctx, operationID, localInput)
	if err != nil && uncertainMutation(ctx, err) {
		added, err = facade.reconcileBacklogAddition(ctx, operationID, localInput, err)
	}
	if err != nil {
		return nil, AddBacklogOutput{}, err
	}
	if added.SchemaVersion != 1 || added.OperationID != operationID || added.Item.Handle == "" ||
		added.Item.RepositoryID != input.RepositoryID || added.Item.Shape != input.Shape ||
		added.Item.SourceConversationRef != callContext.ConversationRef || added.StateVersion < 1 ||
		added.SideEffect != localapi.SideEffectMutate {
		return nil, AddBacklogOutput{}, internalResultFailure()
	}
	return nil, AddBacklogOutput{
		SchemaVersion: added.SchemaVersion, OperationID: added.OperationID, BacklogHandle: added.Item.Handle,
		RepositoryID: added.Item.RepositoryID, Shape: added.Item.Shape, Readiness: added.Item.Readiness,
		StateVersion: added.StateVersion, SideEffect: added.SideEffect,
	}, nil
}

func (facade *Facade) promoteBacklog(
	ctx context.Context,
	request *mcp.CallToolRequest,
	input PromoteBacklogInput,
) (*mcp.CallToolResult, PromoteBacklogOutput, error) {
	callContext, err := facade.authorize(request)
	if err != nil {
		return nil, PromoteBacklogOutput{}, err
	}
	operationID := string(callContext.OperationID)
	localInput := input.local()
	promoted, err := facade.client.PromoteBacklog(ctx, operationID, localInput)
	if err != nil && uncertainMutation(ctx, err) {
		promoted, err = facade.reconcileBacklogPromotion(ctx, operationID, localInput, err)
	}
	if err != nil {
		return nil, PromoteBacklogOutput{}, err
	}
	if promoted.SchemaVersion != 1 || promoted.OperationID != operationID ||
		promoted.BacklogHandle != input.BacklogHandle || promoted.Readiness != domain.BacklogPromoted ||
		promoted.TaskHandle == "" || promoted.State != domain.TaskPrepared || promoted.TaskStateVersion < 1 ||
		promoted.StateVersion <= promoted.TaskStateVersion || promoted.SideEffect != localapi.SideEffectMutate {
		return nil, PromoteBacklogOutput{}, internalResultFailure()
	}
	metadata, err := preparationMetadata(operationID, localapi.PrepareTaskResult{
		SchemaVersion: promoted.SchemaVersion, OperationID: promoted.OperationID,
		TaskHandle: promoted.TaskHandle, State: promoted.State, StateVersion: promoted.TaskStateVersion,
		SideEffect: promoted.SideEffect, ManagedRun: promoted.ManagedRun,
	})
	if err != nil {
		return nil, PromoteBacklogOutput{}, err
	}
	return &mcp.CallToolResult{Meta: mcp.Meta{ManagedRunResultMetaKey: metadata}}, PromoteBacklogOutput{
		SchemaVersion: promoted.SchemaVersion, OperationID: promoted.OperationID,
		BacklogHandle: promoted.BacklogHandle, Readiness: promoted.Readiness,
		TaskHandle: promoted.TaskHandle, State: promoted.State, TaskStateVersion: promoted.TaskStateVersion,
		StateVersion: promoted.StateVersion, SideEffect: promoted.SideEffect,
	}, nil
}

func (facade *Facade) reconcileBacklogAddition(
	ctx context.Context,
	operationID string,
	input localapi.AddBacklogInput,
	original error,
) (localapi.AddBacklogResult, error) {
	if ctx == nil {
		return localapi.AddBacklogResult{}, original
	}
	reconcileContext, cancel := context.WithTimeout(context.WithoutCancel(ctx), facade.reconcileTimeout)
	defer cancel()
	requestID, err := facade.newOperationID()
	if err != nil || domain.ValidateOperationID(requestID) != nil {
		return localapi.AddBacklogResult{}, original
	}
	operation, err := facade.client.Operation(reconcileContext, requestID, operationID)
	if err != nil || operation.OperationID != operationID || operation.Command != "AddBacklog" {
		return localapi.AddBacklogResult{}, original
	}
	switch operation.Status {
	case domain.OperationCompleted:
		return facade.client.AddBacklog(reconcileContext, operationID, input)
	case domain.OperationRejected:
		if operation.ErrorCode.Valid() {
			return localapi.AddBacklogResult{}, safeFailure(
				operation.ErrorCode, false, "backlog addition was rejected",
				"correct the bounded request before retrying",
			)
		}
		return localapi.AddBacklogResult{}, original
	case domain.OperationAccepted, domain.OperationUnknown:
		return localapi.AddBacklogResult{}, original
	default:
		return localapi.AddBacklogResult{}, errors.New("unknown backlog addition reconciliation status")
	}
}

func (facade *Facade) reconcileBacklogPromotion(
	ctx context.Context,
	operationID string,
	input localapi.PromoteBacklogInput,
	original error,
) (localapi.PromoteBacklogResult, error) {
	if ctx == nil {
		return localapi.PromoteBacklogResult{}, original
	}
	reconcileContext, cancel := context.WithTimeout(context.WithoutCancel(ctx), facade.reconcileTimeout)
	defer cancel()
	requestID, err := facade.newOperationID()
	if err != nil || domain.ValidateOperationID(requestID) != nil {
		return localapi.PromoteBacklogResult{}, original
	}
	operation, err := facade.client.Operation(reconcileContext, requestID, operationID)
	if err != nil || operation.OperationID != operationID || operation.Command != "PromoteBacklog" {
		return localapi.PromoteBacklogResult{}, original
	}
	switch operation.Status {
	case domain.OperationCompleted:
		return facade.client.PromoteBacklog(reconcileContext, operationID, input)
	case domain.OperationRejected:
		if operation.ErrorCode.Valid() {
			return localapi.PromoteBacklogResult{}, safeFailure(
				operation.ErrorCode, false, "backlog promotion was rejected",
				"correct the promotion contract before retrying",
			)
		}
		return localapi.PromoteBacklogResult{}, original
	case domain.OperationAccepted, domain.OperationUnknown:
		return localapi.PromoteBacklogResult{}, original
	default:
		return localapi.PromoteBacklogResult{}, errors.New("unknown backlog promotion reconciliation status")
	}
}
