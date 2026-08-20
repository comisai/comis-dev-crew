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

func TestPreparedInitiativeCommitsAndReplaysAllMembersInOneTransaction(t *testing.T) {
	ctx := context.Background()
	databasePath := filepath.Join(canonicalTempDir(t), "devcrew.db")
	store, err := Open(ctx, databasePath)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	mutation := sqlitePreparedInitiativeMutation()
	recordInitiativeMemberIntents(t, store, mutation)
	result, err := store.CommitPreparedInitiative(ctx, mutation)
	if err != nil {
		t.Fatalf("CommitPreparedInitiative() error = %v", err)
	}
	if len(result.Tasks) != 2 || len(result.Preparation.Members) != 2 ||
		result.Initiative.StateVersion != result.Operation.StateVersion {
		t.Fatalf("CommitPreparedInitiative() = %#v, want one versioned two-member result", result)
	}
	for _, task := range result.Tasks {
		if task.StateVersion != result.Operation.StateVersion {
			t.Fatalf("task %q version = %d, want %d", task.Handle, task.StateVersion, result.Operation.StateVersion)
		}
		operationID := initiativeMemberOperationForTest(mutation, task.Handle)
		operation, err := store.GetOperation(ctx, operationID)
		if err != nil || operation.Command != "PrepareTask" || operation.ResultRef != task.Handle ||
			operation.StateVersion != result.Operation.StateVersion {
			t.Fatalf("member operation %q = %#v, %v", operationID, operation, err)
		}
	}
	intents, err := store.ListTaskPreparationIntents(ctx)
	if err != nil || len(intents) != 0 {
		t.Fatalf("remaining member intents = %#v, %v, want none", intents, err)
	}
	replayed, found, err := store.ReplayInitiativePreparation(ctx, mutation.OperationID, mutation.SubjectDigest)
	if err != nil || !found || !reflect.DeepEqual(replayed, result) {
		t.Fatalf("ReplayInitiativePreparation() = %#v, %t, %v, want %#v", replayed, found, err, result)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	reopened, err := Open(ctx, databasePath)
	if err != nil {
		t.Fatalf("Open(restart) error = %v", err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	restarted, found, err := reopened.ReplayInitiativePreparation(ctx, mutation.OperationID, mutation.SubjectDigest)
	if err != nil || !found || !reflect.DeepEqual(restarted, result) {
		t.Fatalf("ReplayInitiativePreparation(restart) = %#v, %t, %v, want %#v", restarted, found, err, result)
	}
	if _, _, err := reopened.ReplayInitiativePreparation(
		ctx, mutation.OperationID, strings.Repeat("f", 64),
	); !errors.Is(err, application.ErrConflict) {
		t.Fatalf("ReplayInitiativePreparation(altered) error = %v, want ErrConflict", err)
	}
}

func TestPreparedInitiativeRollsBackEveryDurableRecordOnMemberFailure(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, filepath.Join(canonicalTempDir(t), "devcrew.db"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	mutation := sqlitePreparedInitiativeMutation()
	recordInitiativeMemberIntents(t, store, mutation)
	if _, err := store.db.ExecContext(ctx, `CREATE TRIGGER refuse_integration_member
		BEFORE INSERT ON tasks WHEN NEW.handle = 'task-integration'
		BEGIN SELECT RAISE(ABORT, 'injected member failure'); END`); err != nil {
		t.Fatalf("install member failure trigger: %v", err)
	}
	if _, err := store.CommitPreparedInitiative(ctx, mutation); err == nil {
		t.Fatal("CommitPreparedInitiative(injected failure) error = nil")
	}
	for table, want := range map[string]int{
		"initiatives": 0, "initiative_preparations": 0, "tasks": 0,
		"task_preparations": 0, "operations": 0, "task_preparation_intents": 2,
	} {
		var count int
		if err := store.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+table).Scan(&count); err != nil { // #nosec G202 -- table names are a closed test fixture.
			t.Fatalf("count %s: %v", table, err)
		}
		if count != want {
			t.Fatalf("%s rows = %d, want %d after rollback", table, count, want)
		}
	}
}

func sqlitePreparedInitiativeMutation() application.PreparedInitiativeMutation {
	at := time.Date(2026, time.August, 20, 16, 0, 0, 0, time.UTC)
	initiative := persistenceInitiative("initiative-prepare-0001", domain.InitiativePreparing, 1)
	initiative.ManagedRunGroupID = ""
	initiative.CreatedAt = at
	initiative.UpdatedAt = at
	members := make([]application.PreparedInitiativeMember, 0, 2)
	for index, handle := range []string{"task-component-a", "task-integration"} {
		task := storeTask(handle, 1)
		task.RepositoryID = "repo-primary"
		task.BaseRevision = initiative.BaseRevisionSet[0].Revision
		task.CreatedAt = at
		task.UpdatedAt = at
		task, _ = task.PinBriefRevision()
		operationID := "prepare-member-000" + string(rune('1'+index))
		members = append(members, application.PreparedInitiativeMember{
			Task: task,
			Preparation: application.ManagedRunPreparation{
				ExternalRunRef: handle, RegistrationNonce: "registration-nonce_" + handle,
				RequestedWorkspaceRoot: "/approved/workspaces/" + handle,
				RequestedAttachment: application.PreparedRuntimeAttachment{
					Kind:          application.RuntimeAttachmentUnixSocket,
					SourcePath:    "/approved/runtime/" + handle + "/attachment.sock",
					RelayIdentity: strings.Repeat("ab", 32),
				},
				ExpiresAt: at.Add(time.Hour), State: application.PreparationOpen,
			},
			OperationID: operationID, SubjectDigest: strings.Repeat(string(rune('a'+index)), 64),
		})
	}
	return application.PreparedInitiativeMutation{
		Initiative: initiative, Members: members,
		GroupRegistrationNonce: "registration-nonce_group", GroupExpiresAt: at.Add(time.Hour),
		OperationID: "prepare-initiative-store", SubjectDigest: strings.Repeat("c", 64), At: at,
	}
}

func recordInitiativeMemberIntents(
	t *testing.T,
	store *Store,
	mutation application.PreparedInitiativeMutation,
) {
	t.Helper()
	for _, member := range mutation.Members {
		if _, err := store.RecordTaskPreparationIntent(context.Background(), application.TaskPreparationIntent{
			OperationID: member.OperationID, TaskHandle: member.Task.Handle,
			SubjectDigest: member.SubjectDigest, CreatedAt: mutation.At,
		}); err != nil {
			t.Fatalf("RecordTaskPreparationIntent(%q) error = %v", member.Task.Handle, err)
		}
	}
}

func initiativeMemberOperationForTest(
	mutation application.PreparedInitiativeMutation,
	taskHandle string,
) string {
	for _, member := range mutation.Members {
		if member.Task.Handle == taskHandle {
			return member.OperationID
		}
	}
	return ""
}
