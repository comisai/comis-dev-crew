package domain

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"
)

const MaximumMergeApprovalTTL = 15 * time.Minute

var mergeApprovalFingerprintPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

// MergeRefusalReason is the closed set of reasons a merge is not authorized.
// Each names a precondition an operator can act on; none of them is a transient
// the caller should retry into.
type MergeRefusalReason string

const (
	MergeRefusedNoApproval       MergeRefusalReason = "no_approval"
	MergeRefusedApprovalExpired  MergeRefusalReason = "approval_expired"
	MergeRefusedScopeMismatch    MergeRefusalReason = "approval_scope_mismatch"
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
	TaskHandle           string
	ApprovalID           string
	ManagedRunID         string
	MCPOperationID       string
	ResolvingPrincipal   string
	OperationFingerprint string
	ApprovedHead         string
	ApprovedAt           time.Time
	ExpiresAt            time.Time
	ConsumedAt           time.Time
	OperatorEnabled      bool
}

// MergeAuthorization carries the exact service and forge observations checked
// immediately before the merge adapter receives authority.
type MergeAuthorization struct {
	ObservedHead   string
	ManagedRunID   string
	MCPOperationID string
	Now            time.Time
}

// AuthorizeMerge decides whether a merge may proceed against the head observed
// immediately before merging.
//
// The order of the checks matters. An operator switch that is off makes the
// question moot, so it is answered before anything about approvals; and the
// absence of an approval is reported as such rather than as a head mismatch
// against an empty head.
func (approval MergeApproval) AuthorizeMerge(request MergeAuthorization) error {
	if !approval.OperatorEnabled {
		return &MergeRefusal{
			Reason: MergeRefusedOperatorDisabled,
			Detail: "merge_after_approval is disabled for this deployment",
		}
	}
	if validateOpaqueID("taskHandle", approval.TaskHandle) != nil ||
		validateAuthorityReference("approvalRequestId", approval.ApprovalID) != nil ||
		validateAuthorityReference("managedRunId", approval.ManagedRunID) != nil ||
		validateOpaqueID("mcpOperationId", approval.MCPOperationID) != nil ||
		!validMergePrincipal(approval.ResolvingPrincipal) ||
		!mergeApprovalFingerprintPattern.MatchString(approval.OperationFingerprint) ||
		validateRevision(approval.ApprovedHead) != nil ||
		approval.ApprovedAt.IsZero() || approval.ApprovedAt.Location() != time.UTC ||
		approval.ExpiresAt.IsZero() || approval.ExpiresAt.Location() != time.UTC ||
		approval.ConsumedAt.IsZero() || approval.ConsumedAt.Location() != time.UTC ||
		approval.ExpiresAt.Sub(approval.ApprovedAt) != MaximumMergeApprovalTTL ||
		approval.ConsumedAt.Before(approval.ApprovedAt) || !approval.ConsumedAt.Before(approval.ExpiresAt) {
		return &MergeRefusal{
			Reason: MergeRefusedNoApproval,
			Detail: "no complete authenticated approval receipt is recorded for this task",
		}
	}
	if request.Now.IsZero() || request.Now.Location() != time.UTC ||
		request.Now.Before(approval.ApprovedAt) || !request.Now.Before(approval.ExpiresAt) {
		return &MergeRefusal{
			Reason: MergeRefusedApprovalExpired,
			Detail: "the recorded approval is outside its current fifteen-minute window",
		}
	}
	if request.ManagedRunID != approval.ManagedRunID || request.MCPOperationID != approval.MCPOperationID {
		return &MergeRefusal{
			Reason: MergeRefusedScopeMismatch,
			Detail: "the approval receipt belongs to a different managed operation",
		}
	}
	if request.ObservedHead != approval.ApprovedHead {
		return &MergeRefusal{
			Reason: MergeRefusedHeadChanged,
			Detail: "the head moved after approval; approval and evidence are both invalid",
		}
	}
	return nil
}

func validMergePrincipal(value string) bool {
	return value != "" && len([]byte(value)) <= 256 && utf8.ValidString(value) &&
		strings.TrimSpace(value) == value && !strings.ContainsAny(value, "\x00\r\n")
}
