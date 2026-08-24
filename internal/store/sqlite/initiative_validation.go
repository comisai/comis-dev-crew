package sqlite

import (
	"context"
	"errors"
	"fmt"

	"github.com/comisai/comis-dev-crew/internal/application"
	"github.com/comisai/comis-dev-crew/internal/domain"
)

func requireInitiativeValidationDependencies(
	ctx context.Context,
	source queryer,
	taskHandle string,
) error {
	initiatives, err := listInitiatives(ctx, source)
	if err != nil {
		return fmt.Errorf("read initiative validation dependencies: %w", err)
	}
	var containing *domain.DevelopmentInitiative
	for index := range initiatives {
		if !initiatives[index].ContainsTask(taskHandle) {
			continue
		}
		if containing != nil {
			return errors.New("validate initiative member: task belongs to multiple initiatives")
		}
		containing = &initiatives[index]
	}
	if containing == nil {
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
