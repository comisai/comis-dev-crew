package sqlite

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/comisai/comis-dev-crew/internal/application"
	"github.com/comisai/comis-dev-crew/internal/domain"
)

func TestInitiativeHostRecoveryCommitsOnlyAnExactAtomicRollup(t *testing.T) {
	ctx := context.Background()
	store, initiativeHandle, activation := preparedInitiativeActivationStore(t)
	if _, err := store.CommitInitiativeActivation(ctx, activation); err != nil {
		t.Fatalf("CommitInitiativeActivation() error = %v", err)
	}
	reconcileAt := activation.At.Add(time.Minute)
	if _, err := store.ReconcileStartup(ctx, reconcileAt); err != nil {
		t.Fatalf("ReconcileStartup() error = %v", err)
	}
	initiative, tasks, _, err := store.InitiativeObservation(ctx, initiativeHandle)
	if err != nil || initiative.State != domain.InitiativeUnknown || len(tasks) != 2 {
		t.Fatalf("InitiativeObservation() = %#v, %#v, %v", initiative, tasks, err)
	}
	memberIDs := []string{tasks[1].ManagedRunID, tasks[0].ManagedRunID}
	mutation := application.InitiativeHostRecoveryMutation{
		InitiativeHandle: initiative.Handle, ServiceInstanceID: activation.ServiceInstanceID,
		ManagedRunGroupID: initiative.ManagedRunGroupID, MemberManagedRunIDs: memberIDs,
		StateCounts:          application.InitiativeHostStateCounts{Active: 2},
		ExpectedStateVersion: initiative.StateVersion, At: reconcileAt.Add(time.Minute),
	}

	recovered, err := store.CommitInitiativeHostRecovery(ctx, mutation)

	if err != nil || recovered.State != domain.InitiativeActive ||
		recovered.StateVersion <= initiative.StateVersion || !recovered.UpdatedAt.Equal(mutation.At) {
		t.Fatalf("CommitInitiativeHostRecovery() = %#v, %v", recovered, err)
	}
}

func TestInitiativeHostRecoveryMismatchPreservesDurableUnknownState(t *testing.T) {
	ctx := context.Background()
	store, initiativeHandle, activation := preparedInitiativeActivationStore(t)
	if _, err := store.CommitInitiativeActivation(ctx, activation); err != nil {
		t.Fatalf("CommitInitiativeActivation() error = %v", err)
	}
	reconcileAt := activation.At.Add(time.Minute)
	if _, err := store.ReconcileStartup(ctx, reconcileAt); err != nil {
		t.Fatalf("ReconcileStartup() error = %v", err)
	}
	initiative, tasks, _, err := store.InitiativeObservation(ctx, initiativeHandle)
	if err != nil {
		t.Fatalf("InitiativeObservation() error = %v", err)
	}
	tests := []struct {
		name   string
		mutate func(*application.InitiativeHostRecoveryMutation)
	}{
		{name: "host state counts differ", mutate: func(m *application.InitiativeHostRecoveryMutation) {
			m.StateCounts = application.InitiativeHostStateCounts{Waiting: 2}
		}},
		{name: "member identity differs", mutate: func(m *application.InitiativeHostRecoveryMutation) {
			m.MemberManagedRunIDs[1] = "managed-run-unexpected"
		}},
		{name: "service instance differs", mutate: func(m *application.InitiativeHostRecoveryMutation) {
			m.ServiceInstanceID = "service-instance-foreign"
		}},
		{name: "snapshot version differs", mutate: func(m *application.InitiativeHostRecoveryMutation) {
			m.ExpectedStateVersion++
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			mutation := application.InitiativeHostRecoveryMutation{
				InitiativeHandle: initiative.Handle, ServiceInstanceID: activation.ServiceInstanceID,
				ManagedRunGroupID:    initiative.ManagedRunGroupID,
				MemberManagedRunIDs:  []string{tasks[0].ManagedRunID, tasks[1].ManagedRunID},
				StateCounts:          application.InitiativeHostStateCounts{Active: 2},
				ExpectedStateVersion: initiative.StateVersion, At: reconcileAt.Add(time.Minute),
			}
			test.mutate(&mutation)
			if _, err := store.CommitInitiativeHostRecovery(ctx, mutation); !errors.Is(err, application.ErrPrecondition) {
				t.Fatalf("CommitInitiativeHostRecovery(mismatch) error = %v, want ErrPrecondition", err)
			}
			preserved, err := store.GetInitiative(ctx, initiative.Handle)
			if err != nil || preserved.State != domain.InitiativeUnknown ||
				preserved.StateVersion != initiative.StateVersion {
				t.Fatalf("preserved initiative = %#v, %v", preserved, err)
			}
		})
	}
}
