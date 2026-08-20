package domain_test

import (
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
	return domain.MergeApproval{
		TaskHandle:      "task-backend",
		ApprovalID:      "approval-0001",
		ApprovedHead:    "0123456789abcdef0123456789abcdef01234567",
		ApprovedAt:      time.Unix(1_800_000_000, 0).UTC(),
		OperatorEnabled: true,
	}
}

func TestMergeIsRefusedWhenTheHeadMovedAfterApproval(t *testing.T) {
	approval := approvalFixture()
	// The approval was given for exact content. A head that moved afterwards is
	// content nobody approved, so the approval and its evidence both die.
	err := approval.AuthorizeMerge("89abcdef0123456789abcdef0123456789abcdef")
	if err == nil {
		t.Fatal("merge authorized against a moved head")
	}
	if !domain.IsMergeRefusal(err, domain.MergeRefusedHeadChanged) {
		t.Fatalf("refusal = %v", err)
	}
}

func TestMergeIsRefusedWhenTheOperatorDisabledIt(t *testing.T) {
	approval := approvalFixture()
	approval.OperatorEnabled = false
	err := approval.AuthorizeMerge(approval.ApprovedHead)
	if !domain.IsMergeRefusal(err, domain.MergeRefusedOperatorDisabled) {
		t.Fatalf("refusal = %v", err)
	}
}

func TestMergeIsRefusedWithoutAnApproval(t *testing.T) {
	approval := approvalFixture()
	approval.ApprovalID = ""
	err := approval.AuthorizeMerge(approval.ApprovedHead)
	if !domain.IsMergeRefusal(err, domain.MergeRefusedNoApproval) {
		t.Fatalf("refusal = %v", err)
	}
}

func TestMergeIsAuthorizedForTheExactApprovedHead(t *testing.T) {
	approval := approvalFixture()
	if err := approval.AuthorizeMerge(approval.ApprovedHead); err != nil {
		t.Fatalf("exact approved head refused: %v", err)
	}
}
