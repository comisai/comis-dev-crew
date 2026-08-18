package application

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/comisai/comis-dev-crew/internal/domain"
)

func TestInterventions_HandbackInspectsExactPausedWorkspaceBeforeCommit(t *testing.T) {
	now := time.Date(2026, time.August, 11, 19, 0, 0, 0, time.UTC)
	task := queryTask("task-handback-application", domain.TaskPaused, 12)
	store := &interventionStore{
		task: task,
		preparation: ManagedRunPreparation{
			ExternalRunRef: task.Handle, RegistrationNonce: "registration-nonce-handback",
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
		WorktreePath: store.preparation.RequestedWorkspaceRoot, Branch: "devcrew/task-handback-application",
		HeadRevision: strings.Repeat("b", 40), Cleanliness: WorkspaceClean,
	}}
	interventions, err := NewInterventions(InterventionConfig{Store: store, Workspaces: inspector, Clock: func() time.Time { return now }})
	if err != nil {
		t.Fatalf("NewInterventions() error = %v", err)
	}
	command := HandbackTaskCommand{
		OperationID: "operation-handback-application", TaskHandle: task.Handle,
		Action: HandbackValidateDeveloperWork,
	}
	if _, err := interventions.HandbackTask(context.Background(), command); err != nil {
		t.Fatalf("HandbackTask() error = %v", err)
	}
	wantRequest := WorkspaceSnapshotRequest{
		TaskHandle: task.Handle, RepositoryID: task.RepositoryID,
		WorktreePath: store.preparation.RequestedWorkspaceRoot,
	}
	if inspector.calls != 1 || inspector.request != wantRequest {
		t.Fatalf("workspace inspection = %d/%#v, want %#v", inspector.calls, inspector.request, wantRequest)
	}
	if store.commitCalls != 1 || store.mutation.TaskHandle != task.Handle || store.mutation.Action != command.Action ||
		store.mutation.Snapshot != inspector.snapshot || store.mutation.At != now || len(store.mutation.SubjectDigest) != 64 {
		t.Fatalf("handback mutation = %#v", store.mutation)
	}
	if store.mutation.CandidateReport.LocalReportID != command.OperationID ||
		store.mutation.CandidateReport.BriefRevision != task.BriefRevision ||
		store.mutation.CandidateReport.BriefRevisionHash != task.BriefRevisionHash ||
		store.mutation.CandidateReport.Kind != domain.ReportCandidateComplete ||
		len(store.mutation.CandidateReportDigest) != 64 {
		t.Fatalf("handback candidate report = %#v", store.mutation.CandidateReport)
	}
}

func TestInterventions_HandbackReplaysBeforeInspectionAndRejectsUnsafeInputs(t *testing.T) {
	task := queryTask("task-handback-replay", domain.TaskPaused, 13)
	replay := MutationResult{Task: task, Operation: domain.OperationRecord{
		SchemaVersion: 1, ID: "operation-handback-replay", Command: "HandbackTask",
		SubjectDigest: strings.Repeat("a", 64), Status: domain.OperationCompleted,
		ResultRef: task.Handle, StateVersion: 13, CreatedAt: task.UpdatedAt, UpdatedAt: task.UpdatedAt,
	}}
	store := &interventionStore{task: task, replay: replay, replayFound: true}
	inspector := &interventionInspector{}
	interventions, err := NewInterventions(InterventionConfig{Store: store, Workspaces: inspector, Clock: func() time.Time { return task.UpdatedAt }})
	if err != nil {
		t.Fatal(err)
	}
	command := HandbackTaskCommand{
		OperationID: replay.Operation.ID, TaskHandle: task.Handle, Action: HandbackValidateDeveloperWork,
	}
	got, err := interventions.HandbackTask(context.Background(), command)
	if err != nil || !reflect.DeepEqual(got, replay) || inspector.calls != 0 || store.commitCalls != 0 {
		t.Fatalf("HandbackTask(replay) = %#v, %v, inspect=%d commit=%d", got, err, inspector.calls, store.commitCalls)
	}
	command.Action = HandbackAction("invented")
	if _, err := interventions.HandbackTask(context.Background(), command); err == nil {
		t.Fatal("HandbackTask(invalid action) error = nil")
	}
	//lint:ignore SA1012 The application boundary explicitly rejects a nil context.
	if _, err := interventions.HandbackTask(nil, HandbackTaskCommand{}); err == nil {
		t.Fatal("HandbackTask(nil) error = nil")
	}
	if _, err := NewInterventions(InterventionConfig{}); err == nil {
		t.Fatal("NewInterventions(incomplete) error = nil")
	}
}

type interventionStore struct {
	task         domain.Task
	preparation  ManagedRunPreparation
	mutation     TaskHandbackMutation
	resume       TaskResumeMutation
	replace      TaskReplaceMutation
	replaceCalls int
	resumeCalls  int
	replay       MutationResult
	replayFound  bool
	replayErr    error
	commitErr    error
	commitCalls  int
}

func (store *interventionStore) ReplayMutation(context.Context, string, string, string) (MutationResult, bool, error) {
	return store.replay, store.replayFound, store.replayErr
}

func (store *interventionStore) GetTask(context.Context, string) (domain.Task, error) {
	if store.task.Handle == "" {
		return domain.Task{}, ErrNotFound
	}
	return store.task, nil
}

func (store *interventionStore) GetManagedRunPreparation(context.Context, string) (ManagedRunPreparation, error) {
	return store.preparation, nil
}

func (store *interventionStore) CommitTaskHandback(_ context.Context, mutation TaskHandbackMutation) (MutationResult, error) {
	store.commitCalls++
	store.mutation = mutation
	if store.commitErr != nil {
		return MutationResult{}, store.commitErr
	}
	return MutationResult{Task: store.task}, nil
}

func (store *interventionStore) CommitTaskReplace(_ context.Context, mutation TaskReplaceMutation) (MutationResult, error) {
	store.replaceCalls++
	store.replace = mutation
	if store.commitErr != nil {
		return MutationResult{}, store.commitErr
	}
	replaced := store.task
	replaced.State = domain.TaskReady
	replaced.WorkerProfileID = mutation.WorkerProfileID
	replaced.BriefRevision = store.task.BriefRevision + 1
	return MutationResult{Task: replaced}, nil
}

func (store *interventionStore) CommitTaskResume(_ context.Context, mutation TaskResumeMutation) (MutationResult, error) {
	store.resumeCalls++
	store.resume = mutation
	if store.commitErr != nil {
		return MutationResult{}, store.commitErr
	}
	resumed := store.task
	resumed.State = domain.TaskWorking
	return MutationResult{Task: resumed}, nil
}

type interventionInspector struct {
	request  WorkspaceSnapshotRequest
	snapshot WorkspaceSnapshot
	err      error
	calls    int
}

func (inspector *interventionInspector) InspectWorkspace(_ context.Context, request WorkspaceSnapshotRequest) (WorkspaceSnapshot, error) {
	inspector.calls++
	inspector.request = request
	if inspector.err != nil {
		return WorkspaceSnapshot{}, inspector.err
	}
	return inspector.snapshot, nil
}
