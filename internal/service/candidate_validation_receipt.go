package service

import (
	"time"

	"github.com/comisai/comis-dev-crew/internal/domain"
	devgit "github.com/comisai/comis-dev-crew/internal/git"
	"github.com/comisai/comis-dev-crew/internal/validation"
)

func validationReceiptMismatch(
	receipt validation.Receipt,
	operationID string,
	task domain.Task,
	profile validation.Profile,
	check validation.LocalCheck,
	snapshot devgit.CandidateSnapshot,
) string {
	switch {
	case receipt.OperationID != operationID:
		return "operation_id"
	case receipt.TaskHandle != task.Handle:
		return "task_handle"
	case receipt.ProfileID != profile.ID:
		return "profile_id"
	case receipt.CheckID != check.ID:
		return "check_id"
	case receipt.ProgramID != check.ProgramID:
		return "program_id"
	case receipt.HeadRevision != snapshot.HeadRevision:
		return "head_revision"
	case receipt.StartedAt.Location() != time.UTC:
		return "started_at_timezone"
	case receipt.CompletedAt.Location() != time.UTC:
		return "completed_at_timezone"
	case receipt.CompletedAt.Before(receipt.StartedAt):
		return "completion_order"
	case len(receipt.OutputHash) != 64:
		return "output_hash_length"
	default:
		return ""
	}
}
