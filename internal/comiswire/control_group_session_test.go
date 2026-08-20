package comiswire

import (
	"context"
	"reflect"
	"testing"
)

func TestControlSessionDispatchesAuthenticatedManagedRunGroupActivation(t *testing.T) {
	called := false
	lease := WorkspaceLeaseID("workspace-lease_group-member-a")
	attachment := ExecutionAttachmentID("execution-attachment_group-member-a")
	target := AttachmentTargetName("attachment-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa.sock")
	params := GroupActivateRequestParams{
		OperationID: "operation_group_activate", ManagedRunGroupID: "managed-run-group_a",
		RegistrationNonce: "group-registration-nonce_a",
		Members: []GroupActivateRequestParamsMembersItem{{
			ManagedRunID: "managed-run_group-member-a", ExternalRunRef: "task-group-member-a",
			RegistrationNonce:     "registration-nonce_group-member-a",
			WorkspaceLeaseID:      &lease,
			ExecutionAttachmentID: &attachment,
			AttachmentTargetName:  &target,
		}},
	}
	response := dispatchControlTestFrame(t, controlHandlerStub{
		groupActivate: func(_ context.Context, got GroupActivateRequestParams) (GroupActivateResponseResult, error) {
			called = true
			if !reflect.DeepEqual(got, params) {
				t.Fatalf("GroupActivate() params = %#v, want %#v", got, params)
			}
			return GroupActivateResponseResult{
				ManagedRunGroupID: got.ManagedRunGroupID,
				Members: []GroupActivateResponseResultMembersItem{{
					ManagedRunID: got.Members[0].ManagedRunID, Outcome: "completed",
				}},
				ActivatedAtMs: 1_800_000_000_000,
			}, nil
		},
	}, struct {
		GroupActivateRequest
		Bearer string `json:"bearer"`
	}{
		GroupActivateRequest: GroupActivateRequest{
			JSONRPC: JSONRPCVersion, ID: params.OperationID,
			Method: MethodManagedRunGroupsActivate, Params: params,
		},
		Bearer: controlTestBearer,
	})
	if !called {
		t.Fatal("group activation handler was not called")
	}
	if err := ValidatePayload(PayloadGroupActivateResponse, response); err != nil {
		t.Fatalf("group activation response validation = %v: %s", err, response)
	}
}

func TestControlSessionDispatchesAuthenticatedManagedRunGroupAbandonment(t *testing.T) {
	called := false
	params := GroupAbandonRequestParams{
		OperationID: "operation_group_abandon", ManagedRunGroupID: "managed-run-group_a",
		RegistrationNonce: "group-registration-nonce_a", Reason: "activation_rejected",
		Disposition: "reap_safe",
		Members: []GroupAbandonRequestParamsMembersItem{
			{ManagedRunID: "managed-run_group-member-a", ExternalRunRef: "task-group-member-a", RegistrationNonce: "registration-nonce_group-member-a"},
			{ManagedRunID: "managed-run_group-member-b", ExternalRunRef: "task-group-member-b", RegistrationNonce: "registration-nonce_group-member-b"},
		},
	}
	response := dispatchControlTestFrame(t, controlHandlerStub{
		groupAbandon: func(_ context.Context, got GroupAbandonRequestParams) (GroupAbandonResponseResult, error) {
			called = true
			if !reflect.DeepEqual(got, params) {
				t.Fatalf("GroupAbandon() params = %#v, want %#v", got, params)
			}
			return GroupAbandonResponseResult{
				ManagedRunGroupID: got.ManagedRunGroupID, State: ManagedRunStateAbandoned,
				Disposition: got.Disposition,
				Members: []GroupAbandonResponseResultMembersItem{
					{ManagedRunID: got.Members[0].ManagedRunID, Outcome: "completed"},
					{ManagedRunID: got.Members[1].ManagedRunID, Outcome: "unknown"},
				},
			}, nil
		},
	}, struct {
		GroupAbandonRequest
		Bearer string `json:"bearer"`
	}{
		GroupAbandonRequest: GroupAbandonRequest{
			JSONRPC: JSONRPCVersion, ID: params.OperationID,
			Method: MethodManagedRunGroupsAbandon, Params: params,
		},
		Bearer: controlTestBearer,
	})
	if !called {
		t.Fatal("group abandonment handler was not called")
	}
	if err := ValidatePayload(PayloadGroupAbandonResponse, response); err != nil {
		t.Fatalf("group abandonment response validation = %v: %s", err, response)
	}
}
