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
	unavailable, err := domain.NewFailure(
		domain.ErrorUnavailable, true, "worker is unavailable", "retry after recovery", errors.New("offline"),
	)
	if err != nil {
		t.Fatal(err)
	}
	tasks := &initiativeTaskControlsStub{pauseErrors: map[string]error{
		"task-control-b": ErrPrecondition,
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

func TestInitiativeResumeAndCancelUseTheExistingPerTaskControls(t *testing.T) {
	for _, test := range []struct {
		name  string
		run   func(*InitiativeControls) (InitiativeControlResult, error)
		calls func(*initiativeTaskControlsStub, *initiativeTaskResumerStub) int
	}{
		{
			name: "resume",
			run: func(controls *InitiativeControls) (InitiativeControlResult, error) {
				return controls.ResumeInitiative(context.Background(), InitiativeControlCommand{
					OperationID: "operation-resume-initiative", InitiativeHandle: "initiative-control",
				})
			},
			calls: func(_ *initiativeTaskControlsStub, resumer *initiativeTaskResumerStub) int {
				return len(resumer.calls)
			},
		},
		{
			name: "cancel",
			run: func(controls *InitiativeControls) (InitiativeControlResult, error) {
				return controls.CancelInitiative(context.Background(), InitiativeControlCommand{
					OperationID: "operation-cancel-initiative", InitiativeHandle: "initiative-control",
				})
			},
			calls: func(tasks *initiativeTaskControlsStub, _ *initiativeTaskResumerStub) int {
				return len(tasks.cancelCalls)
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			store := &initiativeControlStoreStub{
				initiative: initiativeControlFixture(),
				tasks: []domain.Task{
					{Handle: "task-control-a", State: domain.TaskPaused, StateVersion: 4},
					{Handle: "task-control-b", State: domain.TaskPaused, StateVersion: 4},
					{Handle: "task-control-c", State: domain.TaskPaused, StateVersion: 4},
				},
				stateVersion: 4,
			}
			tasks := &initiativeTaskControlsStub{}
			resumer := &initiativeTaskResumerStub{}
			controls, err := NewInitiativeControls(InitiativeControlConfig{
				Store: store, Tasks: tasks, Resumer: resumer, Clock: initiativeControlClock,
			})
			if err != nil {
				t.Fatal(err)
			}
			result, err := test.run(controls)
			if err != nil || len(result.Members) != 3 {
				t.Fatalf("initiative %s = %#v, %v", test.name, result, err)
			}
			if test.calls(tasks, resumer) != 3 {
				t.Fatalf("initiative %s task calls = %d, want 3", test.name, test.calls(tasks, resumer))
			}
		})
	}
}

func TestInitiativeControlsRejectInvalidCompositionAndUnavailableResume(t *testing.T) {
	if _, err := NewInitiativeControls(InitiativeControlConfig{}); err == nil {
		t.Fatal("NewInitiativeControls(empty) error = nil")
	}
	store := &initiativeControlStoreStub{initiative: initiativeControlFixture()}
	controls, err := NewInitiativeControls(InitiativeControlConfig{
		Store: store, Tasks: &initiativeTaskControlsStub{}, Clock: initiativeControlClock,
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = controls.ResumeInitiative(context.Background(), InitiativeControlCommand{
		OperationID: "operation-resume-unavailable", InitiativeHandle: store.initiative.Handle,
	})
	var failure *domain.Failure
	if !errors.As(err, &failure) || failure.Code != domain.ErrorUnavailable || !failure.Retryable {
		t.Fatalf("ResumeInitiative(unavailable) error = %#v", err)
	}
	if _, err := controls.CancelInitiative(context.Background(), InitiativeControlCommand{
		OperationID: "bad id", InitiativeHandle: store.initiative.Handle,
	}); err == nil {
		t.Fatal("CancelInitiative(invalid operation) error = nil")
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
	cancelCalls []CancelTaskCommand
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
	_ context.Context,
	command CancelTaskCommand,
) (MutationResult, error) {
	controls.cancelCalls = append(controls.cancelCalls, command)
	return MutationResult{Task: domain.Task{
		Handle: command.TaskHandle, State: domain.TaskCancelled, StateVersion: 5,
	}}, nil
}

type initiativeTaskResumerStub struct {
	calls []ResumeTaskCommand
}

func (resumer *initiativeTaskResumerStub) ResumeTask(
	_ context.Context,
	command ResumeTaskCommand,
) (MutationResult, error) {
	resumer.calls = append(resumer.calls, command)
	return MutationResult{Task: domain.Task{
		Handle: command.TaskHandle, State: domain.TaskWorking, StateVersion: 5,
	}}, nil
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
