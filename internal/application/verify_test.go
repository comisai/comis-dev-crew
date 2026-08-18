package application

import (
	"context"
	"testing"

	"github.com/comisai/comis-dev-crew/internal/domain"
)

// Verify opens validation and reports that it started. It must not report a
// verdict: the judgement lands later, as evidence, and a command that answered
// immediately would be a second judge with a different view of the same tree.
func TestMutations_VerifyTask_OpensValidationWithoutReportingAVerdict(t *testing.T) {
	store := &mutationStore{}
	mutations := newTestMutations(t, store)

	result, err := mutations.VerifyTask(context.Background(), VerifyTaskCommand{
		OperationID: "operation-verify-0001", TaskHandle: "task-0001",
	})
	if err != nil {
		t.Fatalf("VerifyTask() error = %v", err)
	}
	if store.verifyTask.TaskHandle != "task-0001" {
		t.Errorf("verify mutation = %+v", store.verifyTask)
	}
	if store.verifyTask.SubjectDigest == "" {
		t.Error("a verify must carry a subject digest so a replay can be recognised")
	}
	if result.Task.State != domain.TaskValidating {
		t.Errorf("verify result state = %q, want validating", result.Task.State)
	}
}

func TestMutations_VerifyTask_ReplaysARepeatedRequest(t *testing.T) {
	store := &mutationStore{
		replayFound: true,
		replayResult: MutationResult{
			Task:      domain.Task{Handle: "task-0001", State: domain.TaskValidating},
			Operation: domain.OperationRecord{ID: "operation-verify-0001"},
		},
	}
	mutations := newTestMutations(t, store)

	if _, err := mutations.VerifyTask(context.Background(), VerifyTaskCommand{
		OperationID: "operation-verify-0001", TaskHandle: "task-0001",
	}); err != nil {
		t.Fatalf("VerifyTask() error = %v", err)
	}
	if store.verifyTask.OperationID != "" {
		t.Error("a replayed verify must not open a second validation")
	}
}

func TestMutations_VerifyTask_RefusesForgedIdentityAndDeadContexts(t *testing.T) {
	mutations := newTestMutations(t, &mutationStore{})
	valid := VerifyTaskCommand{OperationID: "operation-verify-0001", TaskHandle: "task-0001"}

	for name, command := range map[string]VerifyTaskCommand{
		"no operation":     {TaskHandle: "task-0001"},
		"no task":          {OperationID: "operation-verify-0001"},
		"forged operation": {OperationID: "../../etc", TaskHandle: "task-0001"},
		"forged task":      {OperationID: "operation-verify-0001", TaskHandle: "task 0001"},
	} {
		if _, err := mutations.VerifyTask(context.Background(), command); err == nil {
			t.Errorf("%s: expected the verify to be refused", name)
		}
	}
	if _, err := mutations.VerifyTask(nilPauseContext(), valid); err == nil {
		t.Error("VerifyTask(nil) error = nil")
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := mutations.VerifyTask(cancelled, valid); err == nil {
		t.Error("VerifyTask(cancelled) error = nil")
	}
}

// Cancel shares the same skeleton and needs the same proof that its own command
// name reaches the replay index: a shared name would make a pause replay answer
// a cancel.
func TestMutations_CancelTask_UsesItsOwnCommandIdentity(t *testing.T) {
	store := &mutationStore{}
	mutations := newTestMutations(t, store)

	if _, err := mutations.CancelTask(context.Background(), CancelTaskCommand{
		OperationID: "operation-cancel-0001", TaskHandle: "task-0001",
	}); err != nil {
		t.Fatalf("CancelTask() error = %v", err)
	}
	if store.cancelTask.TaskHandle != "task-0001" || store.cancelTask.SubjectDigest == "" {
		t.Errorf("cancel mutation = %+v", store.cancelTask)
	}
	// The pause slot must be untouched: two commands sharing a skeleton must not
	// share a destination.
	if store.pauseRequest.OperationID != "" {
		t.Error("a cancel must not commit a pause request")
	}
}

func TestMutations_CancelTask_RefusesForgedIdentityAndDeadContexts(t *testing.T) {
	mutations := newTestMutations(t, &mutationStore{})
	valid := CancelTaskCommand{OperationID: "operation-cancel-0001", TaskHandle: "task-0001"}

	for name, command := range map[string]CancelTaskCommand{
		"no operation": {TaskHandle: "task-0001"},
		"forged task":  {OperationID: "operation-cancel-0001", TaskHandle: "../../etc"},
	} {
		if _, err := mutations.CancelTask(context.Background(), command); err == nil {
			t.Errorf("%s: expected the cancel to be refused", name)
		}
	}
	if _, err := mutations.CancelTask(nilPauseContext(), valid); err == nil {
		t.Error("CancelTask(nil) error = nil")
	}
}
