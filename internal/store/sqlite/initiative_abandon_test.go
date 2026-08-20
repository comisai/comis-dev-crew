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

func TestInitiativeAbandonClosesEveryPreparedMemberAtOneStateVersion(t *testing.T) {
	ctx := context.Background()
	store, mutation := preparedInitiativeAbandonStore(t, application.AbandonDispositionReapSafe)
	result, err := store.CommitInitiativeAbandonment(ctx, mutation)
	if err != nil {
		t.Fatalf("CommitInitiativeAbandonment() error = %v", err)
	}
	if result.Initiative.ManagedRunGroupID != mutation.ManagedRunGroupID ||
		result.Initiative.State != domain.InitiativeCancelled ||
		result.Initiative.StateVersion != result.Operation.StateVersion || len(result.Members) != 2 {
		t.Fatalf("CommitInitiativeAbandonment() = %#v", result)
	}
	for index, member := range mutation.Members {
		if result.Members[index].ManagedRunID != member.ManagedRunID ||
			result.Members[index].Outcome != application.InitiativeActivationCompleted {
			t.Fatalf("member outcome %d = %#v", index, result.Members[index])
		}
		task, taskErr := store.GetTask(ctx, member.ExternalRunRef)
		if taskErr != nil || task.State != domain.TaskCancelled || task.StateVersion != result.Operation.StateVersion {
			t.Fatalf("abandoned task = %#v, %v", task, taskErr)
		}
		preparation, preparationErr := store.GetManagedRunPreparation(ctx, member.ExternalRunRef)
		if preparationErr != nil || preparation.State != application.PreparationAbandoned ||
			preparation.Disposition != mutation.Disposition || preparation.ClosedAt == nil {
			t.Fatalf("abandoned preparation = %#v, %v", preparation, preparationErr)
		}
	}
	replay, found, err := store.ReplayInitiativeAbandonment(ctx, mutation.OperationID, mutation.SubjectDigest)
	if err != nil || !found || !reflect.DeepEqual(replay, result) {
		t.Fatalf("ReplayInitiativeAbandonment() = %#v, %t, %v, want %#v", replay, found, err, result)
	}
	if _, _, err := store.ReplayInitiativeAbandonment(
		ctx, mutation.OperationID, strings.Repeat("f", 64),
	); !errors.Is(err, application.ErrConflict) {
		t.Fatalf("ReplayInitiativeAbandonment(altered) error = %v, want ErrConflict", err)
	}
}

func TestInitiativeAbandonPreservesReversiblePreparedArtifacts(t *testing.T) {
	ctx := context.Background()
	store, mutation := preparedInitiativeAbandonStore(t, application.AbandonDispositionPreserve)
	result, err := store.CommitInitiativeAbandonment(ctx, mutation)
	if err != nil {
		t.Fatalf("CommitInitiativeAbandonment(preserve) error = %v", err)
	}
	if result.Initiative.State != domain.InitiativeUnknown {
		t.Fatalf("preserved initiative state = %q, want unknown", result.Initiative.State)
	}
	for _, member := range mutation.Members {
		task, taskErr := store.GetTask(ctx, member.ExternalRunRef)
		if taskErr != nil || task.State != domain.TaskPrepared || task.ManagedRunID != "" {
			t.Fatalf("preserved task = %#v, %v", task, taskErr)
		}
	}
}

func TestInitiativeAbandonRollsBackEveryMemberOnWriteFailure(t *testing.T) {
	ctx := context.Background()
	store, mutation := preparedInitiativeAbandonStore(t, application.AbandonDispositionReapSafe)
	if _, err := store.db.ExecContext(ctx, `CREATE TRIGGER refuse_group_member_abandon
		BEFORE UPDATE ON tasks WHEN NEW.handle = 'task-integration'
		BEGIN SELECT RAISE(ABORT, 'injected group abandon failure'); END`); err != nil {
		t.Fatalf("install group abandon failure trigger: %v", err)
	}
	if _, err := store.CommitInitiativeAbandonment(ctx, mutation); err == nil {
		t.Fatal("CommitInitiativeAbandonment(injected failure) error = nil")
	}
	for _, member := range mutation.Members {
		task, err := store.GetTask(ctx, member.ExternalRunRef)
		if err != nil || task.State != domain.TaskPrepared {
			t.Fatalf("task after rollback = %#v, %v", task, err)
		}
	}
	if _, err := store.GetOperation(ctx, mutation.OperationID); !errors.Is(err, application.ErrNotFound) {
		t.Fatalf("abandon operation after rollback error = %v, want ErrNotFound", err)
	}
}

func preparedInitiativeAbandonStore(
	t *testing.T,
	disposition application.AbandonDisposition,
) (*Store, application.ManagedRunGroupAbandonmentMutation) {
	t.Helper()
	ctx := context.Background()
	store, err := Open(ctx, filepath.Join(canonicalTempDir(t), "devcrew.db"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	prepared := sqlitePreparedInitiativeMutation()
	recordInitiativeMemberIntents(t, store, prepared)
	if _, err := store.CommitPreparedInitiative(ctx, prepared); err != nil {
		t.Fatalf("CommitPreparedInitiative() error = %v", err)
	}
	members := make([]application.ManagedRunGroupAbandonmentMember, 0, len(prepared.Members))
	for _, member := range prepared.Members {
		members = append(members, application.ManagedRunGroupAbandonmentMember{
			ManagedRunID: "managed-run-" + member.Task.Handle, ExternalRunRef: member.Task.Handle,
			RegistrationNonce: member.Preparation.RegistrationNonce,
		})
	}
	return store, application.ManagedRunGroupAbandonmentMutation{
		ServiceInstanceID: prepared.Members[0].Task.ServiceInstanceID,
		ManagedRunGroupID: "managed-run-group-0001", RegistrationNonce: prepared.GroupRegistrationNonce,
		Members: members, Reason: application.AbandonReasonActivationRejected, Disposition: disposition,
		OperationID: "abandon-initiative-0001", SubjectDigest: strings.Repeat("e", 64),
		At: prepared.At.Add(time.Minute),
	}
}
