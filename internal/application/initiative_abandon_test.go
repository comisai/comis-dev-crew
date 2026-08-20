package application

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/comisai/comis-dev-crew/internal/domain"
)

func TestInitiativeAbandonCommitsTheExactPreparedMemberSet(t *testing.T) {
	store := &initiativeAbandonStore{}
	clock := time.Date(2026, time.August, 20, 18, 0, 0, 0, time.UTC)
	coordinator, err := NewInitiativeAbandonments(InitiativeAbandonmentConfig{
		Store: store, Clock: func() time.Time { return clock },
	})
	if err != nil {
		t.Fatalf("NewInitiativeAbandonments() error = %v", err)
	}
	command := validInitiativeAbandonCommand()
	result, err := coordinator.AbandonManagedRunGroup(context.Background(), command)
	if err != nil {
		t.Fatalf("AbandonManagedRunGroup() error = %v", err)
	}
	if store.commitCalls != 1 || store.committed.ManagedRunGroupID != command.ManagedRunGroupID ||
		len(store.committed.SubjectDigest) != 64 || !store.committed.At.Equal(clock) {
		t.Fatalf("committed abandonment = %#v after %d calls", store.committed, store.commitCalls)
	}
	if result.Operation.ID != command.OperationID || result.Disposition != command.Disposition || len(result.Members) != 2 {
		t.Fatalf("AbandonManagedRunGroup() = %#v", result)
	}
}

func TestInitiativeAbandonReplayDoesNotRepeatTheMutation(t *testing.T) {
	command := validInitiativeAbandonCommand()
	replay := InitiativeAbandonmentResult{
		Initiative: domain.DevelopmentInitiative{Handle: "initiative-abandon", ManagedRunGroupID: command.ManagedRunGroupID},
		Operation:  domain.OperationRecord{ID: command.OperationID}, Disposition: command.Disposition,
		Members: []InitiativeActivationMemberResult{{ManagedRunID: command.Members[0].ManagedRunID, Outcome: InitiativeActivationCompleted}},
	}
	store := &initiativeAbandonStore{replay: replay, replayFound: true}
	coordinator, err := NewInitiativeAbandonments(InitiativeAbandonmentConfig{
		Store: store, Clock: func() time.Time { return time.Date(2026, time.August, 20, 18, 0, 0, 0, time.UTC) },
	})
	if err != nil {
		t.Fatalf("NewInitiativeAbandonments() error = %v", err)
	}
	result, err := coordinator.AbandonManagedRunGroup(context.Background(), command)
	if err != nil || store.commitCalls != 0 || result.Operation.ID != replay.Operation.ID {
		t.Fatalf("AbandonManagedRunGroup(replay) = %#v, %v after %d commits", result, err, store.commitCalls)
	}
}

func TestInitiativeAbandonFailsClosedAcrossStoreBoundaries(t *testing.T) {
	for _, test := range []struct {
		name  string
		store *initiativeAbandonStore
	}{
		{name: "replay read fails", store: &initiativeAbandonStore{replayErr: errors.New("read failed")}},
		{name: "atomic commit fails", store: &initiativeAbandonStore{commitErr: errors.New("commit failed")}},
	} {
		t.Run(test.name, func(t *testing.T) {
			coordinator, err := NewInitiativeAbandonments(InitiativeAbandonmentConfig{
				Store: test.store,
				Clock: func() time.Time { return time.Date(2026, time.August, 20, 18, 0, 0, 0, time.UTC) },
			})
			if err != nil {
				t.Fatalf("NewInitiativeAbandonments() error = %v", err)
			}
			if _, err := coordinator.AbandonManagedRunGroup(context.Background(), validInitiativeAbandonCommand()); err == nil {
				t.Fatal("AbandonManagedRunGroup() error = nil")
			}
		})
	}
	if _, err := NewInitiativeAbandonments(InitiativeAbandonmentConfig{}); err == nil {
		t.Fatal("NewInitiativeAbandonments(empty) error = nil")
	}
}

func validInitiativeAbandonCommand() AbandonManagedRunGroupCommand {
	return AbandonManagedRunGroupCommand{
		OperationID: "abandon-initiative-0001", ServiceInstanceID: "service-instance-0001",
		ManagedRunGroupID: "managed-run-group-0001", RegistrationNonce: "registration-nonce_group",
		Members: []AbandonManagedRunGroupMember{
			{ManagedRunID: "managed-run-backend", ExternalRunRef: "task-backend", RegistrationNonce: "registration-nonce_backend"},
			{ManagedRunID: "managed-run-frontend", ExternalRunRef: "task-frontend", RegistrationNonce: "registration-nonce_frontend"},
		},
		Reason: AbandonReasonActivationRejected, Disposition: AbandonDispositionReapSafe,
	}
}

type initiativeAbandonStore struct {
	replay      InitiativeAbandonmentResult
	replayFound bool
	replayErr   error
	committed   ManagedRunGroupAbandonmentMutation
	commitCalls int
	commitErr   error
}

func (store *initiativeAbandonStore) ReplayInitiativeAbandonment(
	context.Context, string, string,
) (InitiativeAbandonmentResult, bool, error) {
	return store.replay, store.replayFound, store.replayErr
}

func (store *initiativeAbandonStore) CommitInitiativeAbandonment(
	_ context.Context,
	mutation ManagedRunGroupAbandonmentMutation,
) (InitiativeAbandonmentResult, error) {
	store.commitCalls++
	store.committed = mutation
	if store.commitErr != nil {
		return InitiativeAbandonmentResult{}, store.commitErr
	}
	members := make([]InitiativeActivationMemberResult, 0, len(mutation.Members))
	for _, member := range mutation.Members {
		members = append(members, InitiativeActivationMemberResult{
			ManagedRunID: member.ManagedRunID, Outcome: InitiativeActivationCompleted,
		})
	}
	return InitiativeAbandonmentResult{
		Initiative: domain.DevelopmentInitiative{
			Handle: "initiative-abandon", ManagedRunGroupID: mutation.ManagedRunGroupID,
			State: domain.InitiativeCancelled,
		},
		Operation: domain.OperationRecord{ID: mutation.OperationID, UpdatedAt: mutation.At},
		Members:   members, Disposition: mutation.Disposition,
	}, nil
}
