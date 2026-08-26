package localapi

import (
	"context"
	"errors"

	"github.com/comisai/comis-dev-crew/internal/application"
	"github.com/comisai/comis-dev-crew/internal/domain"
)

// AddBacklogInput is bounded intake and carries no execution authority.
type AddBacklogInput struct {
	RepositoryID          string                  `json:"repositoryId"`
	Shape                 domain.TaskShape        `json:"shape"`
	RequestedOutcome      string                  `json:"requestedOutcome"`
	DependsOn             []string                `json:"dependsOn"`
	Priority              domain.BacklogPriority  `json:"priority"`
	Readiness             domain.BacklogReadiness `json:"readiness"`
	SourceConversationRef string                  `json:"sourceConversationRef"`
}

// PromoteBacklogInput completes the normal task contract for one ready item.
// Repository and shape remain absent because the durable item owns them.
type PromoteBacklogInput struct {
	BacklogHandle      string              `json:"backlogHandle"`
	BaseRevision       string              `json:"baseRevision"`
	AcceptanceCriteria []string            `json:"acceptanceCriteria"`
	Constraints        []string            `json:"constraints"`
	ValidationProfile  string              `json:"validationProfile"`
	DeliveryMode       domain.DeliveryMode `json:"deliveryMode"`
	WorkerProfileID    string              `json:"workerProfileId"`
}

// AddBacklogResult returns the exact durable request and parent version.
type AddBacklogResult struct {
	SchemaVersion int                `json:"schemaVersion"`
	OperationID   string             `json:"operationId"`
	Item          domain.BacklogItem `json:"item"`
	StateVersion  int64              `json:"stateVersion"`
	SideEffect    SideEffectClass    `json:"sideEffect"`
}

// PromoteBacklogResult carries the private preparation to the trusted MCP
// adapter while naming both the child task and parent promotion versions.
type PromoteBacklogResult struct {
	SchemaVersion    int                               `json:"schemaVersion"`
	OperationID      string                            `json:"operationId"`
	BacklogHandle    string                            `json:"backlogHandle"`
	Readiness        domain.BacklogReadiness           `json:"readiness"`
	TaskHandle       string                            `json:"taskHandle"`
	State            domain.TaskState                  `json:"state"`
	TaskStateVersion int64                             `json:"taskStateVersion"`
	StateVersion     int64                             `json:"stateVersion"`
	SideEffect       SideEffectClass                   `json:"sideEffect"`
	ManagedRun       application.ManagedRunPreparation `json:"managedRun"`
}

// DecodeAddBacklogInput reads one strict bounded operator intake contract.
func DecodeAddBacklogInput(data []byte) (AddBacklogInput, error) {
	var input AddBacklogInput
	if len(data) == 0 || len(data) > MaxRequestBytes {
		return AddBacklogInput{}, errors.New("backlog addition input exceeds its bound")
	}
	if err := decodeObject(data, &input); err != nil {
		return AddBacklogInput{}, err
	}
	return input, nil
}

// DecodePromoteBacklogInput reads a strict contract whose target is supplied
// separately by the visible command line.
func DecodePromoteBacklogInput(data []byte) (PromoteBacklogInput, error) {
	var input PromoteBacklogInput
	if len(data) == 0 || len(data) > MaxRequestBytes {
		return PromoteBacklogInput{}, errors.New("backlog promotion input exceeds its bound")
	}
	if err := decodeObject(data, &input); err != nil {
		return PromoteBacklogInput{}, err
	}
	if input.BacklogHandle != "" {
		return PromoteBacklogInput{}, errors.New("backlog promotion contract must not name its own item")
	}
	return input, nil
}

// AddBacklog records one bounded request through the canonical local service.
func (client *Client) AddBacklog(
	ctx context.Context,
	operationID string,
	input AddBacklogInput,
) (AddBacklogResult, error) {
	var result AddBacklogResult
	err := client.call(ctx, operationID, MethodAddBacklog, input, &result)
	return result, err
}

// PromoteBacklog creates a normally prepared task from one ready request.
func (client *Client) PromoteBacklog(
	ctx context.Context,
	operationID string,
	input PromoteBacklogInput,
) (PromoteBacklogResult, error) {
	var result PromoteBacklogResult
	err := client.call(ctx, operationID, MethodPromoteBacklog, input, &result)
	return result, err
}

func (handler *Handler) dispatchBacklogMutation(ctx context.Context, request Request) (Outcome, bool) {
	switch request.Method {
	case MethodAddBacklog:
		var input AddBacklogInput
		if err := decodeObject(request.Payload, &input); err != nil {
			return invalidPayload(request.OperationID, err), true
		}
		if handler.backlogAdditions == nil {
			return backlogMutationUnavailable(request.OperationID), true
		}
		result, err := handler.backlogAdditions.AddBacklog(ctx, application.BacklogAdditionCommand{
			OperationID: request.OperationID, RepositoryID: input.RepositoryID, Shape: input.Shape,
			RequestedOutcome: input.RequestedOutcome, DependsOn: append([]string(nil), input.DependsOn...),
			Priority: input.Priority, Readiness: input.Readiness,
			SourceConversationRef: input.SourceConversationRef,
		})
		return handler.addBacklogOutcome(request.OperationID, result, err), true
	case MethodPromoteBacklog:
		var input PromoteBacklogInput
		if err := decodeObject(request.Payload, &input); err != nil {
			return invalidPayload(request.OperationID, err), true
		}
		if handler.backlogPromotions == nil {
			return backlogMutationUnavailable(request.OperationID), true
		}
		result, err := handler.backlogPromotions.PromoteBacklog(ctx, application.BacklogPromotionCommand{
			OperationID: request.OperationID, ServiceInstanceID: handler.serviceInstanceID,
			BacklogHandle: input.BacklogHandle, BaseRevision: input.BaseRevision,
			AcceptanceCriteria: append([]string(nil), input.AcceptanceCriteria...),
			Constraints:        append([]string(nil), input.Constraints...), ValidationProfile: input.ValidationProfile,
			DeliveryMode: input.DeliveryMode, WorkerProfileID: input.WorkerProfileID,
		})
		return handler.promoteBacklogOutcome(request.OperationID, result, err), true
	default:
		return Outcome{}, false
	}
}

func backlogMutationUnavailable(operationID string) Outcome {
	return rejectedOutcome(
		operationID, domain.ErrorUnavailable, true,
		"backlog mutation service is unavailable", "inspect service configuration", nil,
	)
}

func (handler *Handler) addBacklogOutcome(
	operationID string,
	mutation application.BacklogAdditionResult,
	err error,
) Outcome {
	if err != nil {
		return outcomeFromError(operationID, err)
	}
	if mutation.Item.Validate() != nil || mutation.Operation.Validate() != nil ||
		mutation.Operation.ID != operationID || mutation.Operation.Command != string(MethodAddBacklog) ||
		mutation.Operation.Status != domain.OperationCompleted || mutation.Operation.ResultRef != mutation.Item.Handle ||
		mutation.Operation.StateVersion < 1 {
		return rejectedOutcome(operationID, domain.ErrorInternal, false,
			"backlog addition outcome is incomplete", "inspect durable service state", nil)
	}
	result := AddBacklogResult{
		SchemaVersion: 1, OperationID: operationID, Item: mutation.Item,
		StateVersion: mutation.Operation.StateVersion, SideEffect: MethodAddBacklog.SideEffect(),
	}
	return queryOutcome(operationID, result.StateVersion, result, nil)
}

func (handler *Handler) promoteBacklogOutcome(
	operationID string,
	mutation application.BacklogPromotionResult,
	err error,
) Outcome {
	if err != nil {
		return outcomeFromError(operationID, err)
	}
	if mutation.Item.Validate() != nil || mutation.Item.Readiness != domain.BacklogPromoted ||
		mutation.Task.Handle == "" || mutation.Task.State != domain.TaskPrepared || mutation.Task.StateVersion < 1 ||
		mutation.Task.RepositoryID != mutation.Item.RepositoryID || mutation.Task.Shape != mutation.Item.Shape ||
		mutation.Preparation == nil || mutation.Preparation.ExternalRunRef != mutation.Task.Handle ||
		mutation.Preparation.Validate(handler.clock()) != nil || mutation.Operation.Validate() != nil ||
		mutation.Operation.ID != operationID || mutation.Operation.Command != string(MethodPromoteBacklog) ||
		mutation.Operation.Status != domain.OperationCompleted || mutation.Operation.ResultRef != mutation.Task.Handle ||
		mutation.Operation.StateVersion <= mutation.Task.StateVersion {
		return rejectedOutcome(operationID, domain.ErrorInternal, false,
			"backlog promotion outcome is incomplete", "inspect durable service state", nil)
	}
	result := PromoteBacklogResult{
		SchemaVersion: 1, OperationID: operationID, BacklogHandle: mutation.Item.Handle,
		Readiness: mutation.Item.Readiness, TaskHandle: mutation.Task.Handle, State: mutation.Task.State,
		TaskStateVersion: mutation.Task.StateVersion, StateVersion: mutation.Operation.StateVersion,
		SideEffect: MethodPromoteBacklog.SideEffect(), ManagedRun: *mutation.Preparation,
	}
	return queryOutcome(operationID, result.StateVersion, result, nil)
}
