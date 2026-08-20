package application

import (
	"context"
	"errors"
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
	if result.Initiative.State != domain.InitiativeUnknown || store.stateChanges[len(store.stateChanges)-1] != domain.InitiativeUnknown {
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
	if len(store.stateChanges) != 0 {
		t.Fatalf("successful activation state repairs = %#v, want none", store.stateChanges)
	}
}

func validInitiativeActivationCommand() ActivateManagedRunGroupCommand {
	members := make([]ActivateManagedRunGroupMember, 0, 3)
	for index, handle := range []string{"task-backend", "task-frontend", "task-integration"} {
		members = append(members, ActivateManagedRunGroupMember{
			ManagedRunID: "managed-run-" + handle, ExternalRunRef: handle,
			RegistrationNonce: "registration-nonce_" + handle,
			WorkspaceLeaseID:  "workspace-lease-" + handle,
			ExecutionAttachmentID: "execution-attachment-" + handle,
			AttachmentTargetName:  "attachment-0000000000000000000000000000000" + string(rune('a'+index)) + ".sock",
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
	committed    ManagedRunGroupActivationMutation
	commitCalls  int
	stateChanges []domain.InitiativeState
}

func (store *initiativeActivationStore) ReplayInitiativeActivation(
	context.Context, string, string,
) (InitiativeActivationResult, bool, error) {
	return store.replay, store.replayFound, nil
}

func (store *initiativeActivationStore) CommitInitiativeActivation(
	_ context.Context,
	mutation ManagedRunGroupActivationMutation,
) (InitiativeActivationResult, error) {
	store.commitCalls++
	store.committed = mutation
	initiative := domain.DevelopmentInitiative{
		Handle: mutation.ExternalGroupRef, ManagedRunGroupID: mutation.ManagedRunGroupID,
		State: domain.InitiativeActive,
	}
	tasks := make([]domain.Task, 0, len(mutation.Members))
	for _, member := range mutation.Members {
		tasks = append(tasks, domain.Task{
			Handle: member.ExternalRunRef, ManagedRunID: member.Binding.ManagedRunID,
			WorkspaceLeaseID: member.Binding.WorkspaceLeaseID,
			ExecutionAttachmentID: member.ExecutionAttachmentID,
			AttachmentTargetName: member.AttachmentTargetName, State: domain.TaskReady,
		})
	}
	return InitiativeActivationResult{
		Initiative: initiative, Tasks: tasks,
		Operation: domain.OperationRecord{ID: mutation.OperationID, UpdatedAt: mutation.At},
	}, nil
}

func (store *initiativeActivationStore) SetInitiativeActivationState(
	_ context.Context,
	handle string,
	managedRunGroupID string,
	state domain.InitiativeState,
	_ time.Time,
) (domain.DevelopmentInitiative, error) {
	store.stateChanges = append(store.stateChanges, state)
	return domain.DevelopmentInitiative{Handle: handle, ManagedRunGroupID: managedRunGroupID, State: state}, nil
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
	attachments.requests = append(attachments.requests, request)
	if attachments.failAt != 0 && len(attachments.requests) == attachments.failAt {
		return errors.New("attachment unavailable")
	}
	return nil
}

func (*initiativeActivationAttachments) ReleaseRuntimeAttachment(context.Context, string) error { return nil }

type initiativeActivationAcknowledger struct{}

func (initiativeActivationAcknowledger) AcknowledgeWorkerLaunch(
	context.Context, AcknowledgeWorkerLaunchCommand,
) (MutationResult, error) {
	return MutationResult{}, nil
}
