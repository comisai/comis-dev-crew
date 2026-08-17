package application

import (
	"errors"
	"time"

	"github.com/comisai/comis-dev-crew/internal/domain"
)

// RuntimeAttachmentRecoveryRefusalReason is the closed reason preparation recovery cannot establish authority.
type RuntimeAttachmentRecoveryRefusalReason string

const RuntimeAttachmentPreparationUnproven RuntimeAttachmentRecoveryRefusalReason = "unproven_filesystem_authority"

// RuntimeAttachmentRecoveryRefusal preserves one preparation whose runtime ownership cannot be proven.
type RuntimeAttachmentRecoveryRefusal struct {
	OperationID   string
	TaskHandle    string
	SubjectDigest string
	Reason        RuntimeAttachmentRecoveryRefusalReason
	RefusedAt     time.Time
}

// Validate rejects incomplete or unknown preparation recovery evidence.
func (refusal RuntimeAttachmentRecoveryRefusal) Validate() error {
	intent := TaskPreparationIntent{
		OperationID: refusal.OperationID, TaskHandle: refusal.TaskHandle,
		SubjectDigest: refusal.SubjectDigest, CreatedAt: refusal.RefusedAt,
	}
	if intent.Validate() != nil || refusal.Reason != RuntimeAttachmentPreparationUnproven ||
		domain.ValidateTaskHandle(refusal.TaskHandle) != nil {
		return errors.New("runtime attachment recovery refusal is invalid")
	}
	return nil
}
