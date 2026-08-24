package application

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/comisai/comis-dev-crew/internal/domain"
)

func TestPrepareInitiativeCommitsEveryPreparedMemberWithoutLaunching(t *testing.T) {
	store := &initiativeMutationStore{}
	workspaces := &initiativeWorkspacePreparer{}
	attachments := &initiativeAttachmentPreparer{}
	coordinator := newInitiativeMutationsForTest(t, store, workspaces, attachments)

	result, err := coordinator.PrepareInitiative(context.Background(), validPrepareInitiativeCommand())
	if err != nil {
		t.Fatalf("PrepareInitiative() error = %v", err)
	}
	if store.commitCalls != 1 || len(store.committed.Members) != 3 {
		t.Fatalf("committed mutation = %#v after %d calls, want three members once", store.committed, store.commitCalls)
	}
	if len(workspaces.requests) != 3 || len(attachments.requests) != 3 {
		t.Fatalf("preparation calls = workspaces %d attachments %d, want three each", len(workspaces.requests), len(attachments.requests))
	}
	if result.Initiative.ManagedRunGroupID != "" || result.Initiative.State != domain.InitiativePreparing {
		t.Fatalf("prepared initiative host binding = %#v, want unbound preparing", result.Initiative)
	}
	if result.Preparation.ExternalGroupRef != result.Initiative.Handle ||
		result.Preparation.RegistrationNonce == "" || len(result.Preparation.Members) != 3 {
		t.Fatalf("group preparation = %#v, want exact initiative and three members", result.Preparation)
	}
	for index, member := range store.committed.Members {
		if member.Task.State != domain.TaskPrepared || member.Task.ManagedRunID != "" ||
			member.Preparation.ExternalRunRef != member.Task.Handle ||
			member.Preparation.RequestedWorkspaceRoot == "" {
			t.Fatalf("member %d = %#v, want an unbound prepared task", index, member)
		}
	}
	if len(store.intents) != 3 {
		t.Fatalf("durable preparation intents = %d, want three", len(store.intents))
	}
}

func TestPrepareInitiativeRejectsTheWholeGraphBeforeWorkspaceSideEffects(t *testing.T) {
	store := &initiativeMutationStore{}
	workspaces := &initiativeWorkspacePreparer{}
	attachments := &initiativeAttachmentPreparer{}
	coordinator := newInitiativeMutationsForTest(t, store, workspaces, attachments)
	command := validPrepareInitiativeCommand()
	command.Edges = append(command.Edges, PrepareInitiativeEdge{
		FromTaskRef: "integration-ref", ToTaskRef: "backend-ref", Kind: domain.EdgeBlocksStart,
	})

	if _, err := coordinator.PrepareInitiative(context.Background(), command); err == nil {
		t.Fatal("PrepareInitiative(cycle) error = nil")
	}
	if len(store.intents) != 0 || store.commitCalls != 0 || len(workspaces.requests) != 0 || len(attachments.requests) != 0 {
		t.Fatalf("invalid graph produced side effects: intents=%d commits=%d workspaces=%d attachments=%d",
			len(store.intents), store.commitCalls, len(workspaces.requests), len(attachments.requests))
	}
}

func TestPrepareInitiativePersistsOnlyExactProducerOwnedContractArtifacts(t *testing.T) {
	store := &initiativeMutationStore{}
	workspaces := &initiativeWorkspacePreparer{}
	attachments := &initiativeAttachmentPreparer{}
	coordinator := newInitiativeMutationsForTest(t, store, workspaces, attachments)
	command := validPrepareInitiativeCommand()
	content := `{"openapi":"3.1.0"}`
	digest := fmt.Sprintf("%x", sha256.Sum256([]byte(content)))
	command.ContractArtifacts = []PrepareInitiativeContractArtifact{{
		ArtifactHandle: "artifact-api-v1", ProducerTaskRef: "backend-ref",
		Kind: domain.ArtifactAPISchema, MediaType: "application/json", Content: content,
	}}
	command.Components[1].Tasks[0].Contract.ConsumedContracts = []domain.PinnedContract{{
		ArtifactHandle: "artifact-api-v1", Kind: domain.ArtifactAPISchema, ContentHash: digest,
	}}
	command.Edges = append(command.Edges, PrepareInitiativeEdge{
		FromTaskRef: "backend-ref", ToTaskRef: "frontend-ref",
		Kind: domain.EdgeConsumesArtifact, RequiredArtifactKind: domain.ArtifactAPISchema,
	})

	if _, err := coordinator.PrepareInitiative(context.Background(), command); err != nil {
		t.Fatalf("PrepareInitiative() error = %v", err)
	}
	if len(store.committed.ContractArtifacts) != 1 {
		t.Fatalf("contract artifacts = %#v, want one", store.committed.ContractArtifacts)
	}
	artifact := store.committed.ContractArtifacts[0]
	if artifact.Artifact.ProducerTaskHandle != "task-backend" ||
		artifact.Artifact.ContentHash != digest || string(artifact.Content) != content {
		t.Fatalf("durable contract artifact = %#v", artifact)
	}

	secondStore := &initiativeMutationStore{}
	command.OperationID = "prepare-initiative-0002"
	command.Components[1].Tasks[0].Contract.ConsumedContracts[0].ContentHash = strings.Repeat("f", 64)
	if _, err := newInitiativeMutationsForTest(
		t, secondStore, &initiativeWorkspacePreparer{}, &initiativeAttachmentPreparer{},
	).PrepareInitiative(context.Background(), command); err == nil {
		t.Fatal("PrepareInitiative(counterfeit contract digest) error = nil")
	}
	if len(secondStore.intents) != 0 || secondStore.commitCalls != 0 {
		t.Fatal("counterfeit contract reached durable preparation side effects")
	}
}

func TestPrepareInitiativePreservesReversibleArtifactsAfterPartialPreparationFailure(t *testing.T) {
	store := &initiativeMutationStore{}
	workspaces := &initiativeWorkspacePreparer{failAt: 2}
	attachments := &initiativeAttachmentPreparer{}
	coordinator := newInitiativeMutationsForTest(t, store, workspaces, attachments)

	if _, err := coordinator.PrepareInitiative(context.Background(), validPrepareInitiativeCommand()); err == nil {
		t.Fatal("PrepareInitiative(partial workspace failure) error = nil")
	}
	if store.commitCalls != 0 {
		t.Fatalf("atomic initiative commit calls = %d, want zero", store.commitCalls)
	}
	if len(store.intents) != 3 {
		t.Fatalf("durable preparation intents = %d, want all three before allocation", len(store.intents))
	}
	if len(workspaces.requests) != 2 || len(attachments.requests) != 1 {
		t.Fatalf("partial artifacts = workspaces %d attachments %d, want 2 and 1", len(workspaces.requests), len(attachments.requests))
	}
}

func TestPrepareInitiativeReplayReturnsTheDurableGroupWithoutRepeatingAllocation(t *testing.T) {
	replay := InitiativePreparationResult{
		Initiative:  domain.DevelopmentInitiative{Handle: "initiative-replayed"},
		Preparation: ManagedRunGroupPreparation{ExternalGroupRef: "initiative-replayed"},
	}
	store := &initiativeMutationStore{replay: replay, replayFound: true}
	workspaces := &initiativeWorkspacePreparer{}
	attachments := &initiativeAttachmentPreparer{}
	coordinator := newInitiativeMutationsForTest(t, store, workspaces, attachments)

	result, err := coordinator.PrepareInitiative(context.Background(), validPrepareInitiativeCommand())
	if err != nil || result.Initiative.Handle != replay.Initiative.Handle {
		t.Fatalf("PrepareInitiative(replay) = %#v, %v", result, err)
	}
	if len(store.intents) != 0 || store.commitCalls != 0 || len(workspaces.requests) != 0 || len(attachments.requests) != 0 {
		t.Fatal("identical replay repeated initiative preparation side effects")
	}
}

func newInitiativeMutationsForTest(
	t *testing.T,
	store *initiativeMutationStore,
	workspaces *initiativeWorkspacePreparer,
	attachments *initiativeAttachmentPreparer,
) *InitiativeMutations {
	t.Helper()
	taskHandles := []string{"task-backend", "task-frontend", "task-integration"}
	nonceIndex := 0
	coordinator, err := NewInitiativeMutations(InitiativeMutationConfig{
		Store:              store,
		Repositories:       &repositoryCatalog{},
		WorkerProfiles:     acceptingWorkerProfile,
		ValidationProfiles: acceptingValidationProfile,
		Workspaces:         workspaces,
		RuntimeAttachments: attachments,
		TaskIDs: func(string) (string, error) {
			if len(taskHandles) == 0 {
				return "", errors.New("task identities exhausted")
			}
			handle := taskHandles[0]
			taskHandles = taskHandles[1:]
			return handle, nil
		},
		RegistrationNonces: func() (string, error) {
			nonceIndex++
			return "registration-nonce_" + strings.Repeat("a", nonceIndex), nil
		},
		PreparationTTL: time.Hour,
		Clock: func() time.Time {
			return time.Date(2026, time.August, 20, 14, 0, 0, 0, time.UTC)
		},
	})
	if err != nil {
		t.Fatalf("NewInitiativeMutations() error = %v", err)
	}
	return coordinator
}

func validPrepareInitiativeCommand() PrepareInitiativeCommand {
	return PrepareInitiativeCommand{
		OperationID:       "prepare-initiative-0001",
		ServiceInstanceID: "service-instance-0001",
		TitleRef:          "title-ref-0001",
		BaseRevisionSet: []domain.InitiativeBaseRevision{
			{RepositoryID: "product-api", Revision: strings.Repeat("a", 40)},
		},
		Components: []PrepareInitiativeComponent{
			{
				ComponentHandle: "component-backend", RepositoryID: "product-api",
				ResponsibilityRef: "responsibility-backend",
				Tasks:             []PrepareInitiativeTask{{TaskRef: "backend-ref", Contract: initiativeTaskContract()}},
			},
			{
				ComponentHandle: "component-frontend", RepositoryID: "product-api",
				ResponsibilityRef: "responsibility-frontend",
				Tasks:             []PrepareInitiativeTask{{TaskRef: "frontend-ref", Contract: initiativeTaskContract()}},
			},
			{
				ComponentHandle: "component-integration", RepositoryID: "product-api",
				ResponsibilityRef: "responsibility-integration",
				Tasks:             []PrepareInitiativeTask{{TaskRef: "integration-ref", Contract: initiativeTaskContract()}},
			},
		},
		Edges: []PrepareInitiativeEdge{
			{FromTaskRef: "backend-ref", ToTaskRef: "integration-ref", Kind: domain.EdgeIntegratesAfter},
			{FromTaskRef: "frontend-ref", ToTaskRef: "integration-ref", Kind: domain.EdgeIntegratesAfter},
		},
		IntegrationPolicyID:  "integration-default",
		IntegrationOwnerTask: "integration-ref",
	}
}

func initiativeTaskContract() PrepareInitiativeTaskContract {
	return PrepareInitiativeTaskContract{
		Shape:              domain.ShapeShip,
		AcceptanceCriteria: []string{"The component outcome is proven."},
		Constraints:        []string{"Preserve unrelated changes."},
		ValidationProfile:  "go-default",
		DeliveryMode:       domain.DeliveryPullRequest,
		WorkerProfileID:    "fixture-worker",
	}
}

type initiativeMutationStore struct {
	replay      InitiativePreparationResult
	replayFound bool
	intents     []TaskPreparationIntent
	committed   PreparedInitiativeMutation
	commitCalls int
}

func (store *initiativeMutationStore) ReplayInitiativePreparation(
	context.Context,
	string,
	string,
) (InitiativePreparationResult, bool, error) {
	return store.replay, store.replayFound, nil
}

func (store *initiativeMutationStore) RecordTaskPreparationIntent(
	_ context.Context,
	intent TaskPreparationIntent,
) (TaskPreparationIntent, error) {
	store.intents = append(store.intents, intent)
	return intent, nil
}

func (store *initiativeMutationStore) CommitPreparedInitiative(
	_ context.Context,
	mutation PreparedInitiativeMutation,
) (InitiativePreparationResult, error) {
	store.commitCalls++
	store.committed = mutation
	preparations := make([]ManagedRunPreparation, 0, len(mutation.Members))
	tasks := make([]domain.Task, 0, len(mutation.Members))
	for _, member := range mutation.Members {
		preparations = append(preparations, member.Preparation)
		tasks = append(tasks, member.Task)
	}
	return InitiativePreparationResult{
		Initiative: mutation.Initiative,
		Tasks:      tasks,
		Preparation: ManagedRunGroupPreparation{
			ExternalGroupRef:  mutation.Initiative.Handle,
			RegistrationNonce: mutation.GroupRegistrationNonce,
			Members:           preparations,
			ExpiresAt:         mutation.At.Add(time.Hour),
		},
		Operation: domain.OperationRecord{ID: mutation.OperationID},
	}, nil
}

type initiativeWorkspacePreparer struct {
	requests []WorkspacePreparationRequest
	failAt   int
}

func (preparer *initiativeWorkspacePreparer) PrepareWorkspace(
	_ context.Context,
	request WorkspacePreparationRequest,
) (PreparedWorkspace, error) {
	preparer.requests = append(preparer.requests, request)
	if preparer.failAt != 0 && len(preparer.requests) == preparer.failAt {
		return PreparedWorkspace{}, errors.New("workspace unavailable")
	}
	return PreparedWorkspace{CanonicalRoot: "/approved/workspaces/" + request.TaskHandle}, nil
}

type initiativeAttachmentPreparer struct {
	requests []RuntimeAttachmentPreparationRequest
}

func (preparer *initiativeAttachmentPreparer) PrepareRuntimeAttachment(
	_ context.Context,
	request RuntimeAttachmentPreparationRequest,
) (PreparedRuntimeAttachment, error) {
	preparer.requests = append(preparer.requests, request)
	return PreparedRuntimeAttachment{
		Kind:          RuntimeAttachmentUnixSocket,
		SourcePath:    "/approved/runtime/" + request.TaskHandle + "/attachment.sock",
		RelayIdentity: strings.Repeat("ab", 32),
	}, nil
}

func (*initiativeAttachmentPreparer) BindRuntimeAttachment(context.Context, RuntimeAttachmentBindingRequest) error {
	return nil
}

func (*initiativeAttachmentPreparer) ReleaseRuntimeAttachment(context.Context, string) error {
	return nil
}
