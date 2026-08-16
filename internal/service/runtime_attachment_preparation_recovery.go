package service

import (
	"context"
	"errors"
	"fmt"

	"github.com/comisai/comis-dev-crew/internal/application"
)

func (coordinator *runtimeAttachmentCoordinator) recoverTaskPreparationIntents(ctx context.Context) error {
	refusals, err := coordinator.store.ListRuntimeAttachmentRecoveryRefusals(ctx)
	if err != nil {
		return fmt.Errorf("recover runtime attachments: list preparation refusals: %w", err)
	}
	byTask := make(map[string]application.RuntimeAttachmentRecoveryRefusal, len(refusals))
	for _, refusal := range refusals {
		if refusal.Validate() != nil {
			return errors.New("recover runtime attachments: preparation refusal is invalid")
		}
		if _, duplicate := byTask[refusal.TaskHandle]; duplicate {
			return errors.New("recover runtime attachments: preparation refusals conflict")
		}
		byTask[refusal.TaskHandle] = refusal
		coordinator.runtimeAttachmentRefusals[refusal.TaskHandle] = struct{}{}
	}
	intents, err := coordinator.store.ListTaskPreparationIntents(ctx)
	if err != nil {
		return fmt.Errorf("recover runtime attachments: list task preparation intents: %w", err)
	}
	for _, intent := range intents {
		if intent.Validate() != nil {
			return errors.New("recover runtime attachments: task preparation intent is invalid")
		}
		if refusal, found := byTask[intent.TaskHandle]; found {
			if refusal.OperationID != intent.OperationID || refusal.SubjectDigest != intent.SubjectDigest {
				return errors.New("recover runtime attachments: preparation refusal authority differs")
			}
			continue
		}
		if err := coordinator.removeTaskRuntimeDirectory(intent.TaskHandle); err != nil {
			if errors.Is(err, errRuntimeAttachmentPreparationUnproven) {
				at := coordinator.clock().UTC()
				if at.IsZero() {
					return errors.New("recover runtime attachments: service time is invalid")
				}
				if recordErr := coordinator.store.RefuseRuntimeAttachmentRecovery(ctx, intent, at); recordErr != nil {
					return errors.Join(errors.New("recover runtime attachments: preparation refusal cannot be recorded"), recordErr)
				}
				coordinator.runtimeAttachmentRefusals[intent.TaskHandle] = struct{}{}
				continue
			}
			return errors.Join(
				errors.New("recover runtime attachments: uncommitted task runtime directory remains"), err,
			)
		}
	}
	return nil
}
