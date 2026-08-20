package sqlite

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/comisai/comis-dev-crew/internal/application"
)

func TestInitiativeControlResultSurvivesRestartAndRejectsAlteredReplay(t *testing.T) {
	ctx := context.Background()
	store, initiativeHandle, activation := preparedInitiativeActivationStore(t)
	activated, err := store.CommitInitiativeActivation(ctx, activation)
	if err != nil {
		t.Fatalf("CommitInitiativeActivation() error = %v", err)
	}
	members := make([]application.InitiativeControlMemberResult, 0, len(activated.Tasks))
	for _, task := range activated.Tasks {
		members = append(members, application.InitiativeControlMemberResult{
			TaskHandle: task.Handle, OperationID: "pause-member-" + task.Handle,
			Outcome: application.InitiativeControlCompleted,
			State:   task.State, StateVersion: task.StateVersion,
		})
	}
	mutation := application.InitiativeControlMutation{
		OperationID: "operation-control-initiative", Command: "PauseInitiative",
		SubjectDigest: strings.Repeat("a", 64), At: activation.At,
		Result: application.InitiativeControlResult{
			InitiativeHandle: initiativeHandle, State: activated.Initiative.State,
			StateVersion: activated.Initiative.StateVersion,
			Members:      members,
		},
	}
	committed, err := store.CommitInitiativeControl(ctx, mutation)
	if err != nil {
		t.Fatalf("CommitInitiativeControl() error = %v", err)
	}
	replayed, found, err := store.ReplayInitiativeControl(
		ctx, mutation.OperationID, mutation.Command, mutation.SubjectDigest,
	)
	if err != nil || !found || !reflect.DeepEqual(replayed, committed) {
		t.Fatalf("ReplayInitiativeControl() = %#v, %t, %v, want %#v", replayed, found, err, committed)
	}
	if _, _, err := store.ReplayInitiativeControl(
		ctx, mutation.OperationID, mutation.Command, strings.Repeat("b", 64),
	); err == nil {
		t.Fatal("ReplayInitiativeControl(altered) error = nil")
	}
}
