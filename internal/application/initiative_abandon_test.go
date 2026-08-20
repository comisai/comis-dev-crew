package application

import (
	"context"
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
	committed   ManagedRunGroupAbandonmentMutation
	commitCalls int
}

func (store *initiativeAbandonStore) ReplayInitiativeAbandonment(
	context.Context, string, string,
) (InitiativeAbandonmentResult, bool, error) {
	return store.replay, store.replayFound, nil
}

func (store *initiativeAbandonStore) CommitInitiativeAbandonment(
	_ context.Context,
	mutation ManagedRunGroupAbandonmentMutation,
) (InitiativeAbandonmentResult, error) {
	store.commitCalls++
	store.committed = mutation
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
