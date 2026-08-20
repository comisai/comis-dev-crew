package sqlite

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/comisai/comis-dev-crew/internal/application"
	"github.com/comisai/comis-dev-crew/internal/domain"
)

func TestInitiativeActivationCommitsEveryBindingAtOneStateVersion(t *testing.T) {
	ctx := context.Background()
	store, _, mutation := preparedInitiativeActivationStore(t)
	result, err := store.CommitInitiativeActivation(ctx, mutation)
	if err != nil {
		t.Fatalf("CommitInitiativeActivation() error = %v", err)
	}
	if result.Initiative.ManagedRunGroupID != mutation.ManagedRunGroupID || result.Initiative.State != domain.InitiativeActive ||
		result.Initiative.StateVersion != result.Operation.StateVersion || len(result.Tasks) != 2 {
		t.Fatalf("CommitInitiativeActivation() = %#v, want one active two-member group", result)
	}
	for _, task := range result.Tasks {
		if task.State != domain.TaskReady || task.StateVersion != result.Operation.StateVersion || task.ManagedRunID == "" ||
			task.WorkspaceLeaseID == "" || task.ExecutionAttachmentID == "" || task.AttachmentTargetName == "" {
			t.Fatalf("bound member = %#v, want one versioned ready binding", task)
		}
	}
	replay, found, err := store.ReplayInitiativeActivation(ctx, mutation.OperationID, mutation.SubjectDigest)
	if err != nil || !found || !reflect.DeepEqual(replay, result) {
		t.Fatalf("ReplayInitiativeActivation() = %#v, %t, %v, want %#v", replay, found, err, result)
	}
	if _, _, err := store.ReplayInitiativeActivation(
		ctx, mutation.OperationID, strings.Repeat("f", 64),
	); !errors.Is(err, application.ErrConflict) {
		t.Fatalf("ReplayInitiativeActivation(altered) error = %v, want ErrConflict", err)
	}
}

func TestInitiativeActivationRollsBackTheWholeGroupOnMemberFailure(t *testing.T) {
	ctx := context.Background()
	store, initiativeHandle, mutation := preparedInitiativeActivationStore(t)
	if _, err := store.db.ExecContext(ctx, `CREATE TRIGGER refuse_group_member_binding
		BEFORE UPDATE ON tasks WHEN NEW.handle = 'task-integration'
		BEGIN SELECT RAISE(ABORT, 'injected group member failure'); END`); err != nil {
		t.Fatalf("install group activation failure trigger: %v", err)
	}
	if _, err := store.CommitInitiativeActivation(ctx, mutation); err == nil {
		t.Fatal("CommitInitiativeActivation(injected failure) error = nil")
	}
	initiative, err := store.GetInitiative(ctx, initiativeHandle)
	if err != nil || initiative.State != domain.InitiativePreparing || initiative.ManagedRunGroupID != "" {
		t.Fatalf("initiative after rollback = %#v, %v", initiative, err)
	}
	for _, member := range mutation.Members {
		task, err := store.GetTask(ctx, member.ExternalRunRef)
		if err != nil || task.State != domain.TaskPrepared || task.ManagedRunID != "" {
			t.Fatalf("task %q after rollback = %#v, %v", member.ExternalRunRef, task, err)
		}
	}
	if _, err := store.GetOperation(ctx, mutation.OperationID); !errors.Is(err, application.ErrNotFound) {
		t.Fatalf("activation operation after rollback error = %v, want ErrNotFound", err)
	}
}

func preparedInitiativeActivationStore(t *testing.T) (*Store, string, application.ManagedRunGroupActivationMutation) {
	t.Helper()
	ctx := context.Background()
	store, err := Open(ctx, filepath.Join(canonicalTempDir(t), "devcrew.db"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	preparation := sqlitePreparedInitiativeMutation()
	recordInitiativeMemberIntents(t, store, preparation)
	if _, err := store.CommitPreparedInitiative(ctx, preparation); err != nil {
		t.Fatalf("CommitPreparedInitiative() error = %v", err)
	}
	members := make([]application.ManagedRunGroupActivationMember, 0, len(preparation.Members))
	for index, member := range preparation.Members {
		members = append(members, application.ManagedRunGroupActivationMember{
			ExternalRunRef: member.Task.Handle, RegistrationNonce: member.Preparation.RegistrationNonce,
			Binding: domain.TaskBinding{
				ManagedRunID:     "managed-run-" + member.Task.Handle,
				WorkspaceLeaseID: "workspace-lease-" + member.Task.Handle,
			},
			ExecutionAttachmentID: "execution-attachment-" + member.Task.Handle,
			AttachmentTargetName:  fmt.Sprintf("attachment-%032x.sock", index+1),
		})
	}
	return store, preparation.Initiative.Handle, application.ManagedRunGroupActivationMutation{
		ServiceInstanceID: preparation.Members[0].Task.ServiceInstanceID,
		ManagedRunGroupID: "managed-run-group-0001", RegistrationNonce: preparation.GroupRegistrationNonce,
		Members: members, OperationID: "activate-initiative-0001", SubjectDigest: strings.Repeat("d", 64),
		At: preparation.At.Add(time.Minute),
	}
}
