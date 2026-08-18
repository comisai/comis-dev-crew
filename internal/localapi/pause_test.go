package localapi

import (
	"context"
	"testing"
	"time"

	"github.com/comisai/comis-dev-crew/internal/application"
	"github.com/comisai/comis-dev-crew/internal/domain"
)

func pauseTaskResult() application.MutationResult {
	return application.MutationResult{
		Task: domain.Task{Handle: "task-0001", State: domain.TaskWorking, StateVersion: 9},
		Operation: domain.OperationRecord{
			ID: "operation-pause-0001", Command: "PauseTask", Status: domain.OperationCompleted,
			ResultRef: "task-0001", StateVersion: 9,
		},
	}
}

// Pause is classified a mutation by the transport itself. A read-classified
// method would travel on the read path, where a repeat is assumed free and no
// operation identity is required — and the pause would stop being replayable.
func TestLocalAPI_PauseIsClassifiedAsAMutation(t *testing.T) {
	if MethodPauseTask.SideEffect() != SideEffectMutate {
		t.Errorf("pause side effect = %q, want mutate", MethodPauseTask.SideEffect())
	}
	if !MethodPauseTask.valid() {
		t.Error("pause must be a known method")
	}
}

func TestLocalAPI_PauseReachesTheCanonicalCommandAndReportsItsMutation(t *testing.T) {
	mutations := &apiMutations{pauseResult: pauseTaskResult()}
	socketPath := startHandlerServer(t, newTestHandler(t, mutations), CallerMCPFacade)
	client, err := NewClient(socketPath, time.Second)
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}

	result, err := client.PauseTask(context.Background(), "operation-pause-0001", PauseTaskInput{
		TaskHandle: "task-0001",
	})
	if err != nil {
		t.Fatalf("PauseTask() error = %v", err)
	}
	if mutations.pauseCommand.TaskHandle != "task-0001" ||
		mutations.pauseCommand.OperationID != "operation-pause-0001" {
		t.Fatalf("pause command = %+v", mutations.pauseCommand)
	}
	if result.SideEffect != SideEffectMutate {
		t.Errorf("result side effect = %q, want mutate", result.SideEffect)
	}
	// The reported state is the task's actual one. A pause that echoed "paused"
	// would tell the caller the worker had stopped when only the request landed.
	if result.State != domain.TaskWorking {
		t.Errorf("result state = %q, want the task's real state", result.State)
	}
}

// A payload carrying anything beyond the task reference is refused before the
// handler runs: pause takes no instruction, and a transport that silently
// dropped unknown fields would let one look accepted.
func TestLocalAPI_PauseRefusesAForgedPayload(t *testing.T) {
	handler := newTestHandler(t, &apiMutations{pauseResult: pauseTaskResult()})

	outcome := handler.handle(context.Background(), CallerMCPFacade, []byte(
		`{"protocolVersion":"`+ProtocolVersion+`","operationId":"operation-pause-0001",`+
			`"method":"PauseTask","payload":{"taskHandle":"task-0001","interrupt":true}}`,
	))

	if outcome.Status != domain.OperationRejected {
		t.Fatalf("forged pause payload outcome = %+v, want a rejection", outcome)
	}
}

// A service with no mutation surface reports that it is unavailable and
// retryable, rather than refusing as though the request were malformed.
func TestLocalAPI_PauseReportsAnAbsentMutationSurfaceAsUnavailable(t *testing.T) {
	handler := newTestHandler(t, nil)

	outcome := handler.handle(context.Background(), CallerMCPFacade, []byte(
		`{"protocolVersion":"`+ProtocolVersion+`","operationId":"operation-pause-0001",`+
			`"method":"PauseTask","payload":{"taskHandle":"task-0001"}}`,
	))

	if outcome.Status != domain.OperationRejected || outcome.Error == nil ||
		outcome.Error.Code != domain.ErrorUnavailable || !outcome.Error.Retryable {
		t.Fatalf("absent mutation surface outcome = %+v", outcome)
	}
}

func newTestHandler(t *testing.T, mutations *apiMutations) *Handler {
	t.Helper()
	config := HandlerConfig{
		Queries: &apiQueries{}, ServiceInstanceID: "service-instance_a", Clock: time.Now,
	}
	// A nil *apiMutations in an interface slot is not a nil interface, so the
	// absent-surface case must leave the field unset rather than assign a typed
	// nil — otherwise the handler would call through it and panic.
	if mutations != nil {
		config.Mutations = mutations
	}
	handler, err := NewHandler(config)
	if err != nil {
		t.Fatalf("NewHandler() error = %v", err)
	}
	return handler
}
