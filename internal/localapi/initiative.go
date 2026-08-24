package localapi

import (
	"context"

	"github.com/comisai/comis-dev-crew/internal/application"
	"github.com/comisai/comis-dev-crew/internal/domain"
)

// PrepareInitiativeInput carries a complete caller-local graph. Operation and
// service identities are absent because the boundary derives both itself.
type PrepareInitiativeInput struct {
	TitleRef             string                                          `json:"titleRef"`
	BaseRevisionSet      []domain.InitiativeBaseRevision                 `json:"baseRevisionSet"`
	Components           []application.PrepareInitiativeComponent        `json:"components"`
	Edges                []application.PrepareInitiativeEdge             `json:"edges"`
	ContractArtifacts    []application.PrepareInitiativeContractArtifact `json:"contractArtifacts"`
	IntegrationPolicyID  string                                          `json:"integrationPolicyId"`
	IntegrationOwnerTask string                                          `json:"integrationOwnerTask,omitempty"`
}

// ListInitiativesInput optionally scopes initiatives by their closed state.
type ListInitiativesInput struct {
	State domain.InitiativeState `json:"state,omitempty"`
}

// ListBacklogInput scopes bounded requests without carrying run authority.
type ListBacklogInput struct {
	RepositoryID string                  `json:"repositoryId,omitempty"`
	Readiness    domain.BacklogReadiness `json:"readiness,omitempty"`
}

type getInitiativeInput struct {
	InitiativeHandle string `json:"initiativeHandle"`
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

// ListInitiatives reads the versioned initiative list.
func (client *Client) ListInitiatives(
	ctx context.Context,
	operationID string,
	input ListInitiativesInput,
) (application.InitiativeList, error) {
	var result application.InitiativeList
	err := client.call(ctx, operationID, MethodListInitiatives, input, &result)
	return result, err
}

// GetInitiative reads one detailed initiative graph and its safe actions.
func (client *Client) GetInitiative(
	ctx context.Context,
	operationID string,
	initiativeHandle string,
) (application.InitiativeDetail, error) {
	var result application.InitiativeDetail
	err := client.call(ctx, operationID, MethodGetInitiative, getInitiativeInput{
		InitiativeHandle: initiativeHandle,
	}, &result)
	return result, err
}

// ListBacklog reads bounded requests under an optional repository/readiness scope.
func (client *Client) ListBacklog(
	ctx context.Context,
	operationID string,
	input ListBacklogInput,
) (application.BacklogList, error) {
	var result application.BacklogList
	err := client.call(ctx, operationID, MethodListBacklog, input, &result)
	return result, err
}

func (handler *Handler) dispatchInitiative(ctx context.Context, request Request) (Outcome, bool) {
	if outcome, handled := handler.dispatchInitiativeControl(ctx, request); handled {
		return outcome, true
	}
	switch request.Method {
	case MethodListInitiatives:
		var input ListInitiativesInput
		if err := decodeObject(request.Payload, &input); err != nil {
			return invalidPayload(request.OperationID, err), true
		}
		if handler.initiativeQueries == nil {
			return initiativeReadUnavailable(request.OperationID), true
		}
		result, err := handler.initiativeQueries.ListInitiatives(ctx, input.State)
		return queryOutcome(request.OperationID, result.StateVersion, result, err), true
	case MethodGetInitiative:
		var input getInitiativeInput
		if err := decodeObject(request.Payload, &input); err != nil {
			return invalidPayload(request.OperationID, err), true
		}
		if handler.initiativeQueries == nil {
			return initiativeReadUnavailable(request.OperationID), true
		}
		result, err := handler.initiativeQueries.GetInitiative(ctx, input.InitiativeHandle)
		return queryOutcome(request.OperationID, result.StateVersion, result, err), true
	case MethodListBacklog:
		var input ListBacklogInput
		if err := decodeObject(request.Payload, &input); err != nil {
			return invalidPayload(request.OperationID, err), true
		}
		if handler.initiativeQueries == nil {
			return initiativeReadUnavailable(request.OperationID), true
		}
		result, err := handler.initiativeQueries.ListBacklog(ctx, application.BacklogFilter(input))
		return queryOutcome(request.OperationID, result.StateVersion, result, err), true
	case MethodPrepareInitiative:
		return handler.dispatchPrepareInitiative(ctx, request), true
	default:
		return Outcome{}, false
	}
}

func (handler *Handler) dispatchPrepareInitiative(ctx context.Context, request Request) Outcome {
	var input PrepareInitiativeInput
	if err := decodeObject(request.Payload, &input); err != nil {
		return invalidPayload(request.OperationID, err)
	}
	if handler.initiativeMutations == nil {
		return rejectedOutcome(
			request.OperationID, domain.ErrorUnavailable, true,
			"initiative mutation service is unavailable", "inspect service configuration", nil,
		)
	}
	result, err := handler.initiativeMutations.PrepareInitiative(ctx, application.PrepareInitiativeCommand{
		OperationID: request.OperationID, ServiceInstanceID: handler.serviceInstanceID,
		TitleRef: input.TitleRef, BaseRevisionSet: input.BaseRevisionSet,
		Components: input.Components, Edges: input.Edges,
		ContractArtifacts: input.ContractArtifacts, IntegrationPolicyID: input.IntegrationPolicyID,
		IntegrationOwnerTask: input.IntegrationOwnerTask,
	})
	return handler.prepareInitiativeOutcome(request.OperationID, result, err)
}

func initiativeReadUnavailable(operationID string) Outcome {
	return rejectedOutcome(
		operationID, domain.ErrorUnavailable, true,
		"initiative query service is unavailable", "inspect service configuration", nil,
	)
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
			preparations[index].ExternalRunRef != task.Handle ||
			preparations[index].State != application.PreparationOpen {
			return false
		}
		if _, exists := seen[task.Handle]; exists {
			return false
		}
		seen[task.Handle] = struct{}{}
	}
	return true
}
