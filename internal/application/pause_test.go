package application

import (
	"context"
	"testing"
	"time"

	"github.com/comisai/comis-dev-crew/internal/domain"
)

// Pause asks; it does not decide. A worker holding the worktree does not stop
// because the host wrote a state, so the command records a request and leaves
// the task where it is. A task that claimed "paused" here would hand a
// developer a worktree still changing under their editor.
func TestMutations_PauseTask_RecordsARequestWithoutTransitioningTheTask(t *testing.T) {
	store := &mutationStore{}
	mutations := newTestMutations(t, store)

	result, err := mutations.PauseTask(context.Background(), PauseTaskCommand{
		OperationID: "operation-pause-0001", TaskHandle: "task-0001",
	})
	if err != nil {
		t.Fatalf("PauseTask() error = %v", err)
	}
	if store.pauseRequest.TaskHandle != "task-0001" {
		t.Errorf("pause request = %+v, want task-0001", store.pauseRequest)
	}
	if store.pauseRequest.SubjectDigest == "" {
		t.Error("a pause request must carry a subject digest so a replay can be recognised")
	}
	if result.Task.State == domain.TaskPaused {
		t.Error("PauseTask must not settle the task; only the worker's paused report does")
	}
}

// A repeated pause is the ordinary case: an operator retries, or two operators
// ask at once. It must replay rather than record a second request.
func TestMutations_PauseTask_ReplaysARepeatedRequest(t *testing.T) {
	store := &mutationStore{
		replayFound: true,
		replayResult: MutationResult{
			Task:      domain.Task{Handle: "task-0001", State: domain.TaskWorking},
			Operation: domain.OperationRecord{ID: "operation-pause-0001"},
		},
	}
	mutations := newTestMutations(t, store)

	result, err := mutations.PauseTask(context.Background(), PauseTaskCommand{
		OperationID: "operation-pause-0001", TaskHandle: "task-0001",
	})
	if err != nil {
		t.Fatalf("PauseTask() error = %v", err)
	}
	if result.Operation.ID != "operation-pause-0001" {
		t.Errorf("replayed operation = %q", result.Operation.ID)
	}
	if store.pauseRequest.OperationID != "" {
		t.Error("a replayed pause must not record a second request")
	}
}

func TestMutations_PauseTask_RefusesForgedIdentity(t *testing.T) {
	mutations := newTestMutations(t, &mutationStore{})

	for name, command := range map[string]PauseTaskCommand{
		"no operation":     {TaskHandle: "task-0001"},
		"no task":          {OperationID: "operation-pause-0001"},
		"forged operation": {OperationID: "../../etc", TaskHandle: "task-0001"},
		"forged task":      {OperationID: "operation-pause-0001", TaskHandle: "task 0001"},
	} {
		if _, err := mutations.PauseTask(context.Background(), command); err == nil {
			t.Errorf("%s: expected the pause to be refused", name)
		}
	}
}

func TestMutations_PauseTask_RefusesAnAbsentOrDoneContext(t *testing.T) {
	mutations := newTestMutations(t, &mutationStore{})
	command := PauseTaskCommand{OperationID: "operation-pause-0001", TaskHandle: "task-0001"}

	if _, err := mutations.PauseTask(nilPauseContext(), command); err == nil {
		t.Error("PauseTask(nil) error = nil")
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := mutations.PauseTask(cancelled, command); err == nil {
		t.Error("PauseTask(cancelled) error = nil")
	}
}

func nilPauseContext() context.Context { return nil }

func newTestMutations(t *testing.T, store *mutationStore) *Mutations {
	t.Helper()
	clock := time.Date(2026, time.August, 9, 12, 30, 0, 0, time.UTC)
	mutations, err := NewMutations(MutationConfig{
		Store: store, Repositories: &repositoryCatalog{},
		Workspaces: testWorkspacePreparer(), RuntimeAttachments: testRuntimeAttachments(),
		WorkerProfiles: acceptingWorkerProfile, ValidationProfiles: acceptingValidationProfile,
		TaskIDs:            func(string) (string, error) { return "task-0001", nil },
		RegistrationNonces: func() (string, error) { return "registration-nonce_0001", nil },
		PreparationTTL:     15 * time.Minute,
		Clock:              func() time.Time { return clock },
	})
	if err != nil {
		t.Fatalf("NewMutations() error = %v", err)
	}
	return mutations
}
