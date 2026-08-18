package localapi

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/comisai/comis-dev-crew/internal/application"
	"github.com/comisai/comis-dev-crew/internal/domain"
)

// The scout handle travels on the command, not in the contract. A contract that
// could name one would let the file disagree with what the operator typed, and
// nothing afterwards would say which the service acted on.
func TestLocalAPI_PromotionContractMustNotNameItsOwnScout(t *testing.T) {
	if _, err := DecodePromoteScoutInput([]byte(
		`{"scoutTaskHandle":"task-scout-other","acceptanceCriteria":["x"],"constraints":[],` +
			`"validationProfile":"go-default","deliveryMode":"pull_request","workerProfileId":"w"}`,
	)); err == nil {
		t.Fatal("DecodePromoteScoutInput(with scout) error = nil, want a refusal")
	}
}

// The repository and base revision are inherited from the scout, so the contract
// has no field for either. Strict decoding is what makes their absence a refusal
// rather than a silently dropped field.
func TestLocalAPI_PromotionContractRefusesInheritedAuthorityFields(t *testing.T) {
	for name, payload := range map[string]string{
		"repository": `{"repositoryId":"other","acceptanceCriteria":["x"],"constraints":[],` +
			`"validationProfile":"go-default","deliveryMode":"pull_request","workerProfileId":"w"}`,
		"base revision": `{"baseRevision":"aaaa","acceptanceCriteria":["x"],"constraints":[],` +
			`"validationProfile":"go-default","deliveryMode":"pull_request","workerProfileId":"w"}`,
		"shape": `{"shape":"scout","acceptanceCriteria":["x"],"constraints":[],` +
			`"validationProfile":"go-default","deliveryMode":"pull_request","workerProfileId":"w"}`,
	} {
		if _, err := DecodePromoteScoutInput([]byte(payload)); err == nil {
			t.Errorf("%s: DecodePromoteScoutInput() error = nil, want a refusal", name)
		}
	}
}

func TestLocalAPI_PromotionContractAcceptsTheShipContractAlone(t *testing.T) {
	input, err := DecodePromoteScoutInput([]byte(
		`{"acceptanceCriteria":["The change is proven."],"constraints":[],` +
			`"validationProfile":"go-default","deliveryMode":"pull_request",` +
			`"workerProfileId":"codex-reviewed"}`,
	))
	if err != nil {
		t.Fatalf("DecodePromoteScoutInput() error = %v", err)
	}
	if input.ValidationProfile != "go-default" || input.WorkerProfileID != "codex-reviewed" {
		t.Errorf("decoded contract = %+v", input)
	}
	if input.ScoutTaskHandle != "" {
		t.Error("a decoded contract must leave the scout for the caller to supply")
	}
}

func TestLocalAPI_PromotionContractRefusesAnEmptyOrOversizedPayload(t *testing.T) {
	if _, err := DecodePromoteScoutInput(nil); err == nil {
		t.Error("DecodePromoteScoutInput(empty) error = nil")
	}
	oversized := make([]byte, MaxRequestBytes+1)
	if _, err := DecodePromoteScoutInput(oversized); err == nil {
		t.Error("DecodePromoteScoutInput(oversized) error = nil")
	}
}

func TestLocalAPI_PromoteScoutIsAKnownMutation(t *testing.T) {
	if !MethodPromoteScout.valid() || MethodPromoteScout.SideEffect() != SideEffectMutate {
		t.Fatalf("promote scout method posture = %v/%q", MethodPromoteScout.valid(), MethodPromoteScout.SideEffect())
	}
}

// The handler stamps the service identity itself. A promotion carrying a
// caller-supplied service instance would let one deployment mint tasks bound to
// another's authority.
func TestLocalAPI_PromotionReachesTheCanonicalCommandUnderTheServiceIdentity(t *testing.T) {
	mutations := &apiMutations{result: application.MutationResult{
		Task: domain.Task{Handle: "task-ship-0001", State: domain.TaskPrepared, StateVersion: 1},
		Operation: domain.OperationRecord{
			ID: "operation-promote-0001", Command: "PrepareTask", Status: domain.OperationCompleted,
			ResultRef: "task-ship-0001", StateVersion: 1,
		},
		Preparation: &application.ManagedRunPreparation{
			ExternalRunRef: "task-ship-0001", RegistrationNonce: "registration-nonce_promote",
			RequestedWorkspaceRoot: "/approved/worktrees/task-ship-0001",
			RequestedAttachment: application.PreparedRuntimeAttachment{
				Kind:          application.RuntimeAttachmentUnixSocket,
				SourcePath:    "/approved/runtime/task-ship-0001/attachment.sock",
				RelayIdentity: strings.Repeat("ab", 32),
			},
			ExpiresAt: time.Now().UTC().Add(time.Hour), State: application.PreparationOpen,
		},
	}}
	mutations.promoteResult = mutations.result
	handler, err := NewHandler(HandlerConfig{
		Queries: &apiQueries{}, Mutations: mutations,
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

	if _, err := client.PromoteScout(context.Background(), "operation-promote-0001", PromoteScoutInput{
		ScoutTaskHandle: "task-scout-0001", AcceptanceCriteria: []string{"The change is proven."},
		Constraints: []string{}, ValidationProfile: "go-default",
		DeliveryMode: domain.DeliveryPullRequest, WorkerProfileID: "codex-reviewed",
	}); err != nil {
		t.Fatalf("PromoteScout() error = %v", err)
	}
	if mutations.promoteCommand.ScoutTaskHandle != "task-scout-0001" {
		t.Fatalf("promotion command = %+v", mutations.promoteCommand)
	}
	if mutations.promoteCommand.ServiceInstanceID != "service-instance_a" {
		t.Errorf("promotion service identity = %q, want the handler's own",
			mutations.promoteCommand.ServiceInstanceID)
	}
}

func TestLocalAPI_PromotionReportsAnAbsentMutationSurfaceAsUnavailable(t *testing.T) {
	handler, err := NewHandler(HandlerConfig{
		Queries: &apiQueries{}, ServiceInstanceID: "service-instance_a", Clock: time.Now,
	})
	if err != nil {
		t.Fatalf("NewHandler() error = %v", err)
	}

	outcome := handler.handle(context.Background(), CallerMCPFacade, []byte(
		`{"protocolVersion":"`+ProtocolVersion+`","operationId":"operation-promote-0001",`+
			`"method":"PromoteScout","payload":{"scoutTaskHandle":"task-scout-0001",`+
			`"acceptanceCriteria":["x"],"constraints":[],"validationProfile":"go-default",`+
			`"deliveryMode":"pull_request","workerProfileId":"w"}}`,
	))

	if outcome.Status != domain.OperationRejected || outcome.Error == nil ||
		outcome.Error.Code != domain.ErrorUnavailable {
		t.Fatalf("absent mutation surface outcome = %+v", outcome)
	}
}

func TestLocalAPI_PromotionRefusesAForgedPayload(t *testing.T) {
	handler, err := NewHandler(HandlerConfig{
		Queries: &apiQueries{}, Mutations: &apiMutations{},
		ServiceInstanceID: "service-instance_a", Clock: time.Now,
	})
	if err != nil {
		t.Fatalf("NewHandler() error = %v", err)
	}

	outcome := handler.handle(context.Background(), CallerMCPFacade, []byte(
		`{"protocolVersion":"`+ProtocolVersion+`","operationId":"operation-promote-0001",`+
			`"method":"PromoteScout","payload":{"scoutTaskHandle":"task-scout-0001",`+
			`"repositoryId":"other-repo"}}`,
	))

	if outcome.Status != domain.OperationRejected {
		t.Fatalf("forged promotion payload outcome = %+v, want a rejection", outcome)
	}
}

// Steering carries content, so the transport must refuse text that could
// smuggle terminal escape sequences before the handler ever runs.
func TestLocalAPI_SteerReachesTheCanonicalCommandAndRefusesUnsafeText(t *testing.T) {
	mutations := &apiMutations{steerResult: application.MutationResult{
		Task: domain.Task{Handle: "task-0001", State: domain.TaskWorking, StateVersion: 19},
		Operation: domain.OperationRecord{
			ID: "operation-steer-0001", Command: "SteerTask", Status: domain.OperationCompleted,
			ResultRef: "task-0001", StateVersion: 19,
		},
	}}
	handler, err := NewHandler(HandlerConfig{
		Queries: &apiQueries{}, Mutations: mutations,
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

	if _, err := client.SteerTask(context.Background(), "operation-steer-0001", SteerTaskInput{
		TaskHandle: "task-0001", Instruction: "Prefer the existing parser.",
	}); err != nil {
		t.Fatalf("SteerTask() error = %v", err)
	}
	if mutations.steerCommand.Instruction != "Prefer the existing parser." {
		t.Fatalf("steer command = %+v", mutations.steerCommand)
	}

	outcome := handler.handle(context.Background(), CallerMCPFacade, []byte(
		`{"protocolVersion":"`+ProtocolVersion+`","operationId":"operation-steer-0002",`+
			`"method":"SteerTask","payload":{"taskHandle":"task-0001","instruction":"a",`+
			`"retries":3}}`,
	))
	if outcome.Status != domain.OperationRejected {
		t.Fatalf("forged steer payload outcome = %+v, want a rejection", outcome)
	}
}

func TestLocalAPI_SteerReportsAnAbsentMutationSurfaceAsUnavailable(t *testing.T) {
	handler, err := NewHandler(HandlerConfig{
		Queries: &apiQueries{}, ServiceInstanceID: "service-instance_a", Clock: time.Now,
	})
	if err != nil {
		t.Fatalf("NewHandler() error = %v", err)
	}

	outcome := handler.handle(context.Background(), CallerMCPFacade, []byte(
		`{"protocolVersion":"`+ProtocolVersion+`","operationId":"operation-steer-0001",`+
			`"method":"SteerTask","payload":{"taskHandle":"task-0001","instruction":"Do it."}}`,
	))

	if outcome.Status != domain.OperationRejected || outcome.Error == nil ||
		outcome.Error.Code != domain.ErrorUnavailable {
		t.Fatalf("absent mutation surface outcome = %+v", outcome)
	}
}
