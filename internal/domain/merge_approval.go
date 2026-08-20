package domain

import (
	"errors"
	"fmt"
	"time"
)

// MergeRefusalReason is the closed set of reasons a merge is not authorized.
// Each names a precondition an operator can act on; none of them is a transient
// the caller should retry into.
type MergeRefusalReason string

const (
	MergeRefusedNoApproval       MergeRefusalReason = "no_approval"
	MergeRefusedHeadChanged      MergeRefusalReason = "head_changed"
	MergeRefusedOperatorDisabled MergeRefusalReason = "operator_disabled"
)

// MergeRefusal explains why a merge was not authorized.
type MergeRefusal struct {
	Reason MergeRefusalReason
	Detail string
}

func (refusal *MergeRefusal) Error() string {
	return fmt.Sprintf("merge refused (%s): %s", refusal.Reason, refusal.Detail)
}

// IsMergeRefusal reports whether an error is a merge refusal for one reason.
func IsMergeRefusal(err error, reason MergeRefusalReason) bool {
	var refusal *MergeRefusal
	return errors.As(err, &refusal) && refusal.Reason == reason
}

// MergeApproval is one human decision bound to exact content.
//
// The approval names a head, not a task. Approving "the task" would let content
// nobody looked at ride in on a later push, so a head that moves after approval
// invalidates both the approval and the evidence gathered against it, and
// requires fresh validation rather than a re-confirmation.
type MergeApproval struct {
	TaskHandle      string
	ApprovalID      string
	ApprovedHead    string
	ApprovedAt      time.Time
	OperatorEnabled bool
}

// AuthorizeMerge decides whether a merge may proceed against the head observed
// immediately before merging.
//
// The order of the checks matters. An operator switch that is off makes the
// question moot, so it is answered before anything about approvals; and the
// absence of an approval is reported as such rather than as a head mismatch
// against an empty head.
func (approval MergeApproval) AuthorizeMerge(observedHead string) error {
	if !approval.OperatorEnabled {
		return &MergeRefusal{
			Reason: MergeRefusedOperatorDisabled,
			Detail: "merge_after_approval is disabled for this deployment",
		}
	}
	if approval.ApprovalID == "" {
		return &MergeRefusal{
			Reason: MergeRefusedNoApproval,
			Detail: "no current approval is recorded for this task",
		}
	}
	if err := validateRevision(approval.ApprovedHead); err != nil {
		return &MergeRefusal{
			Reason: MergeRefusedNoApproval,
			Detail: "the recorded approval does not pin an exact head",
		}
	}
	if observedHead != approval.ApprovedHead {
		return &MergeRefusal{
			Reason: MergeRefusedHeadChanged,
			Detail: "the head moved after approval; approval and evidence are both invalid",
		}
	}
	return nil
}
