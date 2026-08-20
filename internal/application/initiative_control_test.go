package application

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/comisai/comis-dev-crew/internal/domain"
)

func TestInitiativePauseReportsEveryMemberAndReplaysTheExactGroupResult(t *testing.T) {
	store := &initiativeControlStoreStub{
		initiative: initiativeControlFixture(),
		tasks: []domain.Task{
			{Handle: "task-control-c", State: domain.TaskWorking, StateVersion: 4},
			{Handle: "task-control-a", State: domain.TaskWorking, StateVersion: 4},
			{Handle: "task-control-b", State: domain.TaskWorking, StateVersion: 4},
		},
		stateVersion: 4,
	}
	precondition, err := domain.NewFailure(
		domain.ErrorPrecondition, false, "task cannot pause", "inspect the task", ErrPrecondition,
	)
	if err != nil {
		t.Fatal(err)
	}
	unavailable, err := domain.NewFailure(
		domain.ErrorUnavailable, true, "worker is unavailable", "retry after recovery", errors.New("offline"),
	)
	if err != nil {
		t.Fatal(err)
	}
	tasks := &initiativeTaskControlsStub{pauseErrors: map[string]error{
		"task-control-b": precondition,
		"task-control-c": unavailable,
	}}
	controls, err := NewInitiativeControls(InitiativeControlConfig{
		Store: store, Tasks: tasks, Clock: initiativeControlClock,
	})
	if err != nil {
		t.Fatalf("NewInitiativeControls() error = %v", err)
	}
	command := InitiativeControlCommand{
		OperationID: "operation-pause-initiative", InitiativeHandle: store.initiative.Handle,
	}
	result, err := controls.PauseInitiative(context.Background(), command)
	if err != nil {
		t.Fatalf("PauseInitiative() error = %v", err)
	}
	if len(result.Members) != 3 || result.Members[0].TaskHandle != "task-control-a" ||
		result.Members[0].Outcome != InitiativeControlCompleted ||
		result.Members[1].Outcome != InitiativeControlRejected || result.Members[1].ErrorCode != domain.ErrorPrecondition ||
		result.Members[2].Outcome != InitiativeControlUnknown || result.Members[2].ErrorCode != domain.ErrorUnavailable {
		t.Fatalf("PauseInitiative() members = %#v", result.Members)
	}
	for _, member := range result.Members {
		if domain.ValidateOperationID(member.OperationID) != nil || member.OperationID == command.OperationID {
			t.Fatalf("member operation ID = %q", member.OperationID)
		}
	}
	if store.commits != 1 || len(tasks.pauseCalls) != 3 {
		t.Fatalf("commits/pause calls = %d/%#v", store.commits, tasks.pauseCalls)
	}

	replayed, err := controls.PauseInitiative(context.Background(), command)
	if err != nil || !reflect.DeepEqual(replayed, result) {
		t.Fatalf("PauseInitiative(replay) = %#v, %v, want %#v", replayed, err, result)
	}
	if store.commits != 1 || len(tasks.pauseCalls) != 3 {
		t.Fatalf("replay repeated effects: commits/pause calls = %d/%#v", store.commits, tasks.pauseCalls)
	}
}

type initiativeControlStoreStub struct {
	initiative   domain.DevelopmentInitiative
	tasks        []domain.Task
	stateVersion int64
	replay       *InitiativeControlResult
	commits      int
}

func (store *initiativeControlStoreStub) ReplayInitiativeControl(
	_ context.Context,
	_, _, _ string,
) (InitiativeControlResult, bool, error) {
	if store.replay == nil {
		return InitiativeControlResult{}, false, nil
	}
	return *store.replay, true, nil
}

func (store *initiativeControlStoreStub) InitiativeObservation(
	context.Context,
	string,
) (domain.DevelopmentInitiative, []domain.Task, int64, error) {
	return store.initiative, append([]domain.Task(nil), store.tasks...), store.stateVersion, nil
}

func (store *initiativeControlStoreStub) CommitInitiativeControl(
	_ context.Context,
	mutation InitiativeControlMutation,
) (InitiativeControlResult, error) {
	store.commits++
	result := mutation.Result
	store.replay = &result
	return result, nil
}

type initiativeTaskControlsStub struct {
	pauseCalls  []PauseTaskCommand
	pauseErrors map[string]error
}

func (controls *initiativeTaskControlsStub) PauseTask(
	_ context.Context,
	command PauseTaskCommand,
) (MutationResult, error) {
	controls.pauseCalls = append(controls.pauseCalls, command)
	if err := controls.pauseErrors[command.TaskHandle]; err != nil {
		return MutationResult{}, err
	}
	return MutationResult{Task: domain.Task{
		Handle: command.TaskHandle, State: domain.TaskWorking, StateVersion: 5,
	}}, nil
}

func (controls *initiativeTaskControlsStub) CancelTask(
	context.Context,
	CancelTaskCommand,
) (MutationResult, error) {
	return MutationResult{}, errors.New("unexpected cancel")
}

func initiativeControlFixture() domain.DevelopmentInitiative {
	return domain.DevelopmentInitiative{
		SchemaVersion: 1, Handle: "initiative-control", ManagedRunGroupID: "managed-run-group-control",
		TitleRef: "title-control", State: domain.InitiativeActive,
		BaseRevisionSet: []domain.InitiativeBaseRevision{{RepositoryID: "repo-control", Revision: "0123456789abcdef0123456789abcdef01234567"}},
		Components: []domain.InitiativeComponent{{
			ComponentHandle: "component-control", RepositoryID: "repo-control",
			ResponsibilityRef: "responsibility-control",
			TaskHandles:       []string{"task-control-a", "task-control-b", "task-control-c"},
		}},
		IntegrationPolicyID: "integration-policy-control", IntegrationOwnerTask: "task-control-c",
		StateVersion: 4, CreatedAt: initiativeControlClock(), UpdatedAt: initiativeControlClock(),
	}
}

func initiativeControlClock() time.Time {
	return time.Date(2026, time.August, 20, 18, 0, 0, 0, time.UTC)
}
