package sqlite

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/comisai/comis-dev-crew/internal/application"
)

func TestInitiativeControlResultSurvivesRestartAndRejectsAlteredReplay(t *testing.T) {
	ctx := context.Background()
	store, initiativeHandle, activation := preparedInitiativeActivationStore(t)
	activated, err := store.CommitInitiativeActivation(ctx, activation)
	if err != nil {
		t.Fatalf("CommitInitiativeActivation() error = %v", err)
	}
	for index, task := range activated.Tasks {
		if _, err := store.CommitTaskCancel(ctx, application.TaskCancelMutation{
			TaskHandle: task.Handle, OperationID: "cancel-member-" + task.Handle,
			SubjectDigest: strings.Repeat(string(rune('b'+index)), 64),
			At:            activation.At.Add(time.Duration(index+1) * time.Minute),
		}); err != nil {
			t.Fatalf("CommitTaskCancel(%q) error = %v", task.Handle, err)
		}
	}
	initiative, currentTasks, stateVersion, err := store.InitiativeObservation(ctx, initiativeHandle)
	if err != nil {
		t.Fatalf("InitiativeObservation() error = %v", err)
	}
	members := make([]application.InitiativeControlMemberResult, 0, len(currentTasks))
	for _, task := range currentTasks {
		members = append(members, application.InitiativeControlMemberResult{
			TaskHandle: task.Handle, OperationID: "cancel-member-" + task.Handle,
			Outcome: application.InitiativeControlCompleted,
			State:   task.State, StateVersion: task.StateVersion,
		})
	}
	mutation := application.InitiativeControlMutation{
		OperationID: "operation-control-initiative", Command: "CancelInitiative",
		SubjectDigest: strings.Repeat("a", 64), At: activation.At.Add(3 * time.Minute),
		Result: application.InitiativeControlResult{
			InitiativeHandle: initiativeHandle, State: initiative.State,
			StateVersion: stateVersion,
			Members:      members,
		},
	}
	if _, found, err := store.ReplayInitiativeControl(
		ctx, mutation.OperationID, mutation.Command, mutation.SubjectDigest,
	); err != nil || found {
		t.Fatalf("ReplayInitiativeControl(before commit) found/error = %t/%v", found, err)
	}
	committed, err := store.CommitInitiativeControl(ctx, mutation)
	if err != nil {
		t.Fatalf("CommitInitiativeControl() error = %v", err)
	}
	committedReplay, err := store.CommitInitiativeControl(ctx, mutation)
	if err != nil || !reflect.DeepEqual(committedReplay, committed) {
		t.Fatalf("CommitInitiativeControl(replay) = %#v, %v, want %#v", committedReplay, err, committed)
	}
	var databasePath string
	if err := store.db.QueryRowContext(ctx,
		"SELECT file FROM pragma_database_list WHERE name = 'main'",
	).Scan(&databasePath); err != nil {
		t.Fatalf("read database path: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	reopened, err := Open(ctx, databasePath)
	if err != nil {
		t.Fatalf("Open(restart) error = %v", err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	replayed, found, err := reopened.ReplayInitiativeControl(
		ctx, mutation.OperationID, mutation.Command, mutation.SubjectDigest,
	)
	if err != nil || !found || !reflect.DeepEqual(replayed, committed) {
		t.Fatalf("ReplayInitiativeControl() = %#v, %t, %v, want %#v", replayed, found, err, committed)
	}
	if _, _, err := reopened.ReplayInitiativeControl(
		ctx, mutation.OperationID, mutation.Command, strings.Repeat("b", 64),
	); err == nil {
		t.Fatal("ReplayInitiativeControl(altered) error = nil")
	}
}

func TestInitiativeControlStoreRejectsACompletedMemberWithoutItsTaskOperation(t *testing.T) {
	ctx := context.Background()
	store, initiativeHandle, activation := preparedInitiativeActivationStore(t)
	activated, err := store.CommitInitiativeActivation(ctx, activation)
	if err != nil {
		t.Fatal(err)
	}
	members := make([]application.InitiativeControlMemberResult, 0, len(activated.Tasks))
	for _, task := range activated.Tasks {
		members = append(members, application.InitiativeControlMemberResult{
			TaskHandle: task.Handle, OperationID: "missing-member-" + task.Handle,
			Outcome: application.InitiativeControlCompleted,
			State:   task.State, StateVersion: task.StateVersion,
		})
	}
	mutation := application.InitiativeControlMutation{
		OperationID: "operation-control-missing-member", Command: "PauseInitiative",
		SubjectDigest: strings.Repeat("e", 64), At: activation.At.Add(time.Minute),
		Result: application.InitiativeControlResult{
			InitiativeHandle: initiativeHandle, State: activated.Initiative.State,
			StateVersion: activated.Initiative.StateVersion, Members: members,
		},
	}
	if _, err := store.CommitInitiativeControl(ctx, mutation); err == nil {
		t.Fatal("CommitInitiativeControl(missing member operation) error = nil")
	}
	if _, err := store.GetOperation(ctx, mutation.OperationID); err == nil {
		t.Fatal("refused initiative control wrote its group operation")
	}
}

func TestInitiativeControlValidationRejectsForgedResultShapes(t *testing.T) {
	valid := application.InitiativeControlResult{
		InitiativeHandle: "initiative-validation", State: "active", StateVersion: 3,
		Members: []application.InitiativeControlMemberResult{{
			TaskHandle: "task-validation", OperationID: "operation-validation-member",
			Outcome: application.InitiativeControlCompleted, State: "working", StateVersion: 3,
		}},
	}
	for name, mutate := range map[string]func(*application.InitiativeControlResult){
		"unknown outcome": func(result *application.InitiativeControlResult) {
			result.Members[0].Outcome = "invented"
		},
		"completed with error": func(result *application.InitiativeControlResult) {
			result.Members[0].ErrorCode = "unknown"
		},
		"duplicate member": func(result *application.InitiativeControlResult) {
			result.Members = append(result.Members, result.Members[0])
		},
		"unordered member": func(result *application.InitiativeControlResult) {
			second := result.Members[0]
			second.TaskHandle, second.OperationID = "task-alpha", "operation-alpha-member"
			result.Members = append(result.Members, second)
		},
	} {
		t.Run(name, func(t *testing.T) {
			result := valid
			result.Members = append([]application.InitiativeControlMemberResult(nil), valid.Members...)
			mutate(&result)
			if err := validateInitiativeControlResult(result); err == nil {
				t.Fatalf("validateInitiativeControlResult(%s) error = nil", name)
			}
		})
	}
	for command, want := range map[string]string{
		"PauseInitiative": "PauseTask", "ResumeInitiative": "ResumeTask",
		"CancelInitiative": "CancelTask", "invented": "",
	} {
		if got := initiativeMemberCommand(command); got != want {
			t.Fatalf("initiativeMemberCommand(%q) = %q, want %q", command, got, want)
		}
	}
	if err := validateInitiativeControlMutation(application.InitiativeControlMutation{
		OperationID: "operation-validation", Command: "invented",
		SubjectDigest: strings.Repeat("f", 64), At: time.Now().UTC(), Result: valid,
	}); err == nil {
		t.Fatal("validateInitiativeControlMutation(invented command) error = nil")
	}
	if !validInitiativeControlOutcome(application.InitiativeControlNotAttempted, "unknown") {
		t.Fatal("not-attempted control outcome was rejected")
	}
}
