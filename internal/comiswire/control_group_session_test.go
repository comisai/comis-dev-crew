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
