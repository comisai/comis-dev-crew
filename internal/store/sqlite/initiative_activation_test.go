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

func TestInitiativeActivationStateMovesByExactBoundGroup(t *testing.T) {
	ctx := context.Background()
	store, initiativeHandle, mutation := preparedInitiativeActivationStore(t)
	activated, err := store.CommitInitiativeActivation(ctx, mutation)
	if err != nil {
		t.Fatalf("CommitInitiativeActivation() error = %v", err)
	}
	unknownAt := mutation.At.Add(time.Minute)
	unknown, err := store.SetInitiativeActivationState(ctx, mutation.ManagedRunGroupID, domain.InitiativeUnknown, unknownAt)
	if err != nil || unknown.State != domain.InitiativeUnknown || unknown.StateVersion <= activated.Initiative.StateVersion {
		t.Fatalf("SetInitiativeActivationState(unknown) = %#v, %v", unknown, err)
	}
	replayed, err := store.SetInitiativeActivationState(ctx, mutation.ManagedRunGroupID, domain.InitiativeUnknown, unknownAt)
	if err != nil || !reflect.DeepEqual(replayed, unknown) {
		t.Fatalf("SetInitiativeActivationState(replay) = %#v, %v", replayed, err)
	}
	activeAt := unknownAt.Add(time.Minute)
	active, err := store.SetInitiativeActivationState(ctx, mutation.ManagedRunGroupID, domain.InitiativeActive, activeAt)
	if err != nil || active.State != domain.InitiativeActive || !active.UpdatedAt.Equal(activeAt) {
		t.Fatalf("SetInitiativeActivationState(active) = %#v, %v", active, err)
	}
	if _, err := store.SetInitiativeActivationState(ctx, mutation.ManagedRunGroupID, domain.InitiativeUnknown, unknownAt); !errors.Is(err, application.ErrPrecondition) {
		t.Fatalf("SetInitiativeActivationState(backward time) error = %v", err)
	}
	if _, err := store.SetInitiativeActivationState(ctx, "managed-run-group-missing", domain.InitiativeUnknown, activeAt); !errors.Is(err, application.ErrNotFound) {
		t.Fatalf("SetInitiativeActivationState(missing group) error = %v", err)
	}
	for _, invalid := range []struct {
		group string
		state domain.InitiativeState
		at    time.Time
	}{
		{group: "bad group", state: domain.InitiativeUnknown, at: activeAt},
		{group: mutation.ManagedRunGroupID, state: domain.InitiativeBlocked, at: activeAt},
		{group: mutation.ManagedRunGroupID, state: domain.InitiativeUnknown, at: time.Date(2026, time.August, 20, 20, 0, 0, 0, time.FixedZone("test", 3_600))},
	} {
		if _, err := store.SetInitiativeActivationState(ctx, invalid.group, invalid.state, invalid.at); !errors.Is(err, application.ErrInvalidInput) {
			t.Fatalf("SetInitiativeActivationState(invalid) error = %v", err)
		}
	}
	if _, err := store.db.ExecContext(ctx, `UPDATE initiatives SET state = 'delivered' WHERE handle = ?`, initiativeHandle); err != nil {
		t.Fatalf("seed terminal initiative: %v", err)
	}
	if _, err := store.SetInitiativeActivationState(ctx, mutation.ManagedRunGroupID, domain.InitiativeUnknown, activeAt.Add(time.Minute)); !errors.Is(err, application.ErrPrecondition) {
		t.Fatalf("SetInitiativeActivationState(terminal) error = %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if _, err := store.SetInitiativeActivationState(ctx, mutation.ManagedRunGroupID, domain.InitiativeUnknown, activeAt.Add(2*time.Minute)); err == nil {
		t.Fatal("SetInitiativeActivationState(closed store) error = nil")
	}
}

func TestInitiativeActivationMutationValidationRejectsForgedMembers(t *testing.T) {
	_, _, valid := preparedInitiativeActivationStore(t)
	tests := []func(*application.ManagedRunGroupActivationMutation){
		func(m *application.ManagedRunGroupActivationMutation) { m.OperationID = "bad id" },
		func(m *application.ManagedRunGroupActivationMutation) { m.Members[0].ExternalRunRef = "../task" },
		func(m *application.ManagedRunGroupActivationMutation) {
			m.Members[1].ExternalRunRef = m.Members[0].ExternalRunRef
		},
		func(m *application.ManagedRunGroupActivationMutation) {
			m.Members[1].Binding.ManagedRunID = m.Members[0].Binding.ManagedRunID
		},
	}
	for _, mutate := range tests {
		mutation := valid
		mutation.Members = append([]application.ManagedRunGroupActivationMember(nil), valid.Members...)
		mutate(&mutation)
		if err := validateManagedRunGroupActivationMutation(mutation); !errors.Is(err, application.ErrInvalidInput) {
			t.Fatalf("validateManagedRunGroupActivationMutation() error = %v", err)
		}
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
