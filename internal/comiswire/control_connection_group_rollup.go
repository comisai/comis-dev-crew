package comiswire

import (
	"context"
	"errors"
	"fmt"

	"github.com/comisai/comis-dev-crew/internal/application"
)

// GroupHostRollup reads one content-free host projection over the current
// authenticated connection. The caller must compare its exact member identity
// and state counts before restoring any local initiative authority.
func (connection *ControlConnection) GroupHostRollup(
	ctx context.Context,
	params GroupGetHostRollupRequestParams,
) (GroupGetHostRollupResponseResult, error) {
	if ctx == nil {
		return GroupGetHostRollupResponseResult{}, errors.New("read group rollup from Comis: context is required")
	}
	request := GroupGetHostRollupRequest{
		JSONRPC: JSONRPCVersion, ID: params.OperationID,
		Method: MethodManagedRunGroupsGetHostRollup, Params: params,
	}
	if err := validateGeneratedDocument(schemaGroupGetHostRollupRequest, request); err != nil {
		return GroupGetHostRollupResponseResult{}, fmt.Errorf("read group rollup from Comis: invalid request: %w", err)
	}
	session, err := connection.awaitSession(ctx)
	if err != nil {
		return GroupGetHostRollupResponseResult{}, err
	}
	var response GroupGetHostRollupResponse
	authenticated := authenticatedGroupGetHostRollupRequest{
		GroupGetHostRollupRequest: request, Bearer: connection.config.Credential,
	}
	if err := connection.invoke(ctx, session, request.Method, authenticated, params.OperationID, &response); err != nil {
		return GroupGetHostRollupResponseResult{}, fmt.Errorf("read group rollup from Comis: outcome uncertain: %w", err)
	}
	if err := validateGeneratedDocument(schemaGroupGetHostRollupResponse, response); err != nil {
		return GroupGetHostRollupResponseResult{}, fmt.Errorf("read group rollup from Comis: invalid response: %w", err)
	}
	if response.Result.ManagedRunGroupID != params.ManagedRunGroupID {
		return GroupGetHostRollupResponseResult{}, errors.New("read group rollup from Comis: acknowledgement identity differs")
	}
	return response.Result, nil
}

// ReadInitiativeHostRollup maps the generated wire DTO onto the application
// port without letting protocol types cross into recovery policy.
func (connection *ControlConnection) ReadInitiativeHostRollup(
	ctx context.Context,
	request application.InitiativeHostRollupRequest,
) (application.InitiativeHostRollup, error) {
	result, err := connection.GroupHostRollup(ctx, GroupGetHostRollupRequestParams{
		OperationID: OperationID(request.OperationID), ManagedRunGroupID: ManagedRunGroupID(request.ManagedRunGroupID),
	})
	if err != nil {
		return application.InitiativeHostRollup{}, err
	}
	counts, err := applicationHostStateCounts(result.StateCounts)
	if err != nil {
		return application.InitiativeHostRollup{}, err
	}
	rollup := application.InitiativeHostRollup{
		ManagedRunGroupID:   string(result.ManagedRunGroupID),
		MemberManagedRunIDs: append([]string(nil), result.MemberManagedRunIds...),
		StateCounts:         counts, AttentionCount: int(result.AttentionCount),
		ActiveCustodyCount: int(result.ActiveCustodyCount), UpdatedAtMs: result.UpdatedAtMs,
	}
	if err := rollup.Validate(); err != nil {
		return application.InitiativeHostRollup{}, fmt.Errorf("read group rollup from Comis: application projection is invalid: %w", err)
	}
	return rollup, nil
}

func applicationHostStateCounts(
	counts GroupGetHostRollupResponseResultStateCounts,
) (application.InitiativeHostStateCounts, error) {
	values := []*int64{
		counts.Preparing, counts.Active, counts.Waiting, counts.Paused, counts.CandidateComplete,
		counts.Succeeded, counts.Failed, counts.Cancelled, counts.Unknown,
	}
	converted := make([]int, len(values))
	for index, value := range values {
		if value == nil {
			continue
		}
		if *value < 0 || *value > 16 {
			return application.InitiativeHostStateCounts{}, errors.New("read group rollup from Comis: state count exceeds group bounds")
		}
		converted[index] = int(*value)
	}
	return application.InitiativeHostStateCounts{
		Preparing: converted[0], Active: converted[1], Waiting: converted[2], Paused: converted[3],
		CandidateComplete: converted[4], Succeeded: converted[5], Failed: converted[6],
		Cancelled: converted[7], Unknown: converted[8],
	}, nil
}
