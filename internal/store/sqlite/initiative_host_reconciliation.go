package sqlite

import (
	"context"
	"fmt"

	"github.com/comisai/comis-dev-crew/internal/application"
	"github.com/comisai/comis-dev-crew/internal/domain"
)

var _ application.InitiativeHostRecoveryStore = (*Store)(nil)

// CommitInitiativeHostRecovery restores one initiative only when the host's
// complete member projection still equals the current durable task rows.
func (store *Store) CommitInitiativeHostRecovery(
	ctx context.Context,
	mutation application.InitiativeHostRecoveryMutation,
) (domain.DevelopmentInitiative, error) {
	if err := mutation.Validate(); err != nil {
		return domain.DevelopmentInitiative{}, err
	}
	transaction, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.DevelopmentInitiative{}, fmt.Errorf("begin initiative host recovery: %w", err)
	}
	defer func() { _ = transaction.Rollback() }()
	initiative, err := getInitiative(ctx, transaction, mutation.InitiativeHandle)
	if err != nil {
		return domain.DevelopmentInitiative{}, err
	}
	if initiative.State != domain.InitiativeUnknown ||
		initiative.ManagedRunGroupID != mutation.ManagedRunGroupID ||
		initiative.StateVersion != mutation.ExpectedStateVersion ||
		mutation.At.Before(initiative.UpdatedAt) {
		return domain.DevelopmentInitiative{}, application.ErrPrecondition
	}
	tasks := make([]domain.Task, 0)
	memberIDs := make([]string, 0)
	for _, taskHandle := range initiativeTaskHandles(initiative) {
		task, taskErr := getTask(ctx, transaction, taskHandle)
		if taskErr != nil {
			return domain.DevelopmentInitiative{}, fmt.Errorf("read initiative host recovery member: %w", taskErr)
		}
		if task.ServiceInstanceID != mutation.ServiceInstanceID {
			return domain.DevelopmentInitiative{}, application.ErrPrecondition
		}
		tasks = append(tasks, task)
		memberIDs = append(memberIDs, task.ManagedRunID)
	}
	wantCounts, err := application.InitiativeHostStateCountsForTasks(tasks)
	if err != nil || !sameStringSet(memberIDs, mutation.MemberManagedRunIDs) || wantCounts != mutation.StateCounts {
		return domain.DevelopmentInitiative{}, application.ErrPrecondition
	}
	derivationInput := initiative
	derivationInput.State = domain.InitiativeActive
	recoveredState, err := application.DeriveInitiativeState(derivationInput, tasks)
	if err != nil {
		return domain.DevelopmentInitiative{}, fmt.Errorf("derive initiative host recovery state: %w", err)
	}
	if recoveredState == domain.InitiativeUnknown {
		return domain.DevelopmentInitiative{}, application.ErrPrecondition
	}
	stateVersion, err := nextMutationStateVersion(ctx, transaction)
	if err != nil {
		return domain.DevelopmentInitiative{}, err
	}
	initiative.State = recoveredState
	initiative.StateVersion = stateVersion
	initiative.UpdatedAt = mutation.At
	if err := initiative.Validate(); err != nil {
		return domain.DevelopmentInitiative{}, fmt.Errorf("validate initiative host recovery: %w", err)
	}
	if err := updateInitiativeRecord(ctx, transaction, initiative); err != nil {
		return domain.DevelopmentInitiative{}, err
	}
	if err := transaction.Commit(); err != nil {
		return domain.DevelopmentInitiative{}, fmt.Errorf("commit initiative host recovery: %w", err)
	}
	return initiative, nil
}

func sameStringSet(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	seen := make(map[string]struct{}, len(left))
	for _, value := range left {
		if value == "" {
			return false
		}
		seen[value] = struct{}{}
	}
	if len(seen) != len(left) {
		return false
	}
	for _, value := range right {
		if _, exists := seen[value]; !exists {
			return false
		}
		delete(seen, value)
	}
	return len(seen) == 0
}
