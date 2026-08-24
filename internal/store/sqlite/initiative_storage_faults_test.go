package sqlite

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/comisai/comis-dev-crew/internal/application"
	"github.com/comisai/comis-dev-crew/internal/domain"
)

func TestInitiativePersistenceRejectsCorruptStoredFields(t *testing.T) {
	for _, test := range []struct {
		name      string
		statement string
	}{
		{name: "base revisions", statement: `UPDATE initiatives SET base_revision_set_json = '{'`},
		{name: "components", statement: `UPDATE initiatives SET components_json = '{'`},
		{name: "edges", statement: `UPDATE initiatives SET edges_json = '{'`},
		{name: "contract artifacts", statement: `UPDATE initiatives SET contract_artifacts_json = '{'`},
		{name: "created time", statement: `UPDATE initiatives SET created_at = 'invalid'`},
		{name: "updated time", statement: `UPDATE initiatives SET updated_at = 'invalid'`},
		{name: "domain record", statement: `UPDATE initiatives SET state = 'invented'`},
	} {
		t.Run(test.name, func(t *testing.T) {
			store := openInitiativeFaultStore(t)
			initiative := persistenceInitiative("initiative-corrupt-record", domain.InitiativeActive, 1)
			if err := store.CreateInitiative(context.Background(), initiative); err != nil {
				t.Fatalf("CreateInitiative() error = %v", err)
			}
			mustExecInitiativeBoundary(t, store, test.statement)
			if _, err := store.GetInitiative(context.Background(), initiative.Handle); err == nil {
				t.Fatal("GetInitiative(corrupt record) error = nil")
			}
			if _, err := store.ListInitiatives(context.Background()); err == nil {
				t.Fatal("ListInitiatives(corrupt record) error = nil")
			}
		})
	}
}

func TestBacklogPersistenceRejectsCorruptStoredFields(t *testing.T) {
	for _, test := range []struct {
		name      string
		statement string
	}{
		{name: "dependencies", statement: `UPDATE backlog_items SET depends_on_json = '{'`},
		{name: "created time", statement: `UPDATE backlog_items SET created_at = 'invalid'`},
		{name: "updated time", statement: `UPDATE backlog_items SET updated_at = 'invalid'`},
		{name: "domain record", statement: `UPDATE backlog_items SET readiness = 'invented'`},
	} {
		t.Run(test.name, func(t *testing.T) {
			store := openInitiativeFaultStore(t)
			item := persistenceBacklogItem("backlog-corrupt-record")
			if err := store.CreateBacklogItem(context.Background(), item); err != nil {
				t.Fatalf("CreateBacklogItem() error = %v", err)
			}
			mustExecInitiativeBoundary(t, store, test.statement)
			if _, err := store.GetBacklogItem(context.Background(), item.Handle); err == nil {
				t.Fatal("GetBacklogItem(corrupt record) error = nil")
			}
			if _, err := store.ListBacklogItems(context.Background()); err == nil {
				t.Fatal("ListBacklogItems(corrupt record) error = nil")
			}
		})
	}
}

func TestInitiativeRepositoriesReportClosedDatabaseBoundaries(t *testing.T) {
	store := openInitiativeFaultStore(t)
	if err := store.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	checks := []struct {
		name string
		call func() error
	}{
		{name: "create initiative", call: func() error {
			return store.CreateInitiative(context.Background(), persistenceInitiative("initiative-closed", domain.InitiativeActive, 1))
		}},
		{name: "get initiative", call: func() error { _, err := store.GetInitiative(context.Background(), "initiative-closed"); return err }},
		{name: "list initiatives", call: func() error { _, err := store.ListInitiatives(context.Background()); return err }},
		{name: "create backlog", call: func() error {
			return store.CreateBacklogItem(context.Background(), persistenceBacklogItem("backlog-closed"))
		}},
		{name: "get backlog", call: func() error { _, err := store.GetBacklogItem(context.Background(), "backlog-closed"); return err }},
		{name: "list backlog", call: func() error { _, err := store.ListBacklogItems(context.Background()); return err }},
	}
	for _, check := range checks {
		t.Run(check.name, func(t *testing.T) {
			if err := check.call(); err == nil {
				t.Fatal("closed repository operation error = nil")
			}
		})
	}
}

func TestInitiativePreparationReplayRejectsCorruptPrivateJoins(t *testing.T) {
	for _, test := range []struct {
		name      string
		statement string
	}{
		{name: "missing group preparation", statement: `DELETE FROM initiative_preparations`},
		{name: "invalid group expiry", statement: `UPDATE initiative_preparations SET expires_at = 'invalid'`},
		{name: "invalid group creation", statement: `UPDATE initiative_preparations SET created_at = 'invalid'`},
		{name: "invalid group nonce", statement: `UPDATE initiative_preparations SET registration_nonce = 'bad nonce'`},
		{name: "missing member preparation", statement: `DELETE FROM task_preparations WHERE task_handle = 'task-component-a'`},
		{name: "invalid member record", statement: `UPDATE tasks SET state = 'invented' WHERE handle = 'task-component-a'`},
	} {
		t.Run(test.name, func(t *testing.T) {
			store := openInitiativeFaultStore(t)
			mutation := sqlitePreparedInitiativeMutation()
			recordInitiativeMemberIntents(t, store, mutation)
			if _, err := store.CommitPreparedInitiative(context.Background(), mutation); err != nil {
				t.Fatalf("CommitPreparedInitiative() error = %v", err)
			}
			mustExecInitiativeBoundary(t, store, test.statement)
			if _, _, err := store.ReplayInitiativePreparation(
				context.Background(), mutation.OperationID, mutation.SubjectDigest,
			); err == nil {
				t.Fatal("ReplayInitiativePreparation(corrupt join) error = nil")
			}
		})
	}
}

func TestInitiativeStoreHelpersReportRolledBackTransactions(t *testing.T) {
	store := openInitiativeFaultStore(t)
	transaction, err := store.db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatalf("BeginTx() error = %v", err)
	}
	if err := transaction.Rollback(); err != nil {
		t.Fatalf("Rollback() error = %v", err)
	}
	ctx := context.Background()
	operation := domain.OperationRecord{ID: "operation-rolled-back", ResultRef: "initiative-rolled-back"}
	initiative := persistenceInitiative("initiative-rolled-back", domain.InitiativeActive, 2)
	task := storeTask("task-rolled-back", 2)
	abandonMember := application.ManagedRunGroupAbandonmentMember{
		ManagedRunID: "managed-run-rolled-back", ExternalRunRef: task.Handle, RegistrationNonce: "registration-nonce_rolled-back",
	}
	checks := []struct {
		name string
		call func() error
	}{
		{name: "preparation by nonce", call: func() error {
			_, _, err := initiativePreparationByNonce(ctx, transaction, "registration-nonce_rolled-back")
			return err
		}},
		{name: "preparation result", call: func() error { _, err := initiativePreparationResult(ctx, transaction, operation); return err }},
		{name: "activation result", call: func() error { _, err := initiativeActivationResult(ctx, transaction, operation); return err }},
		{name: "activation group lookup", call: func() error {
			_, err := getInitiativeByManagedRunGroup(ctx, transaction, initiative.ManagedRunGroupID)
			return err
		}},
		{name: "activation task update", call: func() error { return updateInitiativeMemberTask(ctx, transaction, task) }},
		{name: "initiative update", call: func() error { return updateInitiativeRecord(ctx, transaction, initiative) }},
		{name: "abandon member insert", call: func() error {
			return insertInitiativeAbandonmentMember(ctx, transaction, operation.ID, 0, abandonMember, application.InitiativeActivationMemberResult{
				ManagedRunID: abandonMember.ManagedRunID, Outcome: application.InitiativeActivationCompleted,
			})
		}},
		{name: "abandon result", call: func() error { _, err := initiativeAbandonmentResult(ctx, transaction, operation); return err }},
		{name: "aggregate refresh", call: func() error {
			return refreshInitiativeAggregate(ctx, transaction, task.Handle, 2, time.Date(2026, time.August, 20, 21, 0, 0, 0, time.UTC))
		}},
		{name: "launch authorization", call: func() error {
			return authorizeInitiativeTaskStart(ctx, transaction, task, initiativeTestSchedulingLimits(1))
		}},
	}
	for _, check := range checks {
		t.Run(check.name, func(t *testing.T) {
			if err := check.call(); err == nil {
				t.Fatal("rolled-back helper operation error = nil")
			}
		})
	}
}

func TestInitiativeUpdateHelpersRequireExactlyOneTarget(t *testing.T) {
	store := openInitiativeFaultStore(t)
	transaction, err := store.db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatalf("BeginTx() error = %v", err)
	}
	defer func() { _ = transaction.Rollback() }()
	if err := updateInitiativeMemberTask(context.Background(), transaction, storeTask("task-missing-update", 1)); err == nil {
		t.Fatal("updateInitiativeMemberTask(missing) error = nil")
	}
	if err := updateInitiativeRecord(
		context.Background(), transaction, persistenceInitiative("initiative-missing-update", domain.InitiativeActive, 1),
	); err == nil {
		t.Fatal("updateInitiativeRecord(missing) error = nil")
	}
}

func TestInitiativeCommitPathsRejectAlteredOperationReuse(t *testing.T) {
	ctx := context.Background()
	store := openInitiativeFaultStore(t)
	prepared := sqlitePreparedInitiativeMutation()
	recordInitiativeMemberIntents(t, store, prepared)
	if _, err := store.CommitPreparedInitiative(ctx, prepared); err != nil {
		t.Fatalf("CommitPreparedInitiative() error = %v", err)
	}
	alteredPrepared := prepared
	alteredPrepared.SubjectDigest = strings.Repeat("1", 64)
	if _, err := store.CommitPreparedInitiative(ctx, alteredPrepared); err == nil {
		t.Fatal("CommitPreparedInitiative(altered replay) error = nil")
	}
	activation := preparedInitiativeActivationMutation(prepared)
	if _, err := store.CommitInitiativeActivation(ctx, activation); err != nil {
		t.Fatalf("CommitInitiativeActivation() error = %v", err)
	}
	alteredActivation := activation
	alteredActivation.SubjectDigest = strings.Repeat("2", 64)
	if _, err := store.CommitInitiativeActivation(ctx, alteredActivation); err == nil {
		t.Fatal("CommitInitiativeActivation(altered replay) error = nil")
	}

	abandonStore, abandon := preparedInitiativeAbandonStore(t, application.AbandonDispositionReapSafe)
	if _, err := abandonStore.CommitInitiativeAbandonment(ctx, abandon); err != nil {
		t.Fatalf("CommitInitiativeAbandonment() error = %v", err)
	}
	alteredAbandon := abandon
	alteredAbandon.SubjectDigest = strings.Repeat("3", 64)
	if _, err := abandonStore.CommitInitiativeAbandonment(ctx, alteredAbandon); err == nil {
		t.Fatal("CommitInitiativeAbandonment(altered replay) error = nil")
	}
}

func TestInitiativeActivationRollsBackFinalBoundaryWrites(t *testing.T) {
	for _, test := range []struct {
		name    string
		trigger string
	}{
		{name: "initiative update", trigger: `CREATE TRIGGER refuse_activation_initiative BEFORE UPDATE ON initiatives BEGIN SELECT RAISE(ABORT, 'injected'); END`},
		{name: "operation insert", trigger: `CREATE TRIGGER refuse_activation_operation BEFORE INSERT ON operations WHEN NEW.command = 'ActivateManagedRunGroup' BEGIN SELECT RAISE(ABORT, 'injected'); END`},
	} {
		t.Run(test.name, func(t *testing.T) {
			store, _, mutation := preparedInitiativeActivationStore(t)
			mustExecInitiativeBoundary(t, store, test.trigger)
			if _, err := store.CommitInitiativeActivation(context.Background(), mutation); err == nil {
				t.Fatal("CommitInitiativeActivation(injected final write failure) error = nil")
			}
		})
	}
}

func TestInitiativeAbandonRollsBackEveryFinalBoundaryWrite(t *testing.T) {
	for _, test := range []struct {
		name    string
		trigger string
	}{
		{name: "preparation update", trigger: `CREATE TRIGGER refuse_abandon_preparation BEFORE UPDATE ON task_preparations BEGIN SELECT RAISE(ABORT, 'injected'); END`},
		{name: "initiative update", trigger: `CREATE TRIGGER refuse_abandon_initiative BEFORE UPDATE ON initiatives BEGIN SELECT RAISE(ABORT, 'injected'); END`},
		{name: "operation insert", trigger: `CREATE TRIGGER refuse_abandon_operation BEFORE INSERT ON operations WHEN NEW.command = 'AbandonManagedRunGroup' BEGIN SELECT RAISE(ABORT, 'injected'); END`},
		{name: "member outcome insert", trigger: `CREATE TRIGGER refuse_abandon_outcome BEFORE INSERT ON initiative_group_abandon_members BEGIN SELECT RAISE(ABORT, 'injected'); END`},
	} {
		t.Run(test.name, func(t *testing.T) {
			store, mutation := preparedInitiativeAbandonStore(t, application.AbandonDispositionReapSafe)
			mustExecInitiativeBoundary(t, store, test.trigger)
			if _, err := store.CommitInitiativeAbandonment(context.Background(), mutation); err == nil {
				t.Fatal("CommitInitiativeAbandonment(injected final write failure) error = nil")
			}
		})
	}
}

func TestInitiativeAggregateReportsMissingMembersAndStaleVersions(t *testing.T) {
	t.Run("missing durable member", func(t *testing.T) {
		store := openInitiativeFaultStore(t)
		task := storeTask("task-component-a", 1)
		if err := store.CreateTask(context.Background(), task); err != nil {
			t.Fatalf("CreateTask() error = %v", err)
		}
		initiative := persistenceInitiative("initiative-missing-member", domain.InitiativeActive, 2)
		if err := store.CreateInitiative(context.Background(), initiative); err != nil {
			t.Fatalf("CreateInitiative() error = %v", err)
		}
		transaction, err := store.db.BeginTx(context.Background(), nil)
		if err != nil {
			t.Fatalf("BeginTx() error = %v", err)
		}
		defer func() { _ = transaction.Rollback() }()
		if err := refreshInitiativeAggregate(
			context.Background(), transaction, task.Handle, 3, initiative.UpdatedAt.Add(time.Minute),
		); err == nil {
			t.Fatal("refreshInitiativeAggregate(missing member) error = nil")
		}
	})

	t.Run("stale aggregate version", func(t *testing.T) {
		store, _, activation := preparedInitiativeActivationStore(t)
		activated := commitActiveInitiativeForTest(t, context.Background(), store, activation)
		mustExecInitiativeBoundary(t, store, `UPDATE tasks SET state = 'cancelled'`)
		transaction, err := store.db.BeginTx(context.Background(), nil)
		if err != nil {
			t.Fatalf("BeginTx() error = %v", err)
		}
		defer func() { _ = transaction.Rollback() }()
		if err := refreshInitiativeAggregate(
			context.Background(), transaction, activation.Members[0].ExternalRunRef,
			activated.Initiative.StateVersion-1, activation.At.Add(time.Minute),
		); err == nil {
			t.Fatal("refreshInitiativeAggregate(stale version) error = nil")
		}
	})
}

func TestInitiativeLaunchAuthorizationRejectsCorruptFleetAndUnlaunchablePosture(t *testing.T) {
	t.Run("corrupt fleet task", func(t *testing.T) {
		store, _, activation := preparedInitiativeActivationStore(t)
		commitActiveInitiativeForTest(t, context.Background(), store, activation)
		task, err := store.GetTask(context.Background(), activation.Members[0].ExternalRunRef)
		if err != nil {
			t.Fatalf("GetTask() error = %v", err)
		}
		mustExecInitiativeBoundary(t, store, `UPDATE tasks SET state = 'invented' WHERE handle = ?`, activation.Members[1].ExternalRunRef)
		transaction, err := store.db.BeginTx(context.Background(), nil)
		if err != nil {
			t.Fatalf("BeginTx() error = %v", err)
		}
		defer func() { _ = transaction.Rollback() }()
		if err := authorizeInitiativeTaskStart(
			context.Background(), transaction, task, initiativeTestSchedulingLimits(2),
		); err == nil {
			t.Fatal("authorizeInitiativeTaskStart(corrupt fleet) error = nil")
		}
	})

	t.Run("prepared member has no launch posture", func(t *testing.T) {
		store := openInitiativeFaultStore(t)
		task := storeTask("task-prepared-held", 1)
		if err := store.CreateTask(context.Background(), task); err != nil {
			t.Fatalf("CreateTask() error = %v", err)
		}
		initiative := persistenceInitiative("initiative-prepared-held", domain.InitiativeActive, 2)
		initiative.Components = initiative.Components[:1]
		initiative.Components[0].TaskHandles = []string{task.Handle}
		initiative.Edges = nil
		initiative.IntegrationOwnerTask = ""
		if err := store.CreateInitiative(context.Background(), initiative); err != nil {
			t.Fatalf("CreateInitiative() error = %v", err)
		}
		transaction, err := store.db.BeginTx(context.Background(), nil)
		if err != nil {
			t.Fatalf("BeginTx() error = %v", err)
		}
		defer func() { _ = transaction.Rollback() }()
		if err := authorizeInitiativeTaskStart(
			context.Background(), transaction, task, initiativeTestSchedulingLimits(1),
		); err == nil {
			t.Fatal("authorizeInitiativeTaskStart(prepared member) error = nil")
		}
	})
}

func TestInitiativeGroupReplayRejectsCorruptActivationAndAbandonmentStorage(t *testing.T) {
	t.Run("activation expiry cannot be parsed", func(t *testing.T) {
		store, _, activation := preparedInitiativeActivationStore(t)
		mustExecInitiativeBoundary(t, store, `UPDATE initiative_preparations SET expires_at = 'invalid'`)
		if _, err := store.CommitInitiativeActivation(context.Background(), activation); err == nil {
			t.Fatal("CommitInitiativeActivation(invalid expiry) error = nil")
		}
	})

	t.Run("activation result names a missing task", func(t *testing.T) {
		store, _, activation := preparedInitiativeActivationStore(t)
		result, err := store.CommitInitiativeActivation(context.Background(), activation)
		if err != nil {
			t.Fatalf("CommitInitiativeActivation() error = %v", err)
		}
		components := append([]domain.InitiativeComponent(nil), result.Initiative.Components...)
		components[0].TaskHandles = append(append([]string(nil), components[0].TaskHandles...), "task-missing-result")
		encoded, err := json.Marshal(components)
		if err != nil {
			t.Fatalf("json.Marshal(components) error = %v", err)
		}
		mustExecInitiativeBoundary(t, store, `UPDATE initiatives SET components_json = ?`, string(encoded))
		if _, _, err := store.ReplayInitiativeActivation(
			context.Background(), activation.OperationID, activation.SubjectDigest,
		); err == nil {
			t.Fatal("ReplayInitiativeActivation(missing task) error = nil")
		}
	})

	t.Run("abandonment member table is unavailable", func(t *testing.T) {
		store, mutation := preparedInitiativeAbandonStore(t, application.AbandonDispositionReapSafe)
		if _, err := store.CommitInitiativeAbandonment(context.Background(), mutation); err != nil {
			t.Fatalf("CommitInitiativeAbandonment() error = %v", err)
		}
		mustExecInitiativeBoundary(t, store, `ALTER TABLE initiative_group_abandon_members RENAME TO unavailable_abandon_members`)
		if _, _, err := store.ReplayInitiativeAbandonment(
			context.Background(), mutation.OperationID, mutation.SubjectDigest,
		); err == nil {
			t.Fatal("ReplayInitiativeAbandonment(unavailable members) error = nil")
		}
	})
}

func openInitiativeFaultStore(t *testing.T) *Store {
	t.Helper()
	store, err := Open(context.Background(), filepath.Join(canonicalTempDir(t), "devcrew.db"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}
