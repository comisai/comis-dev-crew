package application

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestMutations_PrepareAllocatesOperationBoundWorkspaceBeforeDurableComisPreparation(t *testing.T) {
	store := &mutationStore{}
	workspaces := &workspacePreparer{prepared: PreparedWorkspace{CanonicalRoot: "/approved/worktrees/task-stable-0001"}}
	var taskOperation string
	mutations, err := NewMutations(MutationConfig{
		Store: store, Repositories: &repositoryCatalog{}, Workspaces: workspaces,
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
}

func TestMutations_PrepareRefusesWorkspaceFailureBeforeNonceOrStoreCommit(t *testing.T) {
	privateFailure := errors.New("private workspace allocation failure")
	store := &mutationStore{}
	nonceCalls := 0
	mutations, err := NewMutations(MutationConfig{
		Store: store, Repositories: &repositoryCatalog{},
		Workspaces: &workspacePreparer{err: privateFailure},
		TaskIDs:    func(string) (string, error) { return "task-stable-0001", nil },
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
