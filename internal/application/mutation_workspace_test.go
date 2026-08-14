package application

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/comisai/comis-dev-crew/internal/domain"
)

func TestMutations_PrepareAllocatesOperationBoundWorkspaceBeforeDurableComisPreparation(t *testing.T) {
	store := &mutationStore{}
	workspaces := &workspacePreparer{prepared: PreparedWorkspace{CanonicalRoot: "/approved/worktrees/task-stable-0001"}}
	attachments := &runtimeAttachmentCoordinator{prepared: PreparedRuntimeAttachment{
		Kind: RuntimeAttachmentUnixSocket, SourcePath: "/approved/runtime/task-stable-0001/attachment.sock",
	}}
	var taskOperation string
	mutations, err := NewMutations(MutationConfig{
		Store: store, Repositories: &repositoryCatalog{}, Workspaces: workspaces, RuntimeAttachments: attachments,
		TaskIDs: func(operationID string) (string, error) {
			taskOperation = operationID
			return "task-stable-0001", nil
		},
		RegistrationNonces: testRegistrationNonceSource, PreparationTTL: time.Hour,
		Clock: func() time.Time { return time.Date(2026, time.August, 10, 9, 0, 0, 0, time.UTC) },
	})
	if err != nil {
		t.Fatal(err)
	}
	command := validPrepareCommand()
	if _, err := mutations.PrepareTask(context.Background(), command); err != nil {
		t.Fatalf("PrepareTask() error = %v", err)
	}
	if taskOperation != command.OperationID {
		t.Fatalf("task identity operation = %q, want %q", taskOperation, command.OperationID)
	}
	wantRequest := WorkspacePreparationRequest{
		OperationID: command.OperationID, TaskHandle: "task-stable-0001",
		RepositoryID: command.RepositoryID, BaseRevision: command.BaseRevision,
	}
	if workspaces.calls != 1 || workspaces.request != wantRequest {
		t.Fatalf("workspace preparation = %d / %#v, want %#v", workspaces.calls, workspaces.request, wantRequest)
	}
	if got := store.prepared.Preparation.RequestedWorkspaceRoot; got != workspaces.prepared.CanonicalRoot {
		t.Fatalf("requested workspace root = %q, want %q", got, workspaces.prepared.CanonicalRoot)
	}
	wantAttachmentRequest := RuntimeAttachmentPreparationRequest{
		OperationID: command.OperationID, TaskHandle: "task-stable-0001",
		BriefRevision: store.prepared.Task.BriefRevision, BriefRevisionHash: store.prepared.Task.BriefRevisionHash,
		Brief: mustRenderWorkerBriefForTest(t, store.prepared.Task), WorkingDirectory: workspaces.prepared.CanonicalRoot,
	}
	if attachments.prepareCalls != 1 || attachments.prepareRequest != wantAttachmentRequest {
		t.Fatalf("runtime attachment preparation = %d / %#v, want %#v", attachments.prepareCalls, attachments.prepareRequest, wantAttachmentRequest)
	}
	if got := store.prepared.Preparation.RequestedAttachment; got != attachments.prepared {
		t.Fatalf("requested attachment = %#v, want %#v", got, attachments.prepared)
	}
}

func TestMutations_PrepareRefusesWorkspaceFailureBeforeNonceOrStoreCommit(t *testing.T) {
	privateFailure := errors.New("private workspace allocation failure")
	store := &mutationStore{}
	nonceCalls := 0
	mutations, err := NewMutations(MutationConfig{
		Store: store, Repositories: &repositoryCatalog{},
		Workspaces:         &workspacePreparer{err: privateFailure},
		RuntimeAttachments: &runtimeAttachmentCoordinator{},
		TaskIDs:            func(string) (string, error) { return "task-stable-0001", nil },
		RegistrationNonces: func() (string, error) {
			nonceCalls++
			return "registration-nonce_0001", nil
		},
		PreparationTTL: time.Hour,
		Clock:          func() time.Time { return time.Date(2026, time.August, 10, 9, 0, 0, 0, time.UTC) },
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = mutations.PrepareTask(context.Background(), validPrepareCommand())
	if !errors.Is(err, privateFailure) {
		t.Fatalf("PrepareTask(workspace failure) error = %v", err)
	}
	if nonceCalls != 0 || store.prepareCalls != 0 {
		t.Fatalf("post-workspace side effects = nonce:%d store:%d, want zero", nonceCalls, store.prepareCalls)
	}
}

func TestMutations_PrepareRejectsUnknownProfilesBeforeWorkspaceAllocation(t *testing.T) {
	profileUnavailable := errors.New("profile unavailable")
	tests := []struct {
		name               string
		workerProfiles     WorkerProfileValidator
		validationProfiles ValidationProfileValidator
	}{
		{
			name: "worker profile",
			workerProfiles: func(string, domain.TaskShape) error {
				return profileUnavailable
			},
			validationProfiles: func(string) error { return nil },
		},
		{
			name:               "validation profile",
			workerProfiles:     func(string, domain.TaskShape) error { return nil },
			validationProfiles: func(string) error { return profileUnavailable },
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			store := &mutationStore{}
			workspaces := testWorkspacePreparer()
			attachments := testRuntimeAttachments()
			mutations, err := NewMutations(MutationConfig{
				Store: store, Repositories: &repositoryCatalog{},
				WorkerProfiles: test.workerProfiles, ValidationProfiles: test.validationProfiles,
				Workspaces: workspaces, RuntimeAttachments: attachments,
				TaskIDs:            func(string) (string, error) { return "task-stable-0001", nil },
				RegistrationNonces: testRegistrationNonceSource, PreparationTTL: time.Hour,
				Clock: func() time.Time { return time.Date(2026, time.August, 10, 9, 0, 0, 0, time.UTC) },
			})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := mutations.PrepareTask(context.Background(), validPrepareCommand()); err == nil {
				t.Fatal("PrepareTask(unknown profile) error = nil")
			}
			if workspaces.calls != 0 || attachments.prepareCalls != 0 || store.prepareCalls != 0 {
				t.Fatalf(
					"profile rejection side effects = workspace:%d attachment:%d store:%d, want zero",
					workspaces.calls,
					attachments.prepareCalls,
					store.prepareCalls,
				)
			}
		})
	}
}

func mustRenderWorkerBriefForTest(t *testing.T, task domain.Task) domain.WorkerBrief {
	t.Helper()
	brief, err := task.RenderWorkerBrief()
	if err != nil {
		t.Fatal(err)
	}
	return brief
}

type runtimeAttachmentCoordinator struct {
	prepareRequest RuntimeAttachmentPreparationRequest
	bindRequest    RuntimeAttachmentBindingRequest
	prepared       PreparedRuntimeAttachment
	prepareCalls   int
	bindCalls      int
	err            error
}

func (coordinator *runtimeAttachmentCoordinator) PrepareRuntimeAttachment(
	_ context.Context,
	request RuntimeAttachmentPreparationRequest,
) (PreparedRuntimeAttachment, error) {
	coordinator.prepareCalls++
	coordinator.prepareRequest = request
	return coordinator.prepared, coordinator.err
}

func (coordinator *runtimeAttachmentCoordinator) BindRuntimeAttachment(
	_ context.Context,
	request RuntimeAttachmentBindingRequest,
) error {
	coordinator.bindCalls++
	coordinator.bindRequest = request
	return coordinator.err
}

func (*runtimeAttachmentCoordinator) ReleaseRuntimeAttachment(context.Context, string) error {
	return nil
}

type workspacePreparer struct {
	request  WorkspacePreparationRequest
	prepared PreparedWorkspace
	calls    int
	err      error
}

func (preparer *workspacePreparer) PrepareWorkspace(_ context.Context, request WorkspacePreparationRequest) (PreparedWorkspace, error) {
	preparer.calls++
	preparer.request = request
	return preparer.prepared, preparer.err
}
