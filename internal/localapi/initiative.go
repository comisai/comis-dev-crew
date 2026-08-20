package localapi

import (
	"context"

	"github.com/comisai/comis-dev-crew/internal/application"
	"github.com/comisai/comis-dev-crew/internal/domain"
)

// PrepareInitiativeInput carries a complete caller-local graph. Operation and
// service identities are absent because the boundary derives both itself.
type PrepareInitiativeInput struct {
	TitleRef             string                                   `json:"titleRef"`
	BaseRevisionSet      []domain.InitiativeBaseRevision          `json:"baseRevisionSet"`
	Components           []application.PrepareInitiativeComponent `json:"components"`
	Edges                []application.PrepareInitiativeEdge      `json:"edges"`
	ContractArtifacts    []string                                 `json:"contractArtifacts"`
	IntegrationPolicyID  string                                   `json:"integrationPolicyId"`
	IntegrationOwnerTask string                                   `json:"integrationOwnerTask,omitempty"`
}

// PrepareInitiativeResult returns the complete private two-phase group join.
// The MCP facade forwards it to Comis without interpreting member authority.
type PrepareInitiativeResult struct {
	SchemaVersion    int                                    `json:"schemaVersion"`
	OperationID      string                                 `json:"operationId"`
	InitiativeHandle string                                 `json:"initiativeHandle"`
	State            domain.InitiativeState                 `json:"state"`
	StateVersion     int64                                  `json:"stateVersion"`
	SideEffect       SideEffectClass                        `json:"sideEffect"`
	TaskHandles      []string                               `json:"taskHandles"`
	ManagedRunGroup  application.ManagedRunGroupPreparation `json:"managedRunGroup"`
}

// PrepareInitiative executes the canonical group preparation over the local service.
func (client *Client) PrepareInitiative(
	ctx context.Context,
	operationID string,
	input PrepareInitiativeInput,
) (PrepareInitiativeResult, error) {
	var result PrepareInitiativeResult
	err := client.call(ctx, operationID, MethodPrepareInitiative, input, &result)
	return result, err
}

func (handler *Handler) dispatchInitiative(ctx context.Context, request Request) (Outcome, bool) {
	if request.Method != MethodPrepareInitiative {
		return Outcome{}, false
	}
	var input PrepareInitiativeInput
	if err := decodeObject(request.Payload, &input); err != nil {
		return invalidPayload(request.OperationID, err), true
	}
	if handler.initiativeMutations == nil {
		return rejectedOutcome(
			request.OperationID, domain.ErrorUnavailable, true,
			"initiative mutation service is unavailable", "inspect service configuration", nil,
		), true
	}
	result, err := handler.initiativeMutations.PrepareInitiative(ctx, application.PrepareInitiativeCommand{
		OperationID: request.OperationID, ServiceInstanceID: handler.serviceInstanceID,
		TitleRef: input.TitleRef, BaseRevisionSet: input.BaseRevisionSet,
		Components: input.Components, Edges: input.Edges,
		ContractArtifacts: input.ContractArtifacts, IntegrationPolicyID: input.IntegrationPolicyID,
		IntegrationOwnerTask: input.IntegrationOwnerTask,
	})
	return handler.prepareInitiativeOutcome(request.OperationID, result, err), true
}

func (handler *Handler) prepareInitiativeOutcome(
	operationID string,
	mutation application.InitiativePreparationResult,
	err error,
) Outcome {
	if err != nil {
		return outcomeFromError(operationID, err)
	}
	if mutation.Initiative.Handle == "" || mutation.Initiative.State != domain.InitiativePreparing ||
		mutation.Initiative.StateVersion <= 0 || mutation.Operation.ID != operationID ||
		mutation.Operation.Command != string(MethodPrepareInitiative) ||
		mutation.Operation.Status != domain.OperationCompleted ||
		mutation.Operation.ResultRef != mutation.Initiative.Handle ||
		mutation.Operation.StateVersion != mutation.Initiative.StateVersion ||
		mutation.Preparation.ExternalGroupRef != mutation.Initiative.Handle ||
		mutation.Preparation.Validate(handler.clock()) != nil ||
		!initiativeMembersMatch(mutation.Tasks, mutation.Preparation.Members, mutation.Initiative.StateVersion) {
		return rejectedOutcome(operationID, domain.ErrorInternal, false,
			"initiative mutation outcome is incomplete", "inspect durable service state", nil)
	}
	taskHandles := make([]string, len(mutation.Tasks))
	for index, task := range mutation.Tasks {
		taskHandles[index] = task.Handle
	}
	result := PrepareInitiativeResult{
		SchemaVersion: 1, OperationID: operationID, InitiativeHandle: mutation.Initiative.Handle,
		State: mutation.Initiative.State, StateVersion: mutation.Initiative.StateVersion,
		SideEffect: MethodPrepareInitiative.SideEffect(), TaskHandles: taskHandles,
		ManagedRunGroup: mutation.Preparation,
	}
	return queryOutcome(operationID, result.StateVersion, result, nil)
}

func initiativeMembersMatch(
	tasks []domain.Task,
	preparations []application.ManagedRunPreparation,
	stateVersion int64,
) bool {
	if len(tasks) == 0 || len(tasks) != len(preparations) {
		return false
	}
	seen := make(map[string]struct{}, len(tasks))
	for index, task := range tasks {
		if task.Handle == "" || task.State != domain.TaskPrepared || task.StateVersion != stateVersion ||
			preparations[index].ExternalRunRef != task.Handle {
			return false
		}
		if _, exists := seen[task.Handle]; exists {
			return false
		}
		seen[task.Handle] = struct{}{}
	}
	return true
}
