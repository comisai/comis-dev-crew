package sqlite

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/comisai/comis-dev-crew/internal/application"
	"github.com/comisai/comis-dev-crew/internal/domain"
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
	if _, err := reopened.db.ExecContext(ctx,
		"DELETE FROM initiative_group_control_members WHERE operation_id = ? AND ordinal = 0",
		mutation.OperationID,
	); err != nil {
		t.Fatalf("delete one durable member result: %v", err)
	}
	if _, _, err := reopened.ReplayInitiativeControl(
		ctx, mutation.OperationID, mutation.Command, mutation.SubjectDigest,
	); err == nil {
		t.Fatal("ReplayInitiativeControl(missing durable member) error = nil")
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
		"invalid initiative": func(result *application.InitiativeControlResult) {
			result.InitiativeHandle = "bad handle"
		},
		"invalid task": func(result *application.InitiativeControlResult) {
			result.Members[0].TaskHandle = "bad handle"
		},
		"unknown outcome": func(result *application.InitiativeControlResult) {
			result.Members[0].Outcome = "invented"
		},
		"completed with error": func(result *application.InitiativeControlResult) {
			result.Members[0].ErrorCode = "unknown"
		},
		"duplicate member": func(result *application.InitiativeControlResult) {
			result.Members = append(result.Members, result.Members[0])
		},
		"duplicate operation": func(result *application.InitiativeControlResult) {
			second := result.Members[0]
			second.TaskHandle = "task-validation-second"
			result.Members = append(result.Members, second)
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

func TestInitiativeControlStoreReportsClosedDatabaseFaults(t *testing.T) {
	ctx := context.Background()
	store, _, _ := preparedInitiativeActivationStore(t)
	if _, err := readInitiativeControlResult(ctx, store.db, domain.OperationRecord{ID: "operation-control-missing"}); err == nil || !strings.Contains(err.Error(), "read initiative control result") {
		t.Fatalf("readInitiativeControlResult(missing) error = %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	result := application.InitiativeControlResult{
		InitiativeHandle: "initiative-closed-store", State: "active", StateVersion: 3,
		Members: []application.InitiativeControlMemberResult{{
			TaskHandle: "task-closed-store", OperationID: "operation-closed-member",
			Outcome: application.InitiativeControlCompleted, State: "working", StateVersion: 3,
		}},
	}
	mutation := application.InitiativeControlMutation{
		OperationID: "operation-closed-store", Command: "PauseInitiative",
		SubjectDigest: strings.Repeat("a", 64), Result: result, At: time.Now().UTC(),
	}
	if _, _, err := store.ReplayInitiativeControl(
		ctx, mutation.OperationID, mutation.Command, mutation.SubjectDigest,
	); err == nil || !strings.Contains(err.Error(), "begin initiative control replay") {
		t.Fatalf("ReplayInitiativeControl(closed) error = %v", err)
	}
	if _, err := store.CommitInitiativeControl(ctx, mutation); err == nil || !strings.Contains(err.Error(), "begin initiative control") {
		t.Fatalf("CommitInitiativeControl(closed) error = %v", err)
	}
}

func TestInitiativeControlCommitRollsBackInjectedPersistenceFaults(t *testing.T) {
	ctx := context.Background()
	store, mutation := preparedInitiativeControlMutation(t)

	stale := mutation
	stale.OperationID = "operation-control-stale-snapshot"
	stale.SubjectDigest = strings.Repeat("b", 64)
	stale.Result.StateVersion--
	if _, err := store.CommitInitiativeControl(ctx, stale); err == nil || !strings.Contains(err.Error(), "snapshot changed") {
		t.Fatalf("CommitInitiativeControl(stale snapshot) error = %v", err)
	}

	if _, err := store.db.ExecContext(ctx, `CREATE TRIGGER refuse_initiative_control_result
		BEFORE INSERT ON initiative_group_controls
		BEGIN SELECT RAISE(ABORT, 'injected control result failure'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CommitInitiativeControl(ctx, mutation); err == nil || !strings.Contains(err.Error(), "insert initiative control result") {
		t.Fatalf("CommitInitiativeControl(result fault) error = %v", err)
	}
	if _, err := store.GetOperation(ctx, mutation.OperationID); err == nil {
		t.Fatal("result fault retained the rolled-back operation")
	}
	if _, err := store.db.ExecContext(ctx, "DROP TRIGGER refuse_initiative_control_result"); err != nil {
		t.Fatal(err)
	}

	memberFault := mutation
	memberFault.OperationID = "operation-control-member-fault"
	memberFault.SubjectDigest = strings.Repeat("c", 64)
	if _, err := store.db.ExecContext(ctx, `CREATE TRIGGER refuse_initiative_control_member
		BEFORE INSERT ON initiative_group_control_members
		BEGIN SELECT RAISE(ABORT, 'injected control member failure'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CommitInitiativeControl(ctx, memberFault); err == nil || !strings.Contains(err.Error(), "insert initiative control member") {
		t.Fatalf("CommitInitiativeControl(member fault) error = %v", err)
	}
	if _, err := store.GetOperation(ctx, memberFault.OperationID); err == nil {
		t.Fatal("member fault retained the rolled-back operation")
	}
}

func preparedInitiativeControlMutation(t *testing.T) (*Store, application.InitiativeControlMutation) {
	t.Helper()
	ctx := context.Background()
	store, initiativeHandle, activation := preparedInitiativeActivationStore(t)
	activated, err := store.CommitInitiativeActivation(ctx, activation)
	if err != nil {
		t.Fatal(err)
	}
	for index, task := range activated.Tasks {
		if _, err := store.CommitTaskCancel(ctx, application.TaskCancelMutation{
			TaskHandle: task.Handle, OperationID: "control-fixture-" + task.Handle,
			SubjectDigest: strings.Repeat(string(rune('d'+index)), 64),
			At:            activation.At.Add(time.Duration(index+1) * time.Minute),
		}); err != nil {
			t.Fatal(err)
		}
	}
	initiative, tasks, stateVersion, err := store.InitiativeObservation(ctx, initiativeHandle)
	if err != nil {
		t.Fatal(err)
	}
	members := make([]application.InitiativeControlMemberResult, 0, len(tasks))
	for _, task := range tasks {
		members = append(members, application.InitiativeControlMemberResult{
			TaskHandle: task.Handle, OperationID: "control-fixture-" + task.Handle,
			Outcome: application.InitiativeControlCompleted,
			State:   task.State, StateVersion: task.StateVersion,
		})
	}
	return store, application.InitiativeControlMutation{
		OperationID: "operation-control-result-fault", Command: "CancelInitiative",
		SubjectDigest: strings.Repeat("a", 64), At: activation.At.Add(3 * time.Minute),
		Result: application.InitiativeControlResult{
			InitiativeHandle: initiativeHandle, State: initiative.State,
			StateVersion: stateVersion, Members: members,
		},
	}
}
