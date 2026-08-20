package localapi

import (
	"context"
	"sort"

	"github.com/comisai/comis-dev-crew/internal/application"
	"github.com/comisai/comis-dev-crew/internal/domain"
)

// InitiativeControlInput names one initiative and cannot select its members.
type InitiativeControlInput struct {
	InitiativeHandle string `json:"initiativeHandle"`
}

// InitiativeControlResult is the versioned operator group-control projection.
type InitiativeControlResult struct {
	SchemaVersion    int                                         `json:"schemaVersion"`
	OperationID      string                                      `json:"operationId"`
	InitiativeHandle string                                      `json:"initiativeHandle"`
	State            domain.InitiativeState                      `json:"state"`
	StateVersion     int64                                       `json:"stateVersion"`
	SideEffect       SideEffectClass                             `json:"sideEffect"`
	Members          []application.InitiativeControlMemberResult `json:"members"`
}

// PauseInitiative asks every durable member to pause independently.
func (client *Client) PauseInitiative(
	ctx context.Context,
	operationID string,
	input InitiativeControlInput,
) (InitiativeControlResult, error) {
	return client.controlInitiative(ctx, operationID, MethodPauseInitiative, input)
}

// ResumeInitiative asks every durable member to resume independently.
func (client *Client) ResumeInitiative(
	ctx context.Context,
	operationID string,
	input InitiativeControlInput,
) (InitiativeControlResult, error) {
	return client.controlInitiative(ctx, operationID, MethodResumeInitiative, input)
}

// CancelInitiative cancels every durable member independently.
func (client *Client) CancelInitiative(
	ctx context.Context,
	operationID string,
	input InitiativeControlInput,
) (InitiativeControlResult, error) {
	return client.controlInitiative(ctx, operationID, MethodCancelInitiative, input)
}

func (client *Client) controlInitiative(
	ctx context.Context,
	operationID string,
	method Method,
	input InitiativeControlInput,
) (InitiativeControlResult, error) {
	var result InitiativeControlResult
	err := client.call(ctx, operationID, method, input, &result)
	return result, err
}

func (handler *Handler) dispatchInitiativeControl(ctx context.Context, request Request) (Outcome, bool) {
	var invoke func(context.Context, application.InitiativeControlCommand) (application.InitiativeControlResult, error)
	switch request.Method {
	case MethodPauseInitiative:
		if handler.initiativeControls != nil {
			invoke = handler.initiativeControls.PauseInitiative
		}
	case MethodResumeInitiative:
		if handler.initiativeControls != nil {
			invoke = handler.initiativeControls.ResumeInitiative
		}
	case MethodCancelInitiative:
		if handler.initiativeControls != nil {
			invoke = handler.initiativeControls.CancelInitiative
		}
	default:
		return Outcome{}, false
	}
	var input InitiativeControlInput
	if err := decodeObject(request.Payload, &input); err != nil {
		return invalidPayload(request.OperationID, err), true
	}
	if invoke == nil {
		return rejectedOutcome(
			request.OperationID, domain.ErrorUnavailable, true,
			"initiative control service is unavailable", "inspect service configuration", nil,
		), true
	}
	result, err := invoke(ctx, application.InitiativeControlCommand{
		OperationID: request.OperationID, InitiativeHandle: input.InitiativeHandle,
	})
	return initiativeControlOutcome(request.OperationID, request.Method, result, err), true
}

func initiativeControlOutcome(
	operationID string,
	method Method,
	result application.InitiativeControlResult,
	err error,
) Outcome {
	if err != nil {
		return outcomeFromError(operationID, err)
	}
	if result.Operation.Validate() != nil || domain.ValidateInitiativeState(result.State) != nil ||
		result.Operation.ID != operationID ||
		result.Operation.Command != string(method) || result.Operation.Status != domain.OperationCompleted ||
		result.Operation.ResultRef != result.InitiativeHandle ||
		result.Operation.StateVersion != result.StateVersion || !validInitiativeControlMembers(result.Members) {
		return rejectedOutcome(
			operationID, domain.ErrorInternal, false,
			"initiative control outcome is incomplete", "inspect durable service state", nil,
		)
	}
	projection := InitiativeControlResult{
		SchemaVersion: 1, OperationID: operationID, InitiativeHandle: result.InitiativeHandle,
		State: result.State, StateVersion: result.StateVersion,
		SideEffect: method.SideEffect(), Members: result.Members,
	}
	return queryOutcome(operationID, projection.StateVersion, projection, nil)
}

func validInitiativeControlMembers(members []application.InitiativeControlMemberResult) bool {
	if len(members) == 0 || len(members) > 64 || !sort.SliceIsSorted(members, func(left, right int) bool {
		return members[left].TaskHandle < members[right].TaskHandle
	}) {
		return false
	}
	seen := make(map[string]struct{}, len(members))
	for _, member := range members {
		if domain.ValidateTaskHandle(member.TaskHandle) != nil ||
			domain.ValidateOperationID(member.OperationID) != nil ||
			domain.ValidateTaskState(member.State) != nil || member.StateVersion < 1 {
			return false
		}
		switch member.Outcome {
		case application.InitiativeControlCompleted:
			if member.ErrorCode != "" {
				return false
			}
		case application.InitiativeControlRejected, application.InitiativeControlUnknown,
			application.InitiativeControlNotAttempted:
			if !member.ErrorCode.Valid() {
				return false
			}
		default:
			return false
		}
		if _, exists := seen[member.TaskHandle]; exists {
			return false
		}
		seen[member.TaskHandle] = struct{}{}
	}
	return true
}
