package localapi

import (
	"context"
	"errors"
	"net"
	"path/filepath"
	"testing"
	"time"

	"github.com/comisai/comis-dev-crew/internal/application"
	"github.com/comisai/comis-dev-crew/internal/domain"
)

type stubDecisionWithdrawal struct {
	calls  []string
	result application.MutationResult
	err    error
}

func (stub *stubDecisionWithdrawal) CancelDecision(
	_ context.Context,
	command application.CancelDecisionCommand,
) (application.MutationResult, error) {
	stub.calls = append(stub.calls, command.OperationID+":"+command.TaskHandle+":"+command.ExternalKey)
	return stub.result, stub.err
}

func newCancelClient(t *testing.T, caller CallerClass, withdrawal DecisionWithdrawal) *Client {
	t.Helper()
	handler, err := NewHandler(HandlerConfig{
		Queries: &apiQueries{}, Decisions: withdrawal, Clock: time.Now,
	})
	if err != nil {
		t.Fatalf("NewHandler() error = %v", err)
	}
	socketPath := filepath.Join(canonicalTempDir(t), "runtime", "devcrew.sock")
	server, err := Listen(socketPath, caller, handler)
	if err != nil {
		t.Fatalf("Listen() error = %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- server.Serve(ctx) }()
	t.Cleanup(func() {
		cancel()
		if err := server.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
			t.Errorf("Server.Close() error = %v", err)
		}
		if err := <-done; err != nil {
			t.Errorf("Server.Serve() error = %v", err)
		}
	})
	client, err := NewClient(socketPath, 2*time.Second)
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	return client
}

func cancelResultFixture() application.MutationResult {
	return application.MutationResult{
		Task: domain.Task{Handle: "task-0001", State: domain.TaskWorking, StateVersion: 6},
		Operation: domain.OperationRecord{
			ID: "cancel-decision-0001", Command: string(MethodCancelDecision),
			Status: domain.OperationCompleted, ResultRef: "task-0001", StateVersion: 6,
		},
	}
}

// The operator console withdraws one question over the owner-only endpoint, and
// the outcome names the exact task and version it moved.
func TestClient_CancelsOneOpenDecision(t *testing.T) {
	withdrawal := &stubDecisionWithdrawal{result: cancelResultFixture()}
	client := newCancelClient(t, CallerOperatorCLI, withdrawal)

	result, err := client.CancelDecision(context.Background(), "cancel-decision-0001", CancelDecisionInput{
		TaskHandle: "task-0001", ExternalKey: "schema-choice",
	})
	if err != nil {
		t.Fatalf("CancelDecision() error = %v", err)
	}
	if result.TaskHandle != "task-0001" || result.StateVersion != 6 {
		t.Fatalf("result = %#v", result)
	}
	if len(withdrawal.calls) != 1 || withdrawal.calls[0] != "cancel-decision-0001:task-0001:schema-choice" {
		t.Errorf("withdrawal calls = %v", withdrawal.calls)
	}
}

// Withdrawing is a mutation on private task detail, so it stays off the model
// surface exactly as the decision reads do.
func TestHandler_RefusesDecisionCancellationFromTheModelFacade(t *testing.T) {
	client := newCancelClient(t, CallerMCPFacade, &stubDecisionWithdrawal{result: cancelResultFixture()})
	if _, err := client.CancelDecision(context.Background(), "cancel-decision-0001", CancelDecisionInput{
		TaskHandle: "task-0001", ExternalKey: "schema-choice",
	}); err == nil {
		t.Error("CancelDecision(MCP facade) error = nil, want a refusal")
	}
}

// A deployment with no withdrawal authority reports unavailable rather than
// accepting a cancellation it cannot record.
func TestHandler_ReportsDecisionCancellationUnavailable(t *testing.T) {
	client := newCancelClient(t, CallerOperatorCLI, nil)
	if _, err := client.CancelDecision(context.Background(), "cancel-decision-0001", CancelDecisionInput{
		TaskHandle: "task-0001", ExternalKey: "schema-choice",
	}); err == nil {
		t.Error("CancelDecision(no authority) error = nil")
	}
}

func TestCancelDecisionMethod_IsAnOperatorOnlyMutation(t *testing.T) {
	if !MethodCancelDecision.valid() || MethodCancelDecision.SideEffect() != SideEffectMutate {
		t.Fatalf("CancelDecision method = %q, side effect = %q", MethodCancelDecision, MethodCancelDecision.SideEffect())
	}
	if !MethodCancelDecision.operatorOnly() {
		t.Error("withdrawing a question is reachable from the model facade")
	}
}
