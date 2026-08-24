package sqlite

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/comisai/comis-dev-crew/internal/application"
	"github.com/comisai/comis-dev-crew/internal/domain"
)

func TestInitiativeStoreReplaysMissingAndRepeatedGroupMutations(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, filepath.Join(canonicalTempDir(t), "devcrew.db"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	prepared := sqlitePreparedInitiativeMutation()
	recordInitiativeMemberIntents(t, store, prepared)
	if _, found, err := store.ReplayInitiativePreparation(ctx, prepared.OperationID, prepared.SubjectDigest); err != nil || found {
		t.Fatalf("ReplayInitiativePreparation(missing) found/error = %t/%v", found, err)
	}
	firstPrepared, err := store.CommitPreparedInitiative(ctx, prepared)
	if err != nil {
		t.Fatalf("CommitPreparedInitiative() error = %v", err)
	}
	replayedPrepared, err := store.CommitPreparedInitiative(ctx, prepared)
	if err != nil || !reflect.DeepEqual(replayedPrepared, firstPrepared) {
		t.Fatalf("CommitPreparedInitiative(replay) = %#v, %v", replayedPrepared, err)
	}

	activation := preparedInitiativeActivationMutation(prepared)
	if _, found, err := store.ReplayInitiativeActivation(ctx, activation.OperationID, activation.SubjectDigest); err != nil || found {
		t.Fatalf("ReplayInitiativeActivation(missing) found/error = %t/%v", found, err)
	}
	firstActivation, err := store.CommitInitiativeActivation(ctx, activation)
	if err != nil {
		t.Fatalf("CommitInitiativeActivation() error = %v", err)
	}
	replayedActivation, err := store.CommitInitiativeActivation(ctx, activation)
	if err != nil || !reflect.DeepEqual(replayedActivation, firstActivation) {
		t.Fatalf("CommitInitiativeActivation(replay) = %#v, %v", replayedActivation, err)
	}
}

func TestInitiativeAbandonStoreReplaysMissingAndRepeatedMutation(t *testing.T) {
	ctx := context.Background()
	store, mutation := preparedInitiativeAbandonStore(t, application.AbandonDispositionReapSafe)
	if _, found, err := store.ReplayInitiativeAbandonment(ctx, mutation.OperationID, mutation.SubjectDigest); err != nil || found {
		t.Fatalf("ReplayInitiativeAbandonment(missing) found/error = %t/%v", found, err)
	}
	first, err := store.CommitInitiativeAbandonment(ctx, mutation)
	if err != nil {
		t.Fatalf("CommitInitiativeAbandonment() error = %v", err)
	}
	replayed, err := store.CommitInitiativeAbandonment(ctx, mutation)
	if err != nil || !reflect.DeepEqual(replayed, first) {
		t.Fatalf("CommitInitiativeAbandonment(replay) = %#v, %v", replayed, err)
	}
}

func TestInitiativeGroupStoreMethodsReportClosedDatabaseBoundaries(t *testing.T) {
	ctx := context.Background()
	store, _, activation := preparedInitiativeActivationStore(t)
	abandonStore, abandon := preparedInitiativeAbandonStore(t, application.AbandonDispositionReapSafe)
	prepared := sqlitePreparedInitiativeMutation()
	if err := store.Close(); err != nil {
		t.Fatalf("Close(activation store) error = %v", err)
	}
	if err := abandonStore.Close(); err != nil {
		t.Fatalf("Close(abandon store) error = %v", err)
	}
	checks := []struct {
		name string
		call func() error
	}{
		{name: "replay preparation", call: func() error {
			_, _, err := store.ReplayInitiativePreparation(ctx, prepared.OperationID, prepared.SubjectDigest)
			return err
		}},
		{name: "commit preparation", call: func() error { _, err := store.CommitPreparedInitiative(ctx, prepared); return err }},
		{name: "replay activation", call: func() error {
			_, _, err := store.ReplayInitiativeActivation(ctx, activation.OperationID, activation.SubjectDigest)
			return err
		}},
		{name: "commit activation", call: func() error { _, err := store.CommitInitiativeActivation(ctx, activation); return err }},
		{name: "replay abandonment", call: func() error {
			_, _, err := abandonStore.ReplayInitiativeAbandonment(ctx, abandon.OperationID, abandon.SubjectDigest)
			return err
		}},
		{name: "commit abandonment", call: func() error { _, err := abandonStore.CommitInitiativeAbandonment(ctx, abandon); return err }},
	}
	for _, check := range checks {
		t.Run(check.name, func(t *testing.T) {
			if err := check.call(); err == nil {
				t.Fatal("closed database operation error = nil")
			}
		})
	}
}

func TestInitiativeActivationRejectsStaleOrInexactPreparedGroups(t *testing.T) {
	tests := []struct {
		name  string
		alter func(*testing.T, *Store, *application.ManagedRunGroupActivationMutation)
	}{
		{name: "unknown group nonce", alter: func(_ *testing.T, _ *Store, mutation *application.ManagedRunGroupActivationMutation) {
			mutation.RegistrationNonce = "registration-nonce_missing"
		}},
		{name: "expired preparation", alter: func(_ *testing.T, _ *Store, mutation *application.ManagedRunGroupActivationMutation) {
			mutation.At = mutation.At.Add(time.Hour)
		}},
		{name: "initiative is no longer preparing", alter: func(t *testing.T, store *Store, _ *application.ManagedRunGroupActivationMutation) {
			mustExecInitiativeBoundary(t, store, `UPDATE initiatives SET state = 'failed'`)
		}},
		{name: "member count is incomplete", alter: func(_ *testing.T, _ *Store, mutation *application.ManagedRunGroupActivationMutation) {
			mutation.Members = mutation.Members[:1]
		}},
		{name: "member handle is substituted", alter: func(_ *testing.T, _ *Store, mutation *application.ManagedRunGroupActivationMutation) {
			mutation.Members[1].ExternalRunRef = "task-substituted"
		}},
		{name: "member nonce differs", alter: func(_ *testing.T, _ *Store, mutation *application.ManagedRunGroupActivationMutation) {
			mutation.Members[0].RegistrationNonce = "registration-nonce_forged"
		}},
		{name: "member service differs", alter: func(_ *testing.T, _ *Store, mutation *application.ManagedRunGroupActivationMutation) {
			mutation.ServiceInstanceID = "service-instance-forged"
		}},
		{name: "member preparation is missing", alter: func(t *testing.T, store *Store, _ *application.ManagedRunGroupActivationMutation) {
			mustExecInitiativeBoundary(t, store, `DELETE FROM task_preparations WHERE task_handle = 'task-component-a'`)
		}},
		{name: "binding time precedes member", alter: func(_ *testing.T, _ *Store, mutation *application.ManagedRunGroupActivationMutation) {
			mutation.At = mutation.At.Add(-2 * time.Minute)
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			store, _, mutation := preparedInitiativeActivationStore(t)
			mutation.Members = append([]application.ManagedRunGroupActivationMember(nil), mutation.Members...)
			test.alter(t, store, &mutation)
			if _, err := store.CommitInitiativeActivation(context.Background(), mutation); err == nil {
				t.Fatal("CommitInitiativeActivation(inexact group) error = nil")
			}
		})
	}
}

func TestInitiativeAbandonRejectsUnsafeOrInexactPreparedGroups(t *testing.T) {
	tests := []struct {
		name  string
		alter func(*testing.T, *Store, *application.ManagedRunGroupAbandonmentMutation)
	}{
		{name: "unknown group nonce", alter: func(_ *testing.T, _ *Store, mutation *application.ManagedRunGroupAbandonmentMutation) {
			mutation.RegistrationNonce = "registration-nonce_missing"
		}},
		{name: "initiative is already terminal", alter: func(t *testing.T, store *Store, _ *application.ManagedRunGroupAbandonmentMutation) {
			mustExecInitiativeBoundary(t, store, `UPDATE initiatives SET state = 'delivered'`)
		}},
		{name: "bound group differs", alter: func(t *testing.T, store *Store, _ *application.ManagedRunGroupAbandonmentMutation) {
			mustExecInitiativeBoundary(t, store, `UPDATE initiatives SET state = 'unknown', managed_run_group_id = 'managed-run-group-other'`)
		}},
		{name: "member count is incomplete", alter: func(_ *testing.T, _ *Store, mutation *application.ManagedRunGroupAbandonmentMutation) {
			mutation.Members = mutation.Members[:1]
		}},
		{name: "member task is missing", alter: func(_ *testing.T, _ *Store, mutation *application.ManagedRunGroupAbandonmentMutation) {
			mutation.Members[1].ExternalRunRef = "task-substituted"
		}},
		{name: "member nonce differs", alter: func(_ *testing.T, _ *Store, mutation *application.ManagedRunGroupAbandonmentMutation) {
			mutation.Members[0].RegistrationNonce = "registration-nonce_forged"
		}},
		{name: "member service differs", alter: func(_ *testing.T, _ *Store, mutation *application.ManagedRunGroupAbandonmentMutation) {
			mutation.ServiceInstanceID = "service-instance-forged"
		}},
		{name: "abandon time precedes member", alter: func(_ *testing.T, _ *Store, mutation *application.ManagedRunGroupAbandonmentMutation) {
			mutation.At = mutation.At.Add(-2 * time.Minute)
		}},
		{name: "working member is unsafe", alter: func(t *testing.T, store *Store, mutation *application.ManagedRunGroupAbandonmentMutation) {
			mustExecInitiativeBoundary(t, store, `UPDATE tasks SET state = 'working', managed_run_id = ?, workspace_lease_id = ? WHERE handle = ?`,
				mutation.Members[0].ManagedRunID, "workspace-lease-working", mutation.Members[0].ExternalRunRef)
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			store, mutation := preparedInitiativeAbandonStore(t, application.AbandonDispositionReapSafe)
			mutation.Members = append([]application.ManagedRunGroupAbandonmentMember(nil), mutation.Members...)
			test.alter(t, store, &mutation)
			if _, err := store.CommitInitiativeAbandonment(context.Background(), mutation); err == nil {
				t.Fatal("CommitInitiativeAbandonment(unsafe group) error = nil")
			}
		})
	}
}

func TestInitiativeAbandonPersistsUnknownMemberAsPartialGroup(t *testing.T) {
	store, mutation := preparedInitiativeAbandonStore(t, application.AbandonDispositionReapSafe)
	mustExecInitiativeBoundary(t, store, `UPDATE tasks SET state = 'unknown' WHERE handle = ?`, mutation.Members[0].ExternalRunRef)
	result, err := store.CommitInitiativeAbandonment(context.Background(), mutation)
	if err != nil {
		t.Fatalf("CommitInitiativeAbandonment(unknown member) error = %v", err)
	}
	if result.Initiative.State != domain.InitiativeUnknown || result.Members[0].Outcome != application.InitiativeActivationUnknown {
		t.Fatalf("partial abandonment = %#v", result)
	}
}

func TestInitiativeAbandonReplayRejectsCorruptStoredOutcomes(t *testing.T) {
	for _, test := range []struct {
		name    string
		corrupt string
	}{
		{name: "unknown outcome", corrupt: `UPDATE initiative_group_abandon_members SET outcome = 'invented' WHERE ordinal = 0`},
		{name: "mixed disposition", corrupt: `UPDATE task_preparations SET disposition = 'preserve' WHERE task_handle = 'task-component-a'`},
		{name: "missing members", corrupt: `DELETE FROM initiative_group_abandon_members`},
	} {
		t.Run(test.name, func(t *testing.T) {
			store, mutation := preparedInitiativeAbandonStore(t, application.AbandonDispositionReapSafe)
			if _, err := store.CommitInitiativeAbandonment(context.Background(), mutation); err != nil {
				t.Fatalf("CommitInitiativeAbandonment() error = %v", err)
			}
			mustExecInitiativeBoundary(t, store, test.corrupt)
			if _, _, err := store.ReplayInitiativeAbandonment(context.Background(), mutation.OperationID, mutation.SubjectDigest); err == nil {
				t.Fatal("ReplayInitiativeAbandonment(corrupt result) error = nil")
			}
		})
	}
}

func TestPreparedInitiativeValidationRejectsEveryAuthorityMismatch(t *testing.T) {
	for _, test := range []struct {
		name  string
		alter func(*application.PreparedInitiativeMutation)
	}{
		{name: "active initiative", alter: func(mutation *application.PreparedInitiativeMutation) {
			mutation.Initiative.State = domain.InitiativeActive
		}},
		{name: "member repository", alter: func(mutation *application.PreparedInitiativeMutation) {
			mutation.Members[0].Task.RepositoryID = "repo-other"
			mutation.Members[0].Task, _ = mutation.Members[0].Task.PinBriefRevision()
		}},
		{name: "member service", alter: func(mutation *application.PreparedInitiativeMutation) {
			mutation.Members[1].Task.ServiceInstanceID = "service-instance-other"
		}},
		{name: "duplicate task", alter: func(mutation *application.PreparedInitiativeMutation) {
			mutation.Members[1].Task = mutation.Members[0].Task
			mutation.Members[1].Preparation.ExternalRunRef = mutation.Members[0].Task.Handle
		}},
		{name: "duplicate operation", alter: func(mutation *application.PreparedInitiativeMutation) {
			mutation.Members[1].OperationID = mutation.Members[0].OperationID
		}},
		{name: "incomplete set", alter: func(mutation *application.PreparedInitiativeMutation) { mutation.Members = mutation.Members[:1] }},
		{name: "invalid group nonce", alter: func(mutation *application.PreparedInitiativeMutation) { mutation.GroupRegistrationNonce = "bad nonce" }},
	} {
		t.Run(test.name, func(t *testing.T) {
			mutation := sqlitePreparedInitiativeMutation()
			test.alter(&mutation)
			if err := validatePreparedInitiativeMutation(mutation); err == nil {
				t.Fatal("validatePreparedInitiativeMutation(authority mismatch) error = nil")
			}
		})
	}
}

func TestPreparedInitiativeCommitRollsBackBoundaryWriteFailures(t *testing.T) {
	for _, test := range []struct {
		name    string
		trigger string
	}{
		{name: "group preparation insert", trigger: `CREATE TRIGGER refuse_initiative_preparation BEFORE INSERT ON initiative_preparations BEGIN SELECT RAISE(ABORT, 'injected'); END`},
		{name: "member operation insert", trigger: `CREATE TRIGGER refuse_initiative_member_operation BEFORE INSERT ON operations WHEN NEW.command = 'PrepareTask' BEGIN SELECT RAISE(ABORT, 'injected'); END`},
		{name: "group operation insert", trigger: `CREATE TRIGGER refuse_initiative_group_operation BEFORE INSERT ON operations WHEN NEW.command = 'PrepareInitiative' BEGIN SELECT RAISE(ABORT, 'injected'); END`},
	} {
		t.Run(test.name, func(t *testing.T) {
			store, err := Open(context.Background(), filepath.Join(canonicalTempDir(t), "devcrew.db"))
			if err != nil {
				t.Fatalf("Open() error = %v", err)
			}
			t.Cleanup(func() { _ = store.Close() })
			mutation := sqlitePreparedInitiativeMutation()
			recordInitiativeMemberIntents(t, store, mutation)
			mustExecInitiativeBoundary(t, store, test.trigger)
			if _, err := store.CommitPreparedInitiative(context.Background(), mutation); err == nil {
				t.Fatal("CommitPreparedInitiative(injected failure) error = nil")
			}
			if _, err := store.GetInitiative(context.Background(), mutation.Initiative.Handle); !errors.Is(err, application.ErrNotFound) {
				t.Fatalf("GetInitiative(after rollback) error = %v", err)
			}
		})
	}
}

func TestInitiativeAggregateAndLaunchRejectOverlappingOrCorruptMembership(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, filepath.Join(canonicalTempDir(t), "devcrew.db"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	task := storeTask("task-overlapping", 1)
	task.State = domain.TaskReady
	task.ManagedRunID = "managed-run-overlapping"
	task.WorkspaceLeaseID = "workspace-lease-overlapping"
	if err := store.CreateTask(ctx, task); err != nil {
		t.Fatalf("CreateTask() error = %v", err)
	}
	unrelated := persistenceInitiative("initiative-a-unrelated", domain.InitiativeActive, 2)
	if err := store.CreateInitiative(ctx, unrelated); err != nil {
		t.Fatalf("CreateInitiative(unrelated) error = %v", err)
	}
	for index, handle := range []string{"initiative-overlap-a", "initiative-overlap-b"} {
		initiative := persistenceInitiative(handle, domain.InitiativeActive, int64(index+3))
		initiative.Components[0].TaskHandles = []string{task.Handle}
		initiative.Components = initiative.Components[:1]
		initiative.Edges = nil
		initiative.IntegrationOwnerTask = ""
		initiative.ManagedRunGroupID = "managed-run-group-" + handle
		if err := store.CreateInitiative(ctx, initiative); err != nil {
			t.Fatalf("CreateInitiative(%q) error = %v", handle, err)
		}
	}
	transaction, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("BeginTx() error = %v", err)
	}
	defer func() { _ = transaction.Rollback() }()
	if err := refreshInitiativeAggregate(ctx, transaction, task.Handle, 5, task.UpdatedAt.Add(time.Minute)); err == nil {
		t.Fatal("refreshInitiativeAggregate(overlap) error = nil")
	}
	if err := authorizeInitiativeTaskStart(ctx, transaction, task, initiativeTestSchedulingLimits(2)); err == nil {
		t.Fatal("authorizeInitiativeTaskStart(overlap) error = nil")
	}
}

func TestInitiativeLaunchRejectsInvalidReviewedLimitsInsideTransaction(t *testing.T) {
	ctx := context.Background()
	store, _, activation := preparedInitiativeActivationStore(t)
	commitActiveInitiativeForTest(t, ctx, store, activation)
	task, err := store.GetTask(ctx, activation.Members[0].ExternalRunRef)
	if err != nil {
		t.Fatalf("GetTask() error = %v", err)
	}
	transaction, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("BeginTx() error = %v", err)
	}
	defer func() { _ = transaction.Rollback() }()
	invalid := &application.InitiativeSchedulingLimits{MaxConcurrentTasks: 1, MaxConcurrentTasksPerRepository: 2}
	if err := authorizeInitiativeTaskStart(ctx, transaction, task, invalid); err == nil {
		t.Fatal("authorizeInitiativeTaskStart(invalid limits) error = nil")
	}
}

func preparedInitiativeActivationMutation(
	prepared application.PreparedInitiativeMutation,
) application.ManagedRunGroupActivationMutation {
	members := make([]application.ManagedRunGroupActivationMember, 0, len(prepared.Members))
	for index, member := range prepared.Members {
		members = append(members, application.ManagedRunGroupActivationMember{
			ExternalRunRef: member.Task.Handle, RegistrationNonce: member.Preparation.RegistrationNonce,
			Binding: domain.TaskBinding{
				ManagedRunID: "managed-run-" + member.Task.Handle, WorkspaceLeaseID: "workspace-lease-" + member.Task.Handle,
			},
			ExecutionAttachmentID: "execution-attachment-" + member.Task.Handle,
			AttachmentTargetName:  "attachment-" + strings.Repeat(string(rune('a'+index)), 32) + ".sock",
		})
	}
	return application.ManagedRunGroupActivationMutation{
		ServiceInstanceID: prepared.Members[0].Task.ServiceInstanceID,
		ManagedRunGroupID: "managed-run-group-0001", RegistrationNonce: prepared.GroupRegistrationNonce,
		Members: members, OperationID: "activate-initiative-boundary", SubjectDigest: strings.Repeat("f", 64),
		At: prepared.At.Add(time.Minute),
	}
}

func mustExecInitiativeBoundary(t *testing.T, store *Store, statement string, args ...any) {
	t.Helper()
	if _, err := store.db.ExecContext(context.Background(), statement, args...); err != nil {
		t.Fatalf("initiative boundary fixture write error = %v", err)
	}
}
