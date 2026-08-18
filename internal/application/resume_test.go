package application

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/comisai/comis-dev-crew/internal/domain"
)

func resumeFixture(t *testing.T, state domain.TaskState, cleanliness WorkspaceCleanliness) (
	*Interventions, *interventionStore,
) {
	t.Helper()
	now := time.Date(2026, time.August, 11, 19, 0, 0, 0, time.UTC)
	task := queryTask("task-resume-application", state, 12)
	store := &interventionStore{
		task: task,
		preparation: ManagedRunPreparation{
			ExternalRunRef: task.Handle, RegistrationNonce: "registration-nonce-resume",
			RequestedWorkspaceRoot: "/approved/worktrees/" + task.Handle,
			RequestedAttachment: PreparedRuntimeAttachment{
				Kind: RuntimeAttachmentUnixSocket, SourcePath: "/approved/runtime/attachment.sock",
				RelayIdentity: strings.Repeat("ab", 32),
			},
			ExpiresAt: now.Add(time.Hour), State: PreparationOpen,
		},
	}
	inspector := &interventionInspector{snapshot: WorkspaceSnapshot{
		TaskHandle: task.Handle, RepositoryID: task.RepositoryID,
		WorktreePath: store.preparation.RequestedWorkspaceRoot,
		Branch:       "devcrew/task-resume-application",
		HeadRevision: strings.Repeat("b", 40), Cleanliness: cleanliness,
	}}
	interventions, err := NewInterventions(InterventionConfig{
		Store: store, Workspaces: inspector, Clock: func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("NewInterventions() error = %v", err)
	}
	return interventions, store
}

// This is the rule the command exists for. The paused worker still holds a
// brief, a base revision, and an evidence set describing the tree it stopped on,
// and none of them would notice a developer's edit. Resuming onto a changed tree
// would continue from a description of a tree that no longer exists.
func TestInterventions_ResumeRefusesAWorktreeTheWorkerDidNotLeave(t *testing.T) {
	interventions, store := resumeFixture(t, domain.TaskPaused, WorkspaceDirty)

	_, err := interventions.ResumeTask(context.Background(), ResumeTaskCommand{
		OperationID: "operation-resume-application", TaskHandle: "task-resume-application",
	})

	if err == nil {
		t.Fatal("ResumeTask(dirty worktree) error = nil, want a refusal")
	}
	if store.resumeCalls != 0 {
		t.Error("a refused resume must not reach the durable layer")
	}
	// The refusal must name the command that can absorb the edit. A bare
	// precondition failure leaves an operator holding a deliberate change with
	// no sign the product already has the path they need.
	var failure *domain.Failure
	if !errors.As(err, &failure) {
		t.Fatalf("resume refusal = %v, want a classified failure", err)
	}
	if !strings.Contains(failure.Hint, "validate-developer-work") {
		t.Errorf("resume refusal hint = %q, want it to name handback", failure.Hint)
	}
	if failure.Retryable {
		t.Error("a dirty worktree does not clear by retrying")
	}
}

func TestInterventions_ResumeReturnsACleanPausedTaskToItsWorker(t *testing.T) {
	interventions, store := resumeFixture(t, domain.TaskPaused, WorkspaceClean)

	result, err := interventions.ResumeTask(context.Background(), ResumeTaskCommand{
		OperationID: "operation-resume-application", TaskHandle: "task-resume-application",
	})
	if err != nil {
		t.Fatalf("ResumeTask() error = %v", err)
	}
	if store.resumeCalls != 1 {
		t.Fatalf("resume commits = %d, want 1", store.resumeCalls)
	}
	// The head the caller actually observed travels to the durable layer, so the
	// record says which tree was proven clean rather than merely that one was.
	if store.resume.ObservedHeadRevision != strings.Repeat("b", 40) {
		t.Errorf("recorded head = %q, want the inspected head", store.resume.ObservedHeadRevision)
	}
	if result.Task.State != domain.TaskWorking {
		t.Errorf("resumed state = %q", result.Task.State)
	}
}

// Only a paused task can be resumed, and the state is checked before the
// workspace is inspected: inspecting a running task's worktree would race the
// worker writing to it and could report a dirtiness that means nothing.
func TestInterventions_ResumeRefusesATaskThatIsNotPaused(t *testing.T) {
	interventions, store := resumeFixture(t, domain.TaskWorking, WorkspaceClean)

	if _, err := interventions.ResumeTask(context.Background(), ResumeTaskCommand{
		OperationID: "operation-resume-application", TaskHandle: "task-resume-application",
	}); err == nil {
		t.Fatal("ResumeTask(working) error = nil, want a refusal")
	}
	if store.resumeCalls != 0 {
		t.Error("a refused resume must not reach the durable layer")
	}
}

func TestInterventions_ResumeReplaysARepeatedRequest(t *testing.T) {
	interventions, store := resumeFixture(t, domain.TaskPaused, WorkspaceClean)
	store.replayFound = true
	store.replay = MutationResult{Task: domain.Task{Handle: "task-resume-application", State: domain.TaskWorking}}

	result, err := interventions.ResumeTask(context.Background(), ResumeTaskCommand{
		OperationID: "operation-resume-application", TaskHandle: "task-resume-application",
	})
	if err != nil {
		t.Fatalf("ResumeTask() error = %v", err)
	}
	if store.resumeCalls != 0 {
		t.Error("a replayed resume must not commit a second time")
	}
	if result.Task.State != domain.TaskWorking {
		t.Errorf("replayed state = %q", result.Task.State)
	}
}

func TestInterventions_ResumeRefusesForgedIdentityAndDeadContexts(t *testing.T) {
	interventions, _ := resumeFixture(t, domain.TaskPaused, WorkspaceClean)
	valid := ResumeTaskCommand{
		OperationID: "operation-resume-application", TaskHandle: "task-resume-application",
	}

	for name, command := range map[string]ResumeTaskCommand{
		"no operation":     {TaskHandle: "task-resume-application"},
		"no task":          {OperationID: "operation-resume-application"},
		"forged operation": {OperationID: "../../etc", TaskHandle: "task-resume-application"},
		"forged task":      {OperationID: "operation-resume-application", TaskHandle: "task resume"},
	} {
		if _, err := interventions.ResumeTask(context.Background(), command); err == nil {
			t.Errorf("%s: expected the resume to be refused", name)
		}
	}
	if _, err := interventions.ResumeTask(nilResumeContext(), valid); err == nil {
		t.Error("ResumeTask(nil) error = nil")
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := interventions.ResumeTask(cancelled, valid); err == nil {
		t.Error("ResumeTask(cancelled) error = nil")
	}
}

// Returned through a function so the nil is not a literal argument at the call
// site, matching how this package's own tests exercise the guard.
func nilResumeContext() context.Context { return nil }
