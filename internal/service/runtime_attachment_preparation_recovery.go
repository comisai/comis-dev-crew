package service

import (
	"context"
	"errors"
	"fmt"
)

func (coordinator *runtimeAttachmentCoordinator) recoverTaskPreparationIntents(ctx context.Context) error {
	intents, err := coordinator.store.ListTaskPreparationIntents(ctx)
	if err != nil {
		return fmt.Errorf("recover runtime attachments: list task preparation intents: %w", err)
	}
	for _, intent := range intents {
		if intent.Validate() != nil {
			return errors.New("recover runtime attachments: task preparation intent is invalid")
		}
		if err := coordinator.removeTaskRuntimeDirectory(intent.TaskHandle); err != nil {
			return errors.Join(
				errors.New("recover runtime attachments: uncommitted task runtime directory remains"), err,
			)
		}
	}
	return nil
}
