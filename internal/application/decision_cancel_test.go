package application

import (
	"context"
	"testing"
)

func TestMutations_CancelDecisionCommitsOnlyAValidReference(t *testing.T) {
	store := &mutationStore{}
	mutations := respondMutations(t, store)
	result, err := mutations.CancelDecision(context.Background(), CancelDecisionCommand{
		OperationID: "operation-cancel-decision", TaskHandle: "task-decision-0001",
		ExternalKey: "schema-choice",
	})
	if err != nil {
		t.Fatalf("CancelDecision() error = %v", err)
	}
	if store.cancelDecision.OperationID != "operation-cancel-decision" ||
		store.cancelDecision.TaskHandle != "task-decision-0001" ||
		store.cancelDecision.ExternalKey != "schema-choice" || store.cancelDecision.At.IsZero() {
		t.Fatalf("committed cancellation = %#v", store.cancelDecision)
	}
	if result.Operation.ID != "operation-cancel-decision" {
		t.Fatalf("CancelDecision() result = %#v", result)
	}

	refused := &mutationStore{}
	if _, err := respondMutations(t, refused).CancelDecision(context.Background(), CancelDecisionCommand{
		OperationID: "operation-cancel-invalid", TaskHandle: "task-decision-0001",
		ExternalKey: "not a key",
	}); err == nil {
		t.Fatal("CancelDecision(invalid key) error = nil")
	}
	if refused.cancelDecision.OperationID != "" {
		t.Fatalf("invalid cancellation reached store: %#v", refused.cancelDecision)
	}
}

func TestDecisionStatusValidAcceptsOnlyClosedValues(t *testing.T) {
	for _, status := range []DecisionStatus{DecisionAwaitingHost, DecisionAwaitingHuman} {
		if !status.Valid() {
			t.Fatalf("DecisionStatus(%q).Valid() = false", status)
		}
	}
	if DecisionStatus("invented").Valid() {
		t.Fatal("DecisionStatus(invented).Valid() = true")
	}
}
