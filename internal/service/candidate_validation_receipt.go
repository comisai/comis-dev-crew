package service

import (
	"time"

	"github.com/comisai/comis-dev-crew/internal/domain"
	devgit "github.com/comisai/comis-dev-crew/internal/git"
	"github.com/comisai/comis-dev-crew/internal/validation"
)

func completeValidationReceipt(
	receipt validation.Receipt,
	operationID string,
	task domain.Task,
	profile validation.Profile,
	check validation.LocalCheck,
	snapshot devgit.CandidateSnapshot,
) bool {
	return receipt.OperationID == operationID &&
		receipt.TaskHandle == task.Handle && receipt.ProfileID == profile.ID && receipt.CheckID == check.ID &&
		receipt.ProgramID == check.ProgramID && receipt.HeadRevision == snapshot.HeadRevision &&
		receipt.StartedAt.Location() == time.UTC && receipt.CompletedAt.Location() == time.UTC &&
		!receipt.CompletedAt.Before(receipt.StartedAt) && len(receipt.OutputHash) == 64
}
