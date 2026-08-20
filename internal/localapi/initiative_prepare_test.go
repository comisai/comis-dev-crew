package localapi

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/comisai/comis-dev-crew/internal/application"
	"github.com/comisai/comis-dev-crew/internal/domain"
)

func TestServerClient_PrepareInitiativeUsesCanonicalGroupMutation(t *testing.T) {
	now := time.Date(2026, time.August, 20, 12, 0, 0, 0, time.UTC)
	input := prepareInitiativeInputFixture()
	mutations := &apiInitiativeMutations{result: initiativePreparationFixture(now)}
	handler, err := NewHandler(HandlerConfig{
		Queries: &apiQueries{}, InitiativeMutations: mutations,
		ServiceInstanceID: "service-instance_a", Clock: func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("NewHandler() error = %v", err)
	}
	socketPath := startHandlerServer(t, handler, CallerMCPFacade)
	client, err := NewClient(socketPath, time.Second)
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}

	result, err := client.PrepareInitiative(context.Background(), "operation-initiative-prepare", input)
	if err != nil {
		t.Fatalf("PrepareInitiative() error = %v", err)
	}
	if result.InitiativeHandle != "initiative-0001" || result.State != domain.InitiativePreparing ||
		result.StateVersion != 8 || result.SideEffect != SideEffectMutate ||
		len(result.TaskHandles) != 2 || result.TaskHandles[0] != "task-0001" ||
		!reflect.DeepEqual(result.ManagedRunGroup, mutations.result.Preparation) {
		t.Fatalf("PrepareInitiative() = %#v", result)
	}
	wantCommand := application.PrepareInitiativeCommand{
		OperationID: "operation-initiative-prepare", ServiceInstanceID: "service-instance_a",
		TitleRef: input.TitleRef, BaseRevisionSet: input.BaseRevisionSet,
		Components: input.Components, Edges: input.Edges,
		ContractArtifacts: input.ContractArtifacts, IntegrationPolicyID: input.IntegrationPolicyID,
		IntegrationOwnerTask: input.IntegrationOwnerTask,
	}
	if !reflect.DeepEqual(mutations.command, wantCommand) {
		t.Fatalf("canonical initiative command = %#v, want %#v", mutations.command, wantCommand)
	}
	if !MethodPrepareInitiative.valid() || MethodPrepareInitiative.SideEffect() != SideEffectMutate {
		t.Fatalf("prepare initiative method posture = %v/%q", MethodPrepareInitiative.valid(), MethodPrepareInitiative.SideEffect())
	}
}

func TestPrepareInitiativeBoundaryRefusesForgedAuthorityAndIncompleteResults(t *testing.T) {
	now := time.Date(2026, time.August, 20, 12, 0, 0, 0, time.UTC)
	mutations := &apiInitiativeMutations{result: initiativePreparationFixture(now)}
	handler, err := NewHandler(HandlerConfig{
		Queries: &apiQueries{}, InitiativeMutations: mutations,
		ServiceInstanceID: "service-instance_a", Clock: func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("NewHandler() error = %v", err)
	}
	for _, payload := range []string{
		`{"titleRef":"title-ref","baseRevisionSet":[],"components":[],"edges":[],"contractArtifacts":[],"integrationPolicyId":"policy-a","serviceInstanceId":"forged"}`,
		`{"titleRef":"title-ref","baseRevisionSet":[],"components":[],"edges":[],"contractArtifacts":[],"integrationPolicyId":"policy-a","managedRunGroupId":"forged"}`,
	} {
		request := []byte(`{"protocolVersion":"` + ProtocolVersion + `","operationId":"operation-initiative-forged",` +
			`"method":"PrepareInitiative","payload":` + payload + `}`)
		outcome := handler.handle(context.Background(), CallerMCPFacade, request)
		if outcome.Status != domain.OperationRejected || outcome.Error == nil ||
			outcome.Error.Code != domain.ErrorInvalidArgument {
			t.Fatalf("forged initiative outcome = %#v", outcome)
		}
	}
	if mutations.command.OperationID != "" {
		t.Fatalf("forged payload reached initiative mutations: %#v", mutations.command)
	}

	readOnly, err := NewHandler(HandlerConfig{Queries: &apiQueries{}, Clock: func() time.Time { return now }})
	if err != nil {
		t.Fatalf("NewHandler(read only) error = %v", err)
	}
	encoded, err := json.Marshal(prepareInitiativeInputFixture())
	if err != nil {
		t.Fatalf("marshal initiative input: %v", err)
	}
	request := []byte(`{"protocolVersion":"` + ProtocolVersion + `","operationId":"operation-initiative-prepare",` +
		`"method":"PrepareInitiative","payload":` + string(encoded) + `}`)
	if outcome := readOnly.handle(context.Background(), CallerMCPFacade, request); outcome.Error == nil ||
		outcome.Error.Code != domain.ErrorUnavailable {
		t.Fatalf("absent initiative mutation outcome = %#v", outcome)
	}

	mutations.result.Preparation.Members = mutations.result.Preparation.Members[:1]
	if outcome := handler.handle(context.Background(), CallerMCPFacade, request); outcome.Error == nil ||
		outcome.Error.Code != domain.ErrorInternal {
		t.Fatalf("incomplete initiative outcome = %#v", outcome)
	}
}

type apiInitiativeMutations struct {
	command application.PrepareInitiativeCommand
	result  application.InitiativePreparationResult
	err     error
}

func (mutations *apiInitiativeMutations) PrepareInitiative(
	_ context.Context,
	command application.PrepareInitiativeCommand,
) (application.InitiativePreparationResult, error) {
	mutations.command = command
	return mutations.result, mutations.err
}

func prepareInitiativeInputFixture() PrepareInitiativeInput {
	contract := application.PrepareInitiativeTaskContract{
		Shape: domain.ShapeShip, AcceptanceCriteria: []string{"The component is verified."},
		Constraints: []string{"Keep the interface stable."}, ValidationProfile: "go-default",
		DeliveryMode: domain.DeliveryPullRequest, WorkerProfileID: "codex-reviewed",
	}
	return PrepareInitiativeInput{
		TitleRef: "title-ref", BaseRevisionSet: []domain.InitiativeBaseRevision{
			{RepositoryID: "product-api", Revision: strings.Repeat("a", 40)},
		},
		Components: []application.PrepareInitiativeComponent{
			{ComponentHandle: "component-api", RepositoryID: "product-api", ResponsibilityRef: "responsibility-api", Tasks: []application.PrepareInitiativeTask{
				{TaskRef: "api-ref", Contract: contract}, {TaskRef: "integration-ref", Contract: contract},
			}},
		},
		Edges: []application.PrepareInitiativeEdge{{
			FromTaskRef: "api-ref", ToTaskRef: "integration-ref", Kind: domain.EdgeBlocksStart,
		}},
		ContractArtifacts: []string{}, IntegrationPolicyID: "integration-policy-a",
		IntegrationOwnerTask: "integration-ref",
	}
}

func initiativePreparationFixture(now time.Time) application.InitiativePreparationResult {
	members := []application.ManagedRunPreparation{
		initiativeMemberPreparation(now, "task-0001", "nonce_member_0001"),
		initiativeMemberPreparation(now, "task-0002", "nonce_member_0002"),
	}
	return application.InitiativePreparationResult{
		Initiative: domain.DevelopmentInitiative{
			SchemaVersion: 1, Handle: "initiative-0001", State: domain.InitiativePreparing,
			StateVersion: 8,
		},
		Tasks: []domain.Task{
			{Handle: "task-0001", State: domain.TaskPrepared, StateVersion: 8},
			{Handle: "task-0002", State: domain.TaskPrepared, StateVersion: 8},
		},
		Preparation: application.ManagedRunGroupPreparation{
			ExternalGroupRef: "initiative-0001", RegistrationNonce: "nonce_group_0001",
			Members: members, ExpiresAt: now.Add(time.Hour),
		},
		Operation: domain.OperationRecord{
			ID: "operation-initiative-prepare", Command: "PrepareInitiative",
			Status: domain.OperationCompleted, ResultRef: "initiative-0001", StateVersion: 8,
		},
	}
}

func initiativeMemberPreparation(now time.Time, taskHandle, nonce string) application.ManagedRunPreparation {
	return application.ManagedRunPreparation{
		ExternalRunRef: taskHandle, RegistrationNonce: nonce,
		RequestedWorkspaceRoot: "/approved/worktrees/" + taskHandle,
		RequestedAttachment: application.PreparedRuntimeAttachment{
			Kind:          application.RuntimeAttachmentUnixSocket,
			SourcePath:    "/approved/runtime/" + taskHandle + "/attachment.sock",
			RelayIdentity: strings.Repeat("ab", 32),
		},
		ExpiresAt: now.Add(time.Hour), State: application.PreparationOpen,
	}
}
