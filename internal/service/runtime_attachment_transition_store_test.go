package service

import (
	"context"
	"time"

	"github.com/comisai/comis-dev-crew/internal/application"
	"github.com/comisai/comis-dev-crew/internal/domain"
)

type runtimeTransitionStore struct {
	task         domain.Task
	preparation  application.ManagedRunPreparation
	taskRefusals []application.RuntimeRelayIdentityRefusal
}

func (store *runtimeTransitionStore) ListTasks(context.Context) ([]domain.Task, error) {
	return []domain.Task{store.task}, nil
}

func (*runtimeTransitionStore) ListTaskPreparationIntents(context.Context) ([]application.TaskPreparationIntent, error) {
	return nil, nil
}

func (*runtimeTransitionStore) ListRuntimeAttachmentRecoveryRefusals(
	context.Context,
) ([]application.RuntimeAttachmentRecoveryRefusal, error) {
	return nil, nil
}

func (*runtimeTransitionStore) RefuseRuntimeAttachmentRecovery(
	context.Context,
	application.TaskPreparationIntent,
	time.Time,
) error {
	return nil
}

func (*runtimeTransitionStore) ListRuntimeRelayIdentityUpgrades(context.Context) ([]application.RuntimeRelayIdentityUpgrade, error) {
	return nil, nil
}

func (store *runtimeTransitionStore) ListRuntimeRelayIdentityRefusals(context.Context) ([]application.RuntimeRelayIdentityRefusal, error) {
	return append([]application.RuntimeRelayIdentityRefusal(nil), store.taskRefusals...), nil
}

func (*runtimeTransitionStore) CompleteRuntimeRelayIdentityUpgrade(
	context.Context,
	application.RuntimeRelayIdentityUpgrade,
) error {
	return nil
}

func (*runtimeTransitionStore) RefuseRuntimeRelayIdentityUpgrade(
	context.Context,
	application.RuntimeRelayIdentityUpgrade,
	time.Time,
) error {
	return nil
}

func (store *runtimeTransitionStore) RefuseRuntimeAttachmentTaskRecovery(
	_ context.Context,
	taskHandle string,
	_ time.Time,
) error {
	store.taskRefusals = append(store.taskRefusals, application.RuntimeRelayIdentityRefusal{
		TaskHandle: taskHandle, Reason: application.RuntimeRelayIdentityUnproven,
	})
	return nil
}

func (store *runtimeTransitionStore) GetManagedRunPreparation(context.Context, string) (application.ManagedRunPreparation, error) {
	return store.preparation, nil
}

func (*runtimeTransitionStore) GetTaskCleanupRecord(context.Context, string) (application.TaskCleanupRecord, bool, error) {
	return application.TaskCleanupRecord{}, false, nil
}

func (*runtimeTransitionStore) CommitReport(context.Context, application.ReportMutation) (domain.ReportReceipt, error) {
	return domain.ReportReceipt{}, nil
}
