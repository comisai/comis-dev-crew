package sqlite

import (
	"context"
	"fmt"

	"github.com/comisai/comis-dev-crew/internal/application"
	"github.com/comisai/comis-dev-crew/internal/domain"
)

func requireInitiativeValidationDependencies(
	ctx context.Context,
	source queryer,
	taskHandle string,
) error {
	containing, found, err := initiativeForTask(ctx, source, taskHandle)
	if err != nil {
		return fmt.Errorf("read initiative validation dependencies: %w", err)
	}
	if !found {
		return nil
	}
	for _, edge := range containing.Edges {
		if edge.Kind != domain.EdgeBlocksValidation || edge.ToTaskHandle != taskHandle {
			continue
		}
		predecessor, err := getTask(ctx, source, edge.FromTaskHandle)
		if err != nil {
			return fmt.Errorf("read initiative validation predecessor: %w", err)
		}
		if !predecessor.State.SatisfiesInitiativeDependency() {
			return fmt.Errorf("initiative validation dependency is incomplete: %w", application.ErrPrecondition)
		}
	}
	return nil
}
