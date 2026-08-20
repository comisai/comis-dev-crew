package domain_test

import (
	"strings"
	"testing"
	"time"

	"github.com/comisai/comis-dev-crew/internal/domain"
)

func TestShipAcceptsEveryShipDeliveryModeAndRefusesReport(t *testing.T) {
	for _, mode := range []domain.DeliveryMode{
		domain.DeliveryPullRequest,
		domain.DeliveryLocalBranch,
		domain.DeliveryMergeAfterApproval,
	} {
		if !mode.ValidForShape(domain.ShapeShip) {
			t.Fatalf("ship refused %q", mode)
		}
	}
	if domain.DeliveryReport.ValidForShape(domain.ShapeShip) {
		t.Fatal("ship accepted report")
	}
}

func TestScoutStillAcceptsOnlyReport(t *testing.T) {
	if !domain.DeliveryReport.ValidForShape(domain.ShapeScout) {
		t.Fatal("scout refused report")
	}
	for _, mode := range []domain.DeliveryMode{
		domain.DeliveryPullRequest,
		domain.DeliveryLocalBranch,
		domain.DeliveryMergeAfterApproval,
	} {
		if mode.ValidForShape(domain.ShapeScout) {
			t.Fatalf("scout accepted %q", mode)
		}
	}
}

func TestOnlyMergeAfterApprovalRequiresMergeAuthority(t *testing.T) {
	// Merge authority is a separate action, not a more permissive worker mode.
	// A worker delivering a pull request must never hold it.
	if !domain.DeliveryMergeAfterApproval.RequiresMergeAuthority() {
		t.Fatal("merge_after_approval does not require merge authority")
	}
	for _, mode := range []domain.DeliveryMode{
		domain.DeliveryPullRequest,
		domain.DeliveryLocalBranch,
		domain.DeliveryReport,
	} {
		if mode.RequiresMergeAuthority() {
			t.Fatalf("%q requires merge authority", mode)
		}
	}
}

func approvalFixture() domain.MergeApproval {
	approvedAt := time.Unix(1_800_000_000, 0).UTC()
	return domain.MergeApproval{
		TaskHandle: "task-backend", ApprovalID: "00000000-0000-4000-8000-000000000001",
		ManagedRunID: "managed-run-0001", MCPOperationID: "merge-operation-0001",
		ResolvingPrincipal: "principal-0001", OperationFingerprint: strings.Repeat("a", 64),
		ApprovedHead: "0123456789abcdef0123456789abcdef01234567", ApprovedAt: approvedAt,
		ExpiresAt: approvedAt.Add(domain.MaximumMergeApprovalTTL), ConsumedAt: approvedAt.Add(time.Second),
		OperatorEnabled: true,
	}
}

func authorizationFixture(approval domain.MergeApproval) domain.MergeAuthorization {
	return domain.MergeAuthorization{
		ObservedHead: approval.ApprovedHead, ManagedRunID: approval.ManagedRunID,
		MCPOperationID: approval.MCPOperationID, Now: approval.ConsumedAt,
	}
}

func TestMergeIsRefusedWhenTheHeadMovedAfterApproval(t *testing.T) {
	approval := approvalFixture()
	// The approval was given for exact content. A head that moved afterwards is
	// content nobody approved, so the approval and its evidence both die.
	request := authorizationFixture(approval)
	request.ObservedHead = "89abcdef0123456789abcdef0123456789abcdef"
	err := approval.AuthorizeMerge(request)
	if err == nil {
		t.Fatal("merge authorized against a moved head")
	}
	if !domain.IsMergeRefusal(err, domain.MergeRefusedHeadChanged) {
		t.Fatalf("refusal = %v", err)
	}
	if got := err.Error(); got != "merge refused (head_changed): the head moved after approval; approval and evidence are both invalid" {
		t.Fatalf("refusal text = %q", got)
	}
}

func TestMergeIsRefusedWhenTheOperatorDisabledIt(t *testing.T) {
	approval := approvalFixture()
	approval.OperatorEnabled = false
	err := approval.AuthorizeMerge(authorizationFixture(approval))
	if !domain.IsMergeRefusal(err, domain.MergeRefusedOperatorDisabled) {
		t.Fatalf("refusal = %v", err)
	}
}

func TestMergeIsRefusedWithoutAnApproval(t *testing.T) {
	approval := approvalFixture()
	approval.ApprovalID = ""
	err := approval.AuthorizeMerge(authorizationFixture(approval))
	if !domain.IsMergeRefusal(err, domain.MergeRefusedNoApproval) {
		t.Fatalf("refusal = %v", err)
	}
}

func TestMergeIsRefusedWhenRecordedApprovalDoesNotPinARevision(t *testing.T) {
	approval := approvalFixture()
	approval.ApprovedHead = "not-a-revision"
	err := approval.AuthorizeMerge(authorizationFixture(approval))
	if !domain.IsMergeRefusal(err, domain.MergeRefusedNoApproval) {
		t.Fatalf("refusal = %v", err)
	}
}

func TestMergeIsAuthorizedForTheExactApprovedHead(t *testing.T) {
	approval := approvalFixture()
	if err := approval.AuthorizeMerge(authorizationFixture(approval)); err != nil {
		t.Fatalf("exact approved head refused: %v", err)
	}
}

func TestMergeIsRefusedWhenApprovalTimeIsNotCurrent(t *testing.T) {
	approval := approvalFixture()
	approval.ApprovedAt = time.Unix(1_900_000_000, 0).UTC()
	if err := approval.AuthorizeMerge(authorizationFixture(approval)); err == nil {
		t.Fatal("merge authorized without establishing a current approval window")
	}
}

func TestMergeIsRefusedAfterTheApprovalExpires(t *testing.T) {
	approval := approvalFixture()
	request := authorizationFixture(approval)
	request.Now = approval.ExpiresAt
	err := approval.AuthorizeMerge(request)
	if !domain.IsMergeRefusal(err, domain.MergeRefusedApprovalExpired) {
		t.Fatalf("refusal = %v", err)
	}
}

func TestMergeIsRefusedForAnotherManagedOperation(t *testing.T) {
	approval := approvalFixture()
	request := authorizationFixture(approval)
	request.MCPOperationID = "merge-operation-0002"
	err := approval.AuthorizeMerge(request)
	if !domain.IsMergeRefusal(err, domain.MergeRefusedScopeMismatch) {
		t.Fatalf("refusal = %v", err)
	}
}
