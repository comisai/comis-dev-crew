package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/comisai/comis-dev-crew/internal/application"
	"github.com/comisai/comis-dev-crew/internal/domain"
)

// refreshInitiativeAggregate moves the containing initiative in the same
// transaction and at the same global state version as its member. A failure to
// derive or persist the aggregate therefore rolls back the member transition.
func refreshInitiativeAggregate(
	ctx context.Context,
	transaction *sql.Tx,
	taskHandle string,
	stateVersion int64,
	at time.Time,
) error {
	containing, found, err := initiativeForTask(ctx, transaction, taskHandle)
	if err != nil {
		return fmt.Errorf("refresh initiative aggregate: %w", err)
	}
	if !found {
		return nil
	}
	members := make([]domain.Task, 0)
	for _, handle := range initiativeTaskHandles(containing) {
		task, err := getTask(ctx, transaction, handle)
		if err != nil {
			return fmt.Errorf("refresh initiative aggregate member: %w", err)
		}
		members = append(members, task)
	}
	artifacts, err := listInitiativeContractArtifacts(ctx, transaction, containing.Handle)
	if err != nil {
		return fmt.Errorf("refresh initiative aggregate artifacts: %w", err)
	}
	state, err := application.DeriveInitiativeState(containing, members, artifacts)
	if err != nil {
		return fmt.Errorf("refresh initiative aggregate state: %w", err)
	}
	if state == containing.State {
		return refreshInitiativeLaunchFacts(ctx, transaction, containing)
	}
	if stateVersion < containing.StateVersion || at.Location() != time.UTC || at.Before(containing.UpdatedAt) {
		return errors.New("refresh initiative aggregate: member version or time precedes the initiative")
	}
	containing.State = state
	containing.StateVersion = stateVersion
	containing.UpdatedAt = at
	if err := containing.Validate(); err != nil {
		return fmt.Errorf("refresh initiative aggregate validation: %w", err)
	}
	if err := updateInitiativeRecord(ctx, transaction, containing); err != nil {
		return fmt.Errorf("refresh initiative aggregate record: %w", err)
	}
	return nil
}
