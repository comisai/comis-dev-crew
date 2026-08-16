package application

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/comisai/comis-dev-crew/internal/domain"
)

func TestTaskCandidateReconciler_ValidatesCleanUnknownCandidateWithoutWorkerReport(t *testing.T) {
	now := time.Date(2026, time.August, 13, 10, 0, 0, 0, time.UTC)
	authority := reconciliationAuthority(now)
	store := &taskReconciliationStore{authority: authority}
	inspector := &taskReconciliationInspector{snapshot: WorkspaceSnapshot{
		TaskHandle: authority.Task.Handle, RepositoryID: authority.Task.RepositoryID,
		WorktreePath: authority.Preparation.RequestedWorkspaceRoot,
		Branch:       "devcrew/task-reconcile-application-aaaaaaaaaaaaaaaaaaaaaaaa",
		HeadRevision: strings.Repeat("b", 40), Cleanliness: WorkspaceClean,
	}}
	reconciler, err := NewTaskCandidateReconciler(TaskCandidateReconcilerConfig{
		Store: store, Workspaces: inspector, Clock: func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("NewTaskCandidateReconciler() error = %v", err)
	}
	command := ReconcileTaskCommand{
		OperationID: "operation-reconcile-application", TaskHandle: authority.Task.Handle,
		Action: ReconcileValidateCleanCandidate,
	}
	if _, err := reconciler.ReconcileTask(context.Background(), command); err != nil {
		t.Fatalf("ReconcileTask() error = %v", err)
	}
	wantRequest := ReconciliationWorkspaceRequest{
		PreparationOperationID: authority.PreparationOperationID,
		TaskHandle:             authority.Task.Handle, RepositoryID: authority.Task.RepositoryID,
		WorktreePath: authority.Preparation.RequestedWorkspaceRoot,
		BaseRevision: authority.Task.BaseRevision,
	}
	if inspector.calls != 1 || inspector.request != wantRequest {
		t.Fatalf("workspace promotion = %d/%#v, want %#v", inspector.calls, inspector.request, wantRequest)
	}
	mutation := store.mutation
	if store.commitCalls != 1 || mutation.OperationID != command.OperationID ||
		mutation.TaskHandle != authority.Task.Handle || mutation.Action != command.Action ||
		mutation.PreparationOperationID != authority.PreparationOperationID ||
		mutation.Snapshot != inspector.snapshot || mutation.TerminalSessionID != authority.TerminalSessionID ||
		mutation.TerminalTransition != TerminalExited || mutation.TerminalObservedAt != authority.TerminalObservedAt ||
		mutation.ExpectedTaskVersion != authority.Task.StateVersion || mutation.At != now || len(mutation.SubjectDigest) != 64 {
		t.Fatalf("candidate reconciliation mutation = %#v", mutation)
	}
	if authority.Task.ReportCursor != 7 {
		t.Fatalf("fixture report cursor = %d, want unchanged worker cursor", authority.Task.ReportCursor)
	}
}

func TestTaskCandidateReconciler_ReplaysBeforeAuthorityOrWorkspaceInspection(t *testing.T) {
	now := time.Date(2026, time.August, 13, 10, 5, 0, 0, time.UTC)
	authority := reconciliationAuthority(now)
	replay := MutationResult{
		Task: authority.Task,
		Operation: domain.OperationRecord{
			SchemaVersion: 1, ID: "operation-reconcile-replay", Command: "ReconcileTask",
			SubjectDigest: strings.Repeat("a", 64), Status: domain.OperationCompleted,
			ResultRef: authority.Task.Handle, StateVersion: authority.Task.StateVersion,
			CreatedAt: now, UpdatedAt: now,
		},
	}
	store := &taskReconciliationStore{replay: replay, replayFound: true}
	inspector := &taskReconciliationInspector{}
	reconciler, err := NewTaskCandidateReconciler(TaskCandidateReconcilerConfig{
		Store: store, Workspaces: inspector, Clock: func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	got, err := reconciler.ReconcileTask(context.Background(), ReconcileTaskCommand{
		OperationID: replay.Operation.ID, TaskHandle: authority.Task.Handle,
		Action: ReconcileValidateCleanCandidate,
	})
	if err != nil || !reflect.DeepEqual(got, replay) || store.authorityCalls != 0 ||
		inspector.calls != 0 || store.commitCalls != 0 {
		t.Fatalf("ReconcileTask(replay) = %#v, %v, authority=%d inspect=%d commit=%d",
			got, err, store.authorityCalls, inspector.calls, store.commitCalls)
	}
}

func TestTaskCandidateReconciler_ClassifiesMissingDurableAuthorityAsPrecondition(t *testing.T) {
	now := time.Date(2026, time.August, 13, 10, 7, 0, 0, time.UTC)
	authority := reconciliationAuthority(now)
	store := &taskReconciliationStore{authorityErr: ErrPrecondition}
	reconciler, err := NewTaskCandidateReconciler(TaskCandidateReconcilerConfig{
		Store: store, Workspaces: &taskReconciliationInspector{}, Clock: func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = reconciler.ReconcileTask(context.Background(), ReconcileTaskCommand{
		OperationID: "operation-reconcile-precondition", TaskHandle: authority.Task.Handle,
		Action: ReconcileValidateCleanCandidate,
	})
	var failure *domain.Failure
	if !errors.As(err, &failure) || failure.Code != domain.ErrorPrecondition || failure.Retryable {
		t.Fatalf("ReconcileTask(missing authority) error = %#v, want nonretryable precondition", err)
	}
}

func TestTaskCandidateReconciler_ClassifiesBoundaryFailuresWithoutLeakingAuthority(t *testing.T) {
	now := time.Date(2026, time.August, 13, 10, 8, 0, 0, time.UTC)
	tests := []struct {
		name      string
		command   ReconcileTaskCommand
		configure func(*taskReconciliationStore, *taskReconciliationInspector)
		clock     Clock
		wantCode  domain.ErrorCode
		wantRetry bool
	}{
		{name: "invalid closed action", command: ReconcileTaskCommand{
			OperationID: "operation-reconcile-boundary", TaskHandle: "task-reconcile-application",
			Action: ReconcileTaskAction("invented"),
		}, wantCode: domain.ErrorInvalidArgument},
		{name: "replay lookup unavailable", configure: func(store *taskReconciliationStore, _ *taskReconciliationInspector) {
			store.replayErr = ErrConflict
		}, wantCode: domain.ErrorConflict},
		{name: "authority is not found", configure: func(store *taskReconciliationStore, _ *taskReconciliationInspector) {
			store.authorityErr = ErrNotFound
		}, wantCode: domain.ErrorPrecondition},
		{name: "service clock is invalid", clock: func() time.Time { return time.Time{} },
			wantCode: domain.ErrorUnavailable, wantRetry: true},
		{name: "commit authority changed", configure: func(store *taskReconciliationStore, _ *taskReconciliationInspector) {
			store.commitErr = ErrPrecondition
		}, wantCode: domain.ErrorPrecondition},
		{name: "commit replay conflicts", configure: func(store *taskReconciliationStore, _ *taskReconciliationInspector) {
			store.commitErr = ErrConflict
		}, wantCode: domain.ErrorConflict},
		{name: "commit result names another task", configure: func(store *taskReconciliationStore, _ *taskReconciliationInspector) {
			store.result = MutationResult{Task: domain.Task{Handle: "task-reconcile-other"}}
		}, wantCode: domain.ErrorUnavailable, wantRetry: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			authority := reconciliationAuthority(now)
			store := &taskReconciliationStore{authority: authority}
			inspector := &taskReconciliationInspector{snapshot: WorkspaceSnapshot{
				TaskHandle: authority.Task.Handle, RepositoryID: authority.Task.RepositoryID,
				WorktreePath: authority.Preparation.RequestedWorkspaceRoot,
				Branch:       "devcrew/task-reconcile-boundary", HeadRevision: strings.Repeat("b", 40),
				Cleanliness: WorkspaceClean,
			}}
			if test.configure != nil {
				test.configure(store, inspector)
			}
			clock := test.clock
			if clock == nil {
				clock = func() time.Time { return now }
			}
			reconciler, err := NewTaskCandidateReconciler(TaskCandidateReconcilerConfig{
				Store: store, Workspaces: inspector, Clock: clock,
			})
			if err != nil {
				t.Fatal(err)
			}
			command := test.command
			if command.OperationID == "" {
				command = ReconcileTaskCommand{
					OperationID: "operation-reconcile-boundary", TaskHandle: authority.Task.Handle,
					Action: ReconcileValidateCleanCandidate,
				}
			}
			_, err = reconciler.ReconcileTask(context.Background(), command)
			var failure *domain.Failure
			if !errors.As(err, &failure) || failure.Code != test.wantCode || failure.Retryable != test.wantRetry {
				t.Fatalf("ReconcileTask() error = %#v, want %s retry=%t", err, test.wantCode, test.wantRetry)
			}
		})
	}

	if err := reconciliationPreconditionFailure("", "", nil); err.Error() != "task reconciliation precondition classification failed" {
		t.Fatalf("reconciliationPreconditionFailure(invalid) = %v", err)
	}
	if err := reconciliationUnavailableFailure("", "", nil); err.Error() != "task reconciliation availability classification failed" {
		t.Fatalf("reconciliationUnavailableFailure(invalid) = %v", err)
	}
}

func TestTaskCandidateReconciler_RefusesAmbiguousOrUnsafeRecoveryEvidence(t *testing.T) {
	now := time.Date(2026, time.August, 13, 10, 10, 0, 0, time.UTC)
	privateCause := errors.New("private workspace and terminal detail")
	tests := []struct {
		name      string
		mutate    func(*TaskReconciliationAuthority, *taskReconciliationInspector, *taskReconciliationStore)
		cancelled bool
	}{
		{name: "task is not unknown", mutate: func(authority *TaskReconciliationAuthority, _ *taskReconciliationInspector, _ *taskReconciliationStore) {
			authority.Task.State = domain.TaskWorking
		}},
		{name: "preparation is unavailable", mutate: func(authority *TaskReconciliationAuthority, _ *taskReconciliationInspector, _ *taskReconciliationStore) {
			authority.Preparation.RequestedWorkspaceRoot = ""
		}},
		{name: "preparation is closed", mutate: func(authority *TaskReconciliationAuthority, _ *taskReconciliationInspector, _ *taskReconciliationStore) {
			closedAt := now.Add(-time.Minute)
			authority.Preparation.State = PreparationAbandoned
			authority.Preparation.AbandonReason = AbandonReasonServiceUnavailable
			authority.Preparation.Disposition = AbandonDispositionPreserve
			authority.Preparation.ClosedAt = &closedAt
		}},
		{name: "terminal is still running", mutate: func(authority *TaskReconciliationAuthority, _ *taskReconciliationInspector, _ *taskReconciliationStore) {
			authority.TerminalTransition = TerminalRunning
		}},
		{name: "terminal loss is unresolved", mutate: func(authority *TaskReconciliationAuthority, _ *taskReconciliationInspector, _ *taskReconciliationStore) {
			authority.TerminalTransition = TerminalLost
		}},
		{name: "workspace is dirty", mutate: func(_ *TaskReconciliationAuthority, inspector *taskReconciliationInspector, _ *taskReconciliationStore) {
			inspector.snapshot.Cleanliness = WorkspaceDirty
		}},
		{name: "candidate head is unchanged", mutate: func(authority *TaskReconciliationAuthority, inspector *taskReconciliationInspector, _ *taskReconciliationStore) {
			inspector.snapshot.HeadRevision = authority.Task.BaseRevision
		}},
		{name: "workspace identity differs", mutate: func(_ *TaskReconciliationAuthority, inspector *taskReconciliationInspector, _ *taskReconciliationStore) {
			inspector.snapshot.TaskHandle = "task-reconcile-other"
		}},
		{name: "authority read fails", mutate: func(_ *TaskReconciliationAuthority, _ *taskReconciliationInspector, store *taskReconciliationStore) {
			store.authorityErr = privateCause
		}},
		{name: "workspace inspection fails", mutate: func(_ *TaskReconciliationAuthority, inspector *taskReconciliationInspector, _ *taskReconciliationStore) {
			inspector.err = privateCause
		}},
		{name: "durable commit fails", mutate: func(_ *TaskReconciliationAuthority, _ *taskReconciliationInspector, store *taskReconciliationStore) {
			store.commitErr = privateCause
		}},
		{name: "cancelled request", mutate: func(*TaskReconciliationAuthority, *taskReconciliationInspector, *taskReconciliationStore) {}, cancelled: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			authority := reconciliationAuthority(now)
			store := &taskReconciliationStore{authority: authority}
			inspector := &taskReconciliationInspector{snapshot: WorkspaceSnapshot{
				TaskHandle: authority.Task.Handle, RepositoryID: authority.Task.RepositoryID,
				WorktreePath: authority.Preparation.RequestedWorkspaceRoot,
				Branch:       "devcrew/task-reconcile-application-aaaaaaaaaaaaaaaaaaaaaaaa",
				HeadRevision: strings.Repeat("b", 40), Cleanliness: WorkspaceClean,
			}}
			test.mutate(&authority, inspector, store)
			store.authority = authority
			reconciler, err := NewTaskCandidateReconciler(TaskCandidateReconcilerConfig{
				Store: store, Workspaces: inspector, Clock: func() time.Time { return now },
			})
			if err != nil {
				t.Fatal(err)
			}
			ctx := context.Background()
			if test.cancelled {
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}
			_, err = reconciler.ReconcileTask(ctx, ReconcileTaskCommand{
				OperationID: "operation-reconcile-refusal", TaskHandle: authority.Task.Handle,
				Action: ReconcileValidateCleanCandidate,
			})
			if err == nil {
				t.Fatal("ReconcileTask() error = nil")
			}
			if store.commitCalls != 0 && test.name != "durable commit fails" {
				t.Fatalf("durable commit calls = %d, want zero", store.commitCalls)
			}
		})
	}
	invalid, err := NewTaskCandidateReconciler(TaskCandidateReconcilerConfig{})
	if err == nil || invalid != nil {
		t.Fatalf("NewTaskCandidateReconciler(incomplete) = %#v, %v", invalid, err)
	}
}

func reconciliationAuthority(now time.Time) TaskReconciliationAuthority {
	task := queryTask("task-reconcile-application", domain.TaskUnknown, 31)
	task.ManagedRunID = "managed-run-reconcile"
	task.WorkspaceLeaseID = "workspace-lease-reconcile"
	task.ExecutionAttachmentID = "execution-attachment-reconcile"
	task.AttachmentTargetName = "attachment-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa.sock"
	task.ReportCursor = 7
	return TaskReconciliationAuthority{
		Task: task, PreparationOperationID: "operation-prepare-reconcile",
		Preparation: ManagedRunPreparation{
			ExternalRunRef: task.Handle, RegistrationNonce: "registration-nonce-reconcile",
			RequestedWorkspaceRoot: "/approved/worktrees/" + task.Handle,
			RequestedAttachment: PreparedRuntimeAttachment{
				Kind: RuntimeAttachmentUnixSocket, SourcePath: "/approved/runtime/attachment.sock",
			},
			ExpiresAt: now.Add(time.Hour), State: PreparationOpen,
		},
		TerminalSessionID: "terminal-session-reconcile", TerminalTransition: TerminalExited,
		TerminalObservedAt: now.Add(-time.Minute),
	}
}

type taskReconciliationStore struct {
	authority      TaskReconciliationAuthority
	mutation       TaskCandidateReconciliationMutation
	replay         MutationResult
	replayFound    bool
	replayErr      error
	authorityErr   error
	commitErr      error
	result         MutationResult
	authorityCalls int
	commitCalls    int
}

func (store *taskReconciliationStore) ReplayMutation(context.Context, string, string, string) (MutationResult, bool, error) {
	return store.replay, store.replayFound, store.replayErr
}

func (store *taskReconciliationStore) ReadTaskReconciliationAuthority(
	context.Context,
	string,
) (TaskReconciliationAuthority, error) {
	store.authorityCalls++
	return store.authority, store.authorityErr
}

func (store *taskReconciliationStore) CommitTaskCandidateReconciliation(
	_ context.Context,
	mutation TaskCandidateReconciliationMutation,
) (MutationResult, error) {
	store.commitCalls++
	store.mutation = mutation
	if store.commitErr != nil {
		return MutationResult{}, store.commitErr
	}
	if store.result.Task.Handle != "" {
		return store.result, nil
	}
	return MutationResult{Task: store.authority.Task}, nil
}

type taskReconciliationInspector struct {
	request  ReconciliationWorkspaceRequest
	snapshot WorkspaceSnapshot
	err      error
	calls    int
}

func (inspector *taskReconciliationInspector) InspectReconciliationCandidate(
	_ context.Context,
	request ReconciliationWorkspaceRequest,
) (WorkspaceSnapshot, error) {
	return inspector.observe(request)
}

func (inspector *taskReconciliationInspector) PromoteReconciliationCandidate(
	_ context.Context,
	request ReconciliationWorkspaceRequest,
) (WorkspaceSnapshot, error) {
	return inspector.observe(request)
}

func (inspector *taskReconciliationInspector) observe(request ReconciliationWorkspaceRequest) (WorkspaceSnapshot, error) {
	inspector.calls++
	inspector.request = request
	return inspector.snapshot, inspector.err
}
