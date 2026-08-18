package localapi

import (
	"context"
	"strings"
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

// Resume travels the same mutation path as pause and cancel but reaches the
// intervention surface, not the mutation one. A deployment composed without
// interventions must report that surface as unavailable rather than the
// mutation surface it does have.
func TestLocalAPI_ResumeReachesTheInterventionSurface(t *testing.T) {
	interventions := &apiInterventions{resumeResult: application.MutationResult{
		Task: domain.Task{Handle: "task-0001", State: domain.TaskWorking, StateVersion: 13},
		Operation: domain.OperationRecord{
			ID: "operation-resume-0001", Command: "ResumeTask", Status: domain.OperationCompleted,
			ResultRef: "task-0001", StateVersion: 13,
		},
	}}
	handler, err := NewHandler(HandlerConfig{
		Queries: &apiQueries{}, Interventions: interventions,
		ServiceInstanceID: "service-instance_a", Clock: time.Now,
	})
	if err != nil {
		t.Fatalf("NewHandler() error = %v", err)
	}
	socketPath := startHandlerServer(t, handler, CallerMCPFacade)
	client, err := NewClient(socketPath, time.Second)
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}

	result, err := client.ResumeTask(context.Background(), "operation-resume-0001", ResumeTaskInput{
		TaskHandle: "task-0001",
	})
	if err != nil {
		t.Fatalf("ResumeTask() error = %v", err)
	}
	if interventions.resumeCommand.TaskHandle != "task-0001" {
		t.Fatalf("resume command = %+v", interventions.resumeCommand)
	}
	if result.SideEffect != SideEffectMutate || result.State != domain.TaskWorking {
		t.Errorf("resume result = %+v", result)
	}
}

func TestLocalAPI_ResumeReportsAnAbsentInterventionSurfaceAsUnavailable(t *testing.T) {
	handler, err := NewHandler(HandlerConfig{
		Queries: &apiQueries{}, ServiceInstanceID: "service-instance_a", Clock: time.Now,
	})
	if err != nil {
		t.Fatalf("NewHandler() error = %v", err)
	}

	outcome := handler.handle(context.Background(), CallerMCPFacade, []byte(
		`{"protocolVersion":"`+ProtocolVersion+`","operationId":"operation-resume-0001",`+
			`"method":"ResumeTask","payload":{"taskHandle":"task-0001"}}`,
	))

	if outcome.Status != domain.OperationRejected || outcome.Error == nil ||
		outcome.Error.Code != domain.ErrorUnavailable || !outcome.Error.Retryable {
		t.Fatalf("absent intervention surface outcome = %+v", outcome)
	}
	if !strings.Contains(outcome.Error.Message, "intervention") {
		t.Errorf("refusal must name the absent surface, got %q", outcome.Error.Message)
	}
}

// Every by-handle mutation must reach its own command, and the transport must
// report the method it actually ran. A shared skeleton makes that cheap to get
// wrong: one mis-threaded method argument would make a verify replay answer a
// cancel, and both would look correct in isolation.
func TestLocalAPI_EachByHandleMutationReachesItsOwnCommand(t *testing.T) {
	mutations := &apiMutations{
		pauseResult:  byHandleResult("operation-0001", "PauseTask", domain.TaskWorking),
		cancelResult: byHandleResult("operation-0001", "CancelTask", domain.TaskCancelled),
		verifyResult: byHandleResult("operation-0001", "VerifyTask", domain.TaskValidating),
	}
	socketPath := startHandlerServer(t, newTestHandler(t, mutations), CallerMCPFacade)
	client, err := NewClient(socketPath, time.Second)
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	ctx := context.Background()

	if _, err := client.PauseTask(ctx, "operation-0001", PauseTaskInput{TaskHandle: "task-0001"}); err != nil {
		t.Fatalf("PauseTask() error = %v", err)
	}
	if _, err := client.CancelTask(ctx, "operation-0001", CancelTaskInput{TaskHandle: "task-0002"}); err != nil {
		t.Fatalf("CancelTask() error = %v", err)
	}
	if _, err := client.VerifyTask(ctx, "operation-0001", VerifyTaskInput{TaskHandle: "task-0003"}); err != nil {
		t.Fatalf("VerifyTask() error = %v", err)
	}

	if mutations.pauseCommand.TaskHandle != "task-0001" {
		t.Errorf("pause reached %q", mutations.pauseCommand.TaskHandle)
	}
	if mutations.cancelCommand.TaskHandle != "task-0002" {
		t.Errorf("cancel reached %q", mutations.cancelCommand.TaskHandle)
	}
	if mutations.verifyCommand.TaskHandle != "task-0003" {
		t.Errorf("verify reached %q", mutations.verifyCommand.TaskHandle)
	}
}

// The completeness check compares the operation's recorded command against the
// method the transport ran. A verify whose operation says it cancelled is a
// mismatch the caller must never see reported as its own outcome.
func TestLocalAPI_RefusesAMutationWhoseOperationRecordsADifferentCommand(t *testing.T) {
	mutations := &apiMutations{
		verifyResult: byHandleResult("operation-0001", "CancelTask", domain.TaskValidating),
	}
	socketPath := startHandlerServer(t, newTestHandler(t, mutations), CallerMCPFacade)
	client, err := NewClient(socketPath, time.Second)
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}

	if _, err := client.VerifyTask(context.Background(), "operation-0001", VerifyTaskInput{
		TaskHandle: "task-0001",
	}); err == nil {
		t.Fatal("VerifyTask() with a foreign operation command error = nil, want a refusal")
	}
}

func byHandleResult(operationID, command string, state domain.TaskState) application.MutationResult {
	return application.MutationResult{
		Task: domain.Task{Handle: "task-0001", State: state, StateVersion: 9},
		Operation: domain.OperationRecord{
			ID: operationID, Command: command, Status: domain.OperationCompleted,
			ResultRef: "task-0001", StateVersion: 9,
		},
	}
}

// Replacement travels the intervention surface, and the handler must report the
// method it actually ran. A deployment without interventions reports that
// surface unavailable rather than the mutation surface it does have.
func TestLocalAPI_ReplaceReachesTheInterventionSurface(t *testing.T) {
	interventions := &apiInterventions{replaceResult: application.MutationResult{
		Task: domain.Task{Handle: "task-0001", State: domain.TaskReady, StateVersion: 17},
		Operation: domain.OperationRecord{
			ID: "operation-replace-0001", Command: "ReplaceWorker", Status: domain.OperationCompleted,
			ResultRef: "task-0001", StateVersion: 17,
		},
	}}
	handler, err := NewHandler(HandlerConfig{
		Queries: &apiQueries{}, Interventions: interventions,
		ServiceInstanceID: "service-instance_a", Clock: time.Now,
	})
	if err != nil {
		t.Fatalf("NewHandler() error = %v", err)
	}
	socketPath := startHandlerServer(t, handler, CallerMCPFacade)
	client, err := NewClient(socketPath, time.Second)
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}

	result, err := client.ReplaceWorker(context.Background(), "operation-replace-0001", ReplaceWorkerInput{
		TaskHandle: "task-0001", WorkerProfileID: "claude-reviewed",
	})
	if err != nil {
		t.Fatalf("ReplaceWorker() error = %v", err)
	}
	if interventions.replaceCommand.TaskHandle != "task-0001" ||
		interventions.replaceCommand.WorkerProfileID != "claude-reviewed" {
		t.Fatalf("replacement command = %+v", interventions.replaceCommand)
	}
	if result.SideEffect != SideEffectMutate || result.State != domain.TaskReady {
		t.Errorf("replacement result = %+v", result)
	}
}

func TestLocalAPI_ReplaceReportsAnAbsentInterventionSurfaceAsUnavailable(t *testing.T) {
	handler, err := NewHandler(HandlerConfig{
		Queries: &apiQueries{}, ServiceInstanceID: "service-instance_a", Clock: time.Now,
	})
	if err != nil {
		t.Fatalf("NewHandler() error = %v", err)
	}

	outcome := handler.handle(context.Background(), CallerMCPFacade, []byte(
		`{"protocolVersion":"`+ProtocolVersion+`","operationId":"operation-replace-0001",`+
			`"method":"ReplaceWorker","payload":{"taskHandle":"task-0001",`+
			`"workerProfileId":"claude-reviewed"}}`,
	))

	if outcome.Status != domain.OperationRejected || outcome.Error == nil ||
		outcome.Error.Code != domain.ErrorUnavailable {
		t.Fatalf("absent intervention surface outcome = %+v", outcome)
	}
}

// A payload carrying anything beyond the task and the profile is refused: a
// replacement takes no disposition and no instruction.
func TestLocalAPI_ReplaceRefusesAForgedPayload(t *testing.T) {
	handler, err := NewHandler(HandlerConfig{
		Queries: &apiQueries{}, Interventions: &apiInterventions{},
		ServiceInstanceID: "service-instance_a", Clock: time.Now,
	})
	if err != nil {
		t.Fatalf("NewHandler() error = %v", err)
	}

	outcome := handler.handle(context.Background(), CallerMCPFacade, []byte(
		`{"protocolVersion":"`+ProtocolVersion+`","operationId":"operation-replace-0001",`+
			`"method":"ReplaceWorker","payload":{"taskHandle":"task-0001",`+
			`"workerProfileId":"claude-reviewed","discardWork":true}}`,
	))

	if outcome.Status != domain.OperationRejected {
		t.Fatalf("forged replacement payload outcome = %+v, want a rejection", outcome)
	}
}
