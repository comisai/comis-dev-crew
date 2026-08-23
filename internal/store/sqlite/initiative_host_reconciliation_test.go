package sqlite

import (
	"context"
	"errors"
	"math"
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

func TestInitiativeHostRecoveryDetectsOnlyUndeliveredMemberEgress(t *testing.T) {
	ctx := context.Background()
	store, initiativeHandle, activation := preparedInitiativeActivationStore(t)
	if _, err := store.CommitInitiativeActivation(ctx, activation); err != nil {
		t.Fatalf("CommitInitiativeActivation() error = %v", err)
	}
	initiative, tasks, _, err := store.InitiativeObservation(ctx, initiativeHandle)
	if err != nil || len(tasks) != 2 {
		t.Fatalf("InitiativeObservation() = %#v, %#v, %v", initiative, tasks, err)
	}
	if pending, err := store.InitiativeHasPendingComisEgress(ctx, initiative.Handle); err != nil || pending {
		t.Fatalf("InitiativeHasPendingComisEgress(empty) = %t, %v", pending, err)
	}
	now := activation.At.Add(time.Minute)
	if _, err := store.db.ExecContext(ctx, `INSERT INTO comis_evidence_outbox (
        operation_id, task_handle, evidence_ref, kind, subject_digest, observed_at,
        content_hash, verification_level, body, delivery_kind, file_name, media_type, state_version
    ) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		"put-evidence-host-recovery", tasks[0].Handle, "evidence-host-recovery", "candidate_bundle",
		"subject-digest-host-recovery", formatTime(now),
		"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		"adapter_verified", []byte("{}"), "none", "", "application/json", tasks[0].StateVersion,
	); err != nil {
		t.Fatalf("seed pending Comis egress: %v", err)
	}
	if pending, err := store.InitiativeHasPendingComisEgress(ctx, initiative.Handle); err != nil || !pending {
		t.Fatalf("InitiativeHasPendingComisEgress(pending) = %t, %v", pending, err)
	}
	if _, err := store.db.ExecContext(ctx,
		"UPDATE comis_evidence_outbox SET delivered_at = ? WHERE operation_id = ?",
		formatTime(now.Add(time.Second)), "put-evidence-host-recovery",
	); err != nil {
		t.Fatalf("settle pending Comis egress: %v", err)
	}
	if pending, err := store.InitiativeHasPendingComisEgress(ctx, initiative.Handle); err != nil || pending {
		t.Fatalf("InitiativeHasPendingComisEgress(delivered) = %t, %v", pending, err)
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

func TestInitiativeHostRecoveryRefusesInvalidUnavailableAndUnresolvedAuthority(t *testing.T) {
	if _, err := (&Store{}).CommitInitiativeHostRecovery(
		context.Background(), application.InitiativeHostRecoveryMutation{},
	); !errors.Is(err, application.ErrInvalidInput) {
		t.Fatalf("CommitInitiativeHostRecovery(invalid) error = %v, want ErrInvalidInput", err)
	}

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
	if _, err := store.db.ExecContext(ctx, "UPDATE tasks SET state = 'unknown' WHERE handle = ?", tasks[0].Handle); err != nil {
		t.Fatalf("seed unresolved member state: %v", err)
	}
	mutation := application.InitiativeHostRecoveryMutation{
		InitiativeHandle: initiative.Handle, ServiceInstanceID: activation.ServiceInstanceID,
		ManagedRunGroupID:    initiative.ManagedRunGroupID,
		MemberManagedRunIDs:  []string{tasks[0].ManagedRunID, tasks[1].ManagedRunID},
		StateCounts:          application.InitiativeHostStateCounts{Active: 1, Unknown: 1},
		ExpectedStateVersion: initiative.StateVersion, At: reconcileAt.Add(time.Minute),
	}
	if _, err := store.CommitInitiativeHostRecovery(ctx, mutation); !errors.Is(err, application.ErrPrecondition) {
		t.Fatalf("CommitInitiativeHostRecovery(unresolved member) error = %v, want ErrPrecondition", err)
	}

	if err := store.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if _, err := store.CommitInitiativeHostRecovery(ctx, mutation); err == nil {
		t.Fatal("CommitInitiativeHostRecovery(closed store) error = nil")
	}
}

func TestInitiativeHostRecoveryMemberSetComparisonRejectsEveryInexactShape(t *testing.T) {
	tests := []struct {
		left  []string
		right []string
		want  bool
	}{
		{left: []string{"a"}, right: []string{"a"}, want: true},
		{left: []string{"a"}, right: []string{"a", "b"}},
		{left: []string{""}, right: []string{""}},
		{left: []string{"a", "a"}, right: []string{"a", "a"}},
		{left: []string{"a", "b"}, right: []string{"a", "c"}},
	}
	for _, test := range tests {
		if got := sameStringSet(test.left, test.right); got != test.want {
			t.Fatalf("sameStringSet(%#v, %#v) = %t, want %t", test.left, test.right, got, test.want)
		}
	}
}

func TestInitiativeHostRecoveryStorageFailuresNeverPublishRecoveredState(t *testing.T) {
	newFixture := func(t *testing.T) (*Store, application.InitiativeHostRecoveryMutation) {
		t.Helper()
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
		return store, application.InitiativeHostRecoveryMutation{
			InitiativeHandle: initiative.Handle, ServiceInstanceID: activation.ServiceInstanceID,
			ManagedRunGroupID:    initiative.ManagedRunGroupID,
			MemberManagedRunIDs:  []string{tasks[0].ManagedRunID, tasks[1].ManagedRunID},
			StateCounts:          application.InitiativeHostStateCounts{Active: 2},
			ExpectedStateVersion: initiative.StateVersion, At: reconcileAt.Add(time.Minute),
		}
	}

	t.Run("missing initiative", func(t *testing.T) {
		store, mutation := newFixture(t)
		mutation.InitiativeHandle = "initiative-host-recovery-missing"
		if _, err := store.CommitInitiativeHostRecovery(context.Background(), mutation); !errors.Is(err, application.ErrNotFound) {
			t.Fatalf("CommitInitiativeHostRecovery(missing) error = %v, want ErrNotFound", err)
		}
	})

	t.Run("aggregate write failure", func(t *testing.T) {
		store, mutation := newFixture(t)
		if _, err := store.db.ExecContext(context.Background(), `CREATE TRIGGER refuse_host_recovery
			BEFORE UPDATE ON initiatives BEGIN SELECT RAISE(ABORT, 'injected host recovery failure'); END`); err != nil {
			t.Fatalf("install host recovery failure: %v", err)
		}
		if _, err := store.CommitInitiativeHostRecovery(context.Background(), mutation); err == nil {
			t.Fatal("CommitInitiativeHostRecovery(injected write failure) error = nil")
		}
		preserved, err := store.GetInitiative(context.Background(), mutation.InitiativeHandle)
		if err != nil || preserved.State != domain.InitiativeUnknown ||
			preserved.StateVersion != mutation.ExpectedStateVersion {
			t.Fatalf("preserved initiative = %#v, %v", preserved, err)
		}
	})

	t.Run("state version exhausted", func(t *testing.T) {
		store, mutation := newFixture(t)
		if _, err := store.db.ExecContext(context.Background(),
			"UPDATE tasks SET state_version = ? WHERE handle = (SELECT handle FROM tasks ORDER BY handle LIMIT 1)",
			int64(math.MaxInt64),
		); err != nil {
			t.Fatalf("exhaust state version: %v", err)
		}
		if _, err := store.CommitInitiativeHostRecovery(context.Background(), mutation); err == nil {
			t.Fatal("CommitInitiativeHostRecovery(exhausted version) error = nil")
		}
	})
}
