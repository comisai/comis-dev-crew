package localapi

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/comisai/comis-dev-crew/internal/application"
	"github.com/comisai/comis-dev-crew/internal/domain"
)

func TestServerClient_InitiativeControlsReturnPerMemberOperatorResults(t *testing.T) {
	controls := &apiInitiativeControls{result: apiInitiativeControlResult()}
	handler, err := NewHandler(HandlerConfig{
		Queries: &apiQueries{}, InitiativeControls: controls,
		ServiceInstanceID: "service-instance_a", Clock: time.Now,
	})
	if err != nil {
		t.Fatalf("NewHandler() error = %v", err)
	}
	client, err := NewClient(startHandlerServer(t, handler, CallerOperatorCLI), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	result, err := client.PauseInitiative(
		context.Background(), "operation-pause-group", InitiativeControlInput{InitiativeHandle: "initiative-control"},
	)
	if err != nil {
		t.Fatalf("PauseInitiative() error = %v", err)
	}
	if result.OperationID != "operation-pause-group" || result.SideEffect != SideEffectMutate ||
		result.InitiativeHandle != "initiative-control" || len(result.Members) != 2 ||
		result.Members[1].Outcome != application.InitiativeControlRejected {
		t.Fatalf("PauseInitiative() = %#v", result)
	}
	if controls.command.OperationID != "operation-pause-group" ||
		controls.command.InitiativeHandle != "initiative-control" || controls.method != MethodPauseInitiative {
		t.Fatalf("initiative control dispatch = %q/%#v", controls.method, controls.command)
	}
	for _, method := range []Method{MethodPauseInitiative, MethodResumeInitiative, MethodCancelInitiative} {
		if !method.valid() || method.SideEffect() != SideEffectMutate || !method.operatorOnly() {
			t.Fatalf("method %q posture = valid %t, side effect %q, operator-only %t",
				method, method.valid(), method.SideEffect(), method.operatorOnly())
		}
	}
	if _, err := client.ResumeInitiative(
		context.Background(), "operation-resume-group", InitiativeControlInput{InitiativeHandle: "initiative-control"},
	); err != nil || controls.method != MethodResumeInitiative {
		t.Fatalf("ResumeInitiative() method/error = %q/%v", controls.method, err)
	}
	if _, err := client.CancelInitiative(
		context.Background(), "operation-cancel-group", InitiativeControlInput{InitiativeHandle: "initiative-control"},
	); err != nil || controls.method != MethodCancelInitiative {
		t.Fatalf("CancelInitiative() method/error = %q/%v", controls.method, err)
	}
}

func TestInitiativeControlsRefuseMCPAuthorityAndForgedMemberSelection(t *testing.T) {
	controls := &apiInitiativeControls{result: apiInitiativeControlResult()}
	handler, err := NewHandler(HandlerConfig{
		Queries: &apiQueries{}, InitiativeControls: controls,
		ServiceInstanceID: "service-instance_a", Clock: time.Now,
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, method := range []Method{MethodPauseInitiative, MethodResumeInitiative, MethodCancelInitiative} {
		request := []byte(`{"protocolVersion":"devcrew.local.v1","operationId":"operation-control-group",` +
			`"method":"` + string(method) + `","payload":{"initiativeHandle":"initiative-control"}}`)
		outcome := handler.handle(context.Background(), CallerMCPFacade, request)
		if outcome.Error == nil || outcome.Error.Code != domain.ErrorUnauthorized {
			t.Fatalf("MCP %q outcome = %#v", method, outcome)
		}
	}
	forged := []byte(`{"protocolVersion":"devcrew.local.v1","operationId":"operation-control-group",` +
		`"method":"PauseInitiative","payload":{"initiativeHandle":"initiative-control","taskHandles":["task-other"]}}`)
	if outcome := handler.handle(context.Background(), CallerOperatorCLI, forged); outcome.Error == nil ||
		outcome.Error.Code != domain.ErrorInvalidArgument {
		t.Fatalf("forged initiative member selection outcome = %#v", outcome)
	}
	if controls.command.OperationID != "" {
		t.Fatalf("refused initiative control reached application: %#v", controls.command)
	}
}

func TestInitiativeControlBoundaryRefusesUnavailableAndIncompleteResults(t *testing.T) {
	readOnly, err := NewHandler(HandlerConfig{Queries: &apiQueries{}, Clock: time.Now})
	if err != nil {
		t.Fatal(err)
	}
	request := []byte(`{"protocolVersion":"devcrew.local.v1","operationId":"operation-control-group",` +
		`"method":"PauseInitiative","payload":{"initiativeHandle":"initiative-control"}}`)
	if outcome := readOnly.handle(context.Background(), CallerOperatorCLI, request); outcome.Error == nil ||
		outcome.Error.Code != domain.ErrorUnavailable {
		t.Fatalf("unavailable initiative controls outcome = %#v", outcome)
	}

	controls := &apiInitiativeControls{result: apiInitiativeControlResult()}
	handler, err := NewHandler(HandlerConfig{
		Queries: &apiQueries{}, InitiativeControls: controls,
		ServiceInstanceID: "service-instance_a", Clock: time.Now,
	})
	if err != nil {
		t.Fatal(err)
	}
	controls.result.Operation.ResultRef = "initiative-other"
	if outcome := handler.handle(context.Background(), CallerOperatorCLI, request); outcome.Error == nil ||
		outcome.Error.Code != domain.ErrorInternal {
		t.Fatalf("incomplete initiative control outcome = %#v", outcome)
	}
	controls.err, _ = domain.NewFailure(
		domain.ErrorPrecondition, false, "initiative cannot be controlled", "inspect the initiative", nil,
	)
	if outcome := handler.handle(context.Background(), CallerOperatorCLI, request); outcome.Error == nil ||
		outcome.Error.Code != domain.ErrorPrecondition {
		t.Fatalf("failed initiative control outcome = %#v", outcome)
	}
}

type apiInitiativeControls struct {
	command application.InitiativeControlCommand
	method  Method
	result  application.InitiativeControlResult
	err     error
}

func (controls *apiInitiativeControls) PauseInitiative(
	_ context.Context,
	command application.InitiativeControlCommand,
) (application.InitiativeControlResult, error) {
	controls.command, controls.method = command, MethodPauseInitiative
	return controls.resultFor(command, MethodPauseInitiative), controls.err
}

func (controls *apiInitiativeControls) ResumeInitiative(
	_ context.Context,
	command application.InitiativeControlCommand,
) (application.InitiativeControlResult, error) {
	controls.command, controls.method = command, MethodResumeInitiative
	return controls.resultFor(command, MethodResumeInitiative), controls.err
}

func (controls *apiInitiativeControls) CancelInitiative(
	_ context.Context,
	command application.InitiativeControlCommand,
) (application.InitiativeControlResult, error) {
	controls.command, controls.method = command, MethodCancelInitiative
	return controls.resultFor(command, MethodCancelInitiative), controls.err
}

func (controls *apiInitiativeControls) resultFor(
	command application.InitiativeControlCommand,
	method Method,
) application.InitiativeControlResult {
	result := controls.result
	result.Operation.ID = command.OperationID
	result.Operation.Command = string(method)
	return result
}

func apiInitiativeControlResult() application.InitiativeControlResult {
	return application.InitiativeControlResult{
		InitiativeHandle: "initiative-control", State: domain.InitiativeBlocked, StateVersion: 12,
		Members: []application.InitiativeControlMemberResult{
			{TaskHandle: "task-control-a", OperationID: "control-member-a", Outcome: application.InitiativeControlCompleted, State: domain.TaskWorking, StateVersion: 11},
			{TaskHandle: "task-control-b", OperationID: "control-member-b", Outcome: application.InitiativeControlRejected, ErrorCode: domain.ErrorPrecondition, State: domain.TaskPaused, StateVersion: 12},
		},
		Operation: domain.OperationRecord{
			SchemaVersion: 1, ID: "operation-pause-group", Command: "PauseInitiative",
			SubjectDigest: strings.Repeat("a", 64), Status: domain.OperationCompleted,
			ResultRef: "initiative-control", StateVersion: 12,
			CreatedAt: time.Date(2026, time.August, 20, 18, 0, 0, 0, time.UTC),
			UpdatedAt: time.Date(2026, time.August, 20, 18, 0, 0, 0, time.UTC),
		},
	}
}
