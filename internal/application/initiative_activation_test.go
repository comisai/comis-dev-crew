package application

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/comisai/comis-dev-crew/internal/domain"
)

func TestInitiativeActivationPublishesPerMemberAttachmentOutcomes(t *testing.T) {
	store := &initiativeActivationStore{}
	attachments := &initiativeActivationAttachments{store: store, failAt: 2}
	coordinator, err := NewInitiativeActivations(InitiativeActivationConfig{
		Store: store, RuntimeAttachments: attachments, Acknowledger: initiativeActivationAcknowledger{},
		Clock: func() time.Time { return time.Date(2026, time.August, 20, 17, 0, 0, 0, time.UTC) },
	})
	if err != nil {
		t.Fatalf("NewInitiativeActivations() error = %v", err)
	}

	result, err := coordinator.ActivateManagedRunGroup(context.Background(), validInitiativeActivationCommand())
	if err != nil {
		t.Fatalf("ActivateManagedRunGroup() error = %v", err)
	}
	if store.commitCalls != 1 || len(store.committed.Members) != 3 {
		t.Fatalf("activation commit = %#v after %d calls, want the complete group once", store.committed, store.commitCalls)
	}
	if attachments.boundBeforeCommit || len(attachments.requests) != 3 {
		t.Fatalf("attachment bindings = %#v, boundBeforeCommit=%t", attachments.requests, attachments.boundBeforeCommit)
	}
	want := []InitiativeActivationOutcome{InitiativeActivationCompleted, InitiativeActivationUnknown, InitiativeActivationCompleted}
	if len(result.Members) != len(want) {
		t.Fatalf("member outcomes = %#v, want %d", result.Members, len(want))
	}
	for index, outcome := range want {
		if result.Members[index].Outcome != outcome {
			t.Fatalf("member %d outcome = %q, want %q", index, result.Members[index].Outcome, outcome)
		}
	}
	if result.Initiative.State != domain.InitiativeUnknown || len(store.stateChanges) != 0 {
		t.Fatalf("partial activation initiative = %#v, state changes %#v", result.Initiative, store.stateChanges)
	}
}

func TestInitiativeActivationBecomesActiveOnlyAfterEveryAttachmentBinds(t *testing.T) {
	store := &initiativeActivationStore{}
	attachments := &initiativeActivationAttachments{store: store}
	coordinator, err := NewInitiativeActivations(InitiativeActivationConfig{
		Store: store, RuntimeAttachments: attachments, Acknowledger: initiativeActivationAcknowledger{},
		Clock: func() time.Time { return time.Date(2026, time.August, 20, 17, 0, 0, 0, time.UTC) },
	})
	if err != nil {
		t.Fatalf("NewInitiativeActivations() error = %v", err)
	}

	result, err := coordinator.ActivateManagedRunGroup(context.Background(), validInitiativeActivationCommand())
	if err != nil {
		t.Fatalf("ActivateManagedRunGroup() error = %v", err)
	}
	if result.Initiative.State != domain.InitiativeActive {
		t.Fatalf("initiative state = %q, want active", result.Initiative.State)
	}
	for _, member := range result.Members {
		if member.Outcome != InitiativeActivationCompleted {
			t.Fatalf("member outcome = %q, want completed", member.Outcome)
		}
	}
	if len(store.stateChanges) != 1 || store.stateChanges[0] != domain.InitiativeActive {
		t.Fatalf("successful activation state publications = %#v, want active", store.stateChanges)
	}
}

func TestInitiativeActivationFailsClosedAcrossStoreBoundaries(t *testing.T) {
	newCoordinator := func(store *initiativeActivationStore, attachments *initiativeActivationAttachments) *InitiativeActivations {
		t.Helper()
		coordinator, err := NewInitiativeActivations(InitiativeActivationConfig{
			Store: store, RuntimeAttachments: attachments, Acknowledger: initiativeActivationAcknowledger{},
			Clock: func() time.Time { return time.Date(2026, time.August, 20, 17, 0, 0, 0, time.UTC) },
		})
		if err != nil {
			t.Fatalf("NewInitiativeActivations() error = %v", err)
		}
		return coordinator
	}
	for _, test := range []struct {
		name  string
		store *initiativeActivationStore
		fail  int
	}{
		{name: "replay read fails", store: &initiativeActivationStore{replayErr: errors.New("read failed")}},
		{name: "atomic commit fails", store: &initiativeActivationStore{commitErr: errors.New("commit failed")}},
		{name: "committed result is incomplete", store: &initiativeActivationStore{
			replayFound: true, replay: InitiativeActivationResult{Initiative: domain.DevelopmentInitiative{State: domain.InitiativeActive}},
		}},
		{name: "active posture write fails", store: &initiativeActivationStore{stateErr: errors.New("posture failed")}},
	} {
		t.Run(test.name, func(t *testing.T) {
			attachments := &initiativeActivationAttachments{store: test.store, failAt: test.fail}
			if _, err := newCoordinator(test.store, attachments).ActivateManagedRunGroup(
				context.Background(), validInitiativeActivationCommand(),
			); err == nil {
				t.Fatal("ActivateManagedRunGroup() error = nil")
			}
		})
	}
	if _, err := NewInitiativeActivations(InitiativeActivationConfig{}); err == nil {
		t.Fatal("NewInitiativeActivations(empty) error = nil")
	}
}

func validInitiativeActivationCommand() ActivateManagedRunGroupCommand {
	members := make([]ActivateManagedRunGroupMember, 0, 3)
	for index, handle := range []string{"task-backend", "task-frontend", "task-integration"} {
		members = append(members, ActivateManagedRunGroupMember{
			ManagedRunID: "managed-run-" + handle, ExternalRunRef: handle,
			RegistrationNonce:     "registration-nonce_" + handle,
			WorkspaceLeaseID:      "workspace-lease-" + handle,
			ExecutionAttachmentID: "execution-attachment-" + handle,
			AttachmentTargetName:  fmt.Sprintf("attachment-%032x.sock", index+1),
		})
	}
	return ActivateManagedRunGroupCommand{
		OperationID: "activate-initiative-0001", ServiceInstanceID: "service-instance-0001",
		ManagedRunGroupID: "managed-run-group-0001", RegistrationNonce: "registration-nonce_group",
		Members: members,
	}
}

type initiativeActivationStore struct {
	replay       InitiativeActivationResult
	replayFound  bool
	replayErr    error
	committed    ManagedRunGroupActivationMutation
	commitCalls  int
	commitErr    error
	stateChanges []domain.InitiativeState
	stateErr     error
	currentState domain.InitiativeState
}

func (store *initiativeActivationStore) ReplayInitiativeActivation(
	context.Context, string, string,
) (InitiativeActivationResult, bool, error) {
	return store.replay, store.replayFound, store.replayErr
}

func (store *initiativeActivationStore) CommitInitiativeActivation(
	_ context.Context,
	mutation ManagedRunGroupActivationMutation,
) (InitiativeActivationResult, error) {
	store.commitCalls++
	store.committed = mutation
	if store.commitErr != nil {
		return InitiativeActivationResult{}, store.commitErr
	}
	initiative := domain.DevelopmentInitiative{
		Handle: "initiative-prepared-0001", ManagedRunGroupID: mutation.ManagedRunGroupID,
		State: domain.InitiativeUnknown,
	}
	store.currentState = initiative.State
	tasks := make([]domain.Task, 0, len(mutation.Members))
	for _, member := range mutation.Members {
		tasks = append(tasks, domain.Task{
			Handle: member.ExternalRunRef, ManagedRunID: member.Binding.ManagedRunID,
			WorkspaceLeaseID:      member.Binding.WorkspaceLeaseID,
			ExecutionAttachmentID: member.ExecutionAttachmentID,
			AttachmentTargetName:  member.AttachmentTargetName, State: domain.TaskReady,
		})
	}
	return InitiativeActivationResult{
		Initiative: initiative, Tasks: tasks,
		Operation: domain.OperationRecord{ID: mutation.OperationID, UpdatedAt: mutation.At},
	}, nil
}

func (store *initiativeActivationStore) SetInitiativeActivationState(
	_ context.Context,
	managedRunGroupID string,
	state domain.InitiativeState,
	_ time.Time,
) (domain.DevelopmentInitiative, error) {
	store.stateChanges = append(store.stateChanges, state)
	if store.stateErr != nil {
		return domain.DevelopmentInitiative{}, store.stateErr
	}
	store.currentState = state
	return domain.DevelopmentInitiative{Handle: "initiative-prepared-0001", ManagedRunGroupID: managedRunGroupID, State: state}, nil
}

type initiativeActivationAttachments struct {
	store             *initiativeActivationStore
	requests          []RuntimeAttachmentBindingRequest
	failAt            int
	boundBeforeCommit bool
}

func (*initiativeActivationAttachments) PrepareRuntimeAttachment(
	context.Context, RuntimeAttachmentPreparationRequest,
) (PreparedRuntimeAttachment, error) {
	return PreparedRuntimeAttachment{}, errors.New("preparation is outside activation")
}

func (attachments *initiativeActivationAttachments) BindRuntimeAttachment(
	_ context.Context,
	request RuntimeAttachmentBindingRequest,
) error {
	if attachments.store.commitCalls == 0 {
		attachments.boundBeforeCommit = true
	}
	if attachments.store.currentState != domain.InitiativeUnknown {
		return errors.New("initiative is launchable before attachment binding completes")
	}
	attachments.requests = append(attachments.requests, request)
	if attachments.failAt != 0 && len(attachments.requests) == attachments.failAt {
		return errors.New("attachment unavailable")
	}
	return nil
}

func (*initiativeActivationAttachments) ReleaseRuntimeAttachment(context.Context, string) error {
	return nil
}

type initiativeActivationAcknowledger struct{}

func (initiativeActivationAcknowledger) AcknowledgeWorkerLaunch(
	context.Context, AcknowledgeWorkerLaunchCommand,
) (MutationResult, error) {
	return MutationResult{}, nil
}
