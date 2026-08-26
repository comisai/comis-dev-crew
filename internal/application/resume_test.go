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
	if result.Task.State != domain.TaskReady {
		t.Errorf("resumed state = %q", result.Task.State)
	}
}

// A worker commits through lease-private Git administration so its branch
// update cannot escape the task lease. Until the service promotes that exact
// verified commit, ordinary shared Git sees the committed files as dirty. That
// is the worker's own clean handoff, not a developer edit.
func TestInterventions_ResumePromotesAWorkersCleanPrivateCommitBeforeRelaunch(t *testing.T) {
	interventions, store := resumeFixture(t, domain.TaskPaused, WorkspaceDirty)
	inspector := &promotingInterventionInspector{
		interventionInspector: *(interventions.workspaces.(*interventionInspector)),
	}
	inspector.promoted = inspector.snapshot
	inspector.promoted.HeadRevision = strings.Repeat("c", 40)
	inspector.promoted.Cleanliness = WorkspaceClean
	interventions.workspaces = inspector
	store.preparationOperationID = "operation-prepare-resume-private"

	result, err := interventions.ResumeTask(context.Background(), ResumeTaskCommand{
		OperationID: "operation-resume-private", TaskHandle: store.task.Handle,
	})
	if err != nil {
		t.Fatalf("ResumeTask(private clean commit) error = %v", err)
	}
	if inspector.promoteCalls != 1 {
		t.Fatalf("private candidate promotions = %d, want 1", inspector.promoteCalls)
	}
	if store.resume.ObservedHeadRevision != inspector.promoted.HeadRevision || result.Task.State != domain.TaskReady {
		t.Fatalf("resumed private candidate = %#v, mutation %#v", result.Task, store.resume)
	}
}

func TestInterventions_ResumeRotatesTheProtectedAcknowledgementGeneration(t *testing.T) {
	interventions, store := resumeFixture(t, domain.TaskPaused, WorkspaceClean)
	runtimeLaunches := &interventionRuntimeLaunches{}
	interventions.runtimeLaunches = runtimeLaunches

	result, err := interventions.ResumeTask(context.Background(), ResumeTaskCommand{
		OperationID: "operation-resume-runtime-generation", TaskHandle: store.task.Handle,
	})
	if err != nil {
		t.Fatalf("ResumeTask(runtime generation) error = %v", err)
	}
	wantOperationID, err := RuntimeRelaunchAcknowledgementOperationID(
		result.Task.Handle, result.Task.StateVersion,
	)
	if err != nil {
		t.Fatal(err)
	}
	if runtimeLaunches.calls != 1 || runtimeLaunches.request != (RuntimeAttachmentLaunchRebindRequest{
		TaskHandle: result.Task.Handle, ReadyStateVersion: result.Task.StateVersion,
		LaunchOperationID: wantOperationID, Brief: mustRenderResumeBrief(t, result.Task),
	}) {
		t.Fatalf("runtime launch rebind = %d/%#v", runtimeLaunches.calls, runtimeLaunches.request)
	}
}

func TestInterventions_ResumeRelaunchFailsClosedOnIncompleteGenerationAuthority(t *testing.T) {
	interventions, store := resumeFixture(t, domain.TaskPaused, WorkspaceClean)
	ready := store.task
	ready.State = domain.TaskReady
	interventions.runtimeLaunches = &interventionRuntimeLaunches{err: errors.New("runtime unavailable")}
	if err := interventions.rebindReadyWorkerLaunch(context.Background(), ready); err == nil {
		t.Fatal("rebindReadyWorkerLaunch(runtime failure) error = nil")
	}
	ready.StateVersion = 0
	if err := interventions.rebindReadyWorkerLaunch(context.Background(), ready); err == nil {
		t.Fatal("rebindReadyWorkerLaunch(absent generation) error = nil")
	}
	ready.StateVersion = store.task.StateVersion
	ready.BriefRevisionHash = strings.Repeat("f", 64)
	if err := interventions.rebindReadyWorkerLaunch(context.Background(), ready); err == nil {
		t.Fatal("rebindReadyWorkerLaunch(unpinned brief) error = nil")
	}
	ready.State = domain.TaskWorking
	if err := interventions.rebindReadyWorkerLaunch(context.Background(), ready); err != nil {
		t.Fatalf("rebindReadyWorkerLaunch(advanced replay) error = %v", err)
	}
}

func TestInterventions_ResumePrivateHandoffRefusesIncompleteOrUnavailableAuthority(t *testing.T) {
	interventions, store := resumeFixture(t, domain.TaskPaused, WorkspaceDirty)
	inspector := &promotingInterventionInspector{
		interventionInspector: *(interventions.workspaces.(*interventionInspector)),
		promoteErr:            errors.New("private Git unavailable"),
	}
	interventions.workspaces = inspector
	if _, found := interventions.promotePausedWorkerCandidate(context.Background(), store.task); found {
		t.Fatal("private handoff accepted an absent preparation operation")
	}
	store.preparationOperationID = "operation-prepare-private-unavailable"
	if _, found := interventions.promotePausedWorkerCandidate(context.Background(), store.task); found {
		t.Fatal("private handoff accepted an unavailable Git promotion")
	}
}

func mustRenderResumeBrief(t *testing.T, task domain.Task) domain.WorkerBrief {
	t.Helper()
	brief, err := task.RenderWorkerBrief()
	if err != nil {
		t.Fatal(err)
	}
	return brief
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
	store.replay = MutationResult{Task: domain.Task{Handle: "task-resume-application", State: domain.TaskReady}}

	result, err := interventions.ResumeTask(context.Background(), ResumeTaskCommand{
		OperationID: "operation-resume-application", TaskHandle: "task-resume-application",
	})
	if err != nil {
		t.Fatalf("ResumeTask() error = %v", err)
	}
	if store.resumeCalls != 0 {
		t.Error("a replayed resume must not commit a second time")
	}
	if result.Task.State != domain.TaskReady {
		t.Errorf("replayed state = %q", result.Task.State)
	}
}

func TestInterventions_ResumeClassifiesEveryDependencyBoundaryFailure(t *testing.T) {
	t.Run("replay read failure", func(t *testing.T) {
		interventions, store := resumeFixture(t, domain.TaskPaused, WorkspaceClean)
		store.replayErr = errors.New("replay unavailable")
		if _, err := interventions.ResumeTask(context.Background(), ResumeTaskCommand{
			OperationID: "operation-resume-replay-failure", TaskHandle: store.task.Handle,
		}); err == nil {
			t.Fatal("ResumeTask(replay failure) error = nil")
		}
	})

	t.Run("task read failure", func(t *testing.T) {
		interventions, store := resumeFixture(t, domain.TaskPaused, WorkspaceClean)
		store.task = domain.Task{}
		if _, err := interventions.ResumeTask(context.Background(), ResumeTaskCommand{
			OperationID: "operation-resume-task-read-failure", TaskHandle: "task-resume-application",
		}); err == nil {
			t.Fatal("ResumeTask(task read failure) error = nil")
		}
	})

	t.Run("preparation read failure", func(t *testing.T) {
		interventions, store := resumeFixture(t, domain.TaskPaused, WorkspaceClean)
		store.preparation.RequestedWorkspaceRoot = ""
		if _, err := interventions.ResumeTask(context.Background(), ResumeTaskCommand{
			OperationID: "operation-resume-preparation-failure", TaskHandle: store.task.Handle,
		}); err == nil {
			t.Fatal("ResumeTask(preparation failure) error = nil")
		}
	})

	t.Run("workspace inspection failure", func(t *testing.T) {
		interventions, store := resumeFixture(t, domain.TaskPaused, WorkspaceClean)
		interventions.workspaces.(*interventionInspector).err = errors.New("Git unavailable")
		if _, err := interventions.ResumeTask(context.Background(), ResumeTaskCommand{
			OperationID: "operation-resume-inspection-failure", TaskHandle: store.task.Handle,
		}); err == nil {
			t.Fatal("ResumeTask(inspection failure) error = nil")
		}
	})

	t.Run("workspace authority mismatch", func(t *testing.T) {
		interventions, store := resumeFixture(t, domain.TaskPaused, WorkspaceClean)
		interventions.workspaces.(*interventionInspector).snapshot.TaskHandle = "task-different-authority"
		if _, err := interventions.ResumeTask(context.Background(), ResumeTaskCommand{
			OperationID: "operation-resume-authority-failure", TaskHandle: store.task.Handle,
		}); err == nil {
			t.Fatal("ResumeTask(authority mismatch) error = nil")
		}
	})

	t.Run("durable commit failure", func(t *testing.T) {
		interventions, store := resumeFixture(t, domain.TaskPaused, WorkspaceClean)
		store.commitErr = errors.New("store unavailable")
		if _, err := interventions.ResumeTask(context.Background(), ResumeTaskCommand{
			OperationID: "operation-resume-commit-failure", TaskHandle: store.task.Handle,
		}); err == nil {
			t.Fatal("ResumeTask(commit failure) error = nil")
		}
	})

	t.Run("replayed generation rebind failure", func(t *testing.T) {
		interventions, store := resumeFixture(t, domain.TaskPaused, WorkspaceClean)
		store.replayFound = true
		store.replay = MutationResult{Task: store.task}
		store.replay.Task.State = domain.TaskReady
		interventions.runtimeLaunches = &interventionRuntimeLaunches{err: errors.New("runtime unavailable")}
		if _, err := interventions.ResumeTask(context.Background(), ResumeTaskCommand{
			OperationID: "operation-resume-rebind-replay-failure", TaskHandle: store.task.Handle,
		}); err == nil {
			t.Fatal("ResumeTask(replayed rebind failure) error = nil")
		}
	})
}

type promotingInterventionInspector struct {
	interventionInspector
	promoted     WorkspaceSnapshot
	promoteCalls int
	promoteErr   error
}

type interventionRuntimeLaunches struct {
	request RuntimeAttachmentLaunchRebindRequest
	calls   int
	err     error
}

func (launches *interventionRuntimeLaunches) RebindRuntimeAttachmentLaunch(
	_ context.Context,
	request RuntimeAttachmentLaunchRebindRequest,
) error {
	launches.calls++
	launches.request = request
	return launches.err
}

func (inspector *promotingInterventionInspector) PromoteReconciliationCandidate(
	_ context.Context,
	_ ReconciliationWorkspaceRequest,
) (WorkspaceSnapshot, error) {
	inspector.promoteCalls++
	return inspector.promoted, inspector.promoteErr
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
