package localapi

import (
	"context"
	"testing"

	"github.com/comisai/comis-dev-crew/internal/application"
)

func (stub *stubDecisionWithdrawal) RespondDecision(
	_ context.Context,
	command application.RespondDecisionCommand,
) (application.MutationResult, error) {
	stub.calls = append(stub.calls, command.OperationID+":"+command.TaskHandle+":"+command.ExternalKey+":"+command.Response)
	return stub.result, stub.err
}

// Answering a question is the operator console's authority. The model facade
// raises questions and applies answers; it does not get to supply them, or a
// worker could satisfy its own decision hold without a human ever replying.
func TestRespondDecisionMethod_IsAnOperatorOnlyMutation(t *testing.T) {
	if !MethodRespondDecision.valid() || MethodRespondDecision.SideEffect() != SideEffectMutate {
		t.Fatalf("RespondDecision method = %q, side effect = %q", MethodRespondDecision, MethodRespondDecision.SideEffect())
	}
	if !MethodRespondDecision.operatorOnly() {
		t.Error("answering a question is reachable from the model facade")
	}
}

func TestHandler_RoutesAnOperatorAnswerToTheDecisionAuthority(t *testing.T) {
	authority := &stubDecisionWithdrawal{result: cancelResultFixture()}
	client := newCancelClient(t, CallerOperatorCLI, authority)
	if _, err := client.RespondDecision(context.Background(), "respond-decision-0001", RespondDecisionInput{
		TaskHandle: "task-0001", ExternalKey: "schema-choice", Response: "use the versioned schema",
	}); err != nil {
		t.Fatalf("RespondDecision() error = %v", err)
	}
	if len(authority.calls) != 1 ||
		authority.calls[0] != "respond-decision-0001:task-0001:schema-choice:use the versioned schema" {
		t.Fatalf("decision authority calls = %#v", authority.calls)
	}
}

func TestHandler_RefusesAnAnswerFromTheModelFacade(t *testing.T) {
	client := newCancelClient(t, CallerMCPFacade, &stubDecisionWithdrawal{result: cancelResultFixture()})
	if _, err := client.RespondDecision(context.Background(), "respond-decision-0002", RespondDecisionInput{
		TaskHandle: "task-0001", ExternalKey: "schema-choice", Response: "use the versioned schema",
	}); err == nil {
		t.Error("RespondDecision(model facade) error = nil")
	}
}
