package mcpadapter

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/comisai/comis-dev-crew/internal/application"
	"github.com/comisai/comis-dev-crew/internal/comiswire"
	"github.com/comisai/comis-dev-crew/internal/domain"
	"github.com/comisai/comis-dev-crew/internal/localapi"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestFacade_InitiativeToolsPreserveCanonicalAuthorityAndSideEffects(t *testing.T) {
	client := &initiativeMCPClient{fakeClient: &fakeClient{}}
	client.prepare = initiativeMCPPreparation()
	client.detail = application.InitiativeDetail{
		SchemaVersion: 1, StateVersion: 31,
		Initiative: domain.DevelopmentInitiative{Handle: "initiative-mcp", State: domain.InitiativePreparing},
	}
	client.backlog = application.BacklogList{SchemaVersion: 1, StateVersion: 31, Items: []domain.BacklogItem{}}
	facade, err := New(Config{
		Client: client, ServiceInstanceID: "service-instance-0001", Version: "test",
		NewOperationID: func() (string, error) { return "reconcile-0001", nil },
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	session := connectFacade(t, facade)
	tools, err := session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("ListTools() error = %v", err)
	}
	wantRead := map[string]bool{
		ToolPrepareInitiative: false, ToolGetInitiative: true, ToolBacklogList: true,
	}
	for _, listed := range tools.Tools {
		readOnly, wanted := wantRead[listed.Name]
		if !wanted {
			continue
		}
		if listed.Annotations == nil || listed.Annotations.ReadOnlyHint != readOnly ||
			listed.Annotations.DestructiveHint == nil || *listed.Annotations.DestructiveHint {
			t.Fatalf("tool %q annotations = %#v", listed.Name, listed.Annotations)
		}
		delete(wantRead, listed.Name)
	}
	if len(wantRead) != 0 {
		t.Fatalf("initiative tools are absent: %#v", wantRead)
	}

	prepared, err := session.CallTool(context.Background(), &mcp.CallToolParams{
		Meta: callMeta("prepare-initiative-mcp", "service-instance-0001"),
		Name: ToolPrepareInitiative, Arguments: prepareInitiativeMCPInput(),
	})
	if err != nil || prepared.IsError {
		t.Fatalf("CallTool(prepare_initiative) = %#v, %v", prepared, err)
	}
	visible, err := json.Marshal(prepared.StructuredContent)
	if err != nil {
		t.Fatal(err)
	}
	for _, private := range []string{"registration-nonce", "/approved/worktrees", "/approved/runtime", "managedRunGroup"} {
		if strings.Contains(string(visible), private) {
			t.Fatalf("visible initiative result leaked %q: %s", private, visible)
		}
	}
	extension, err := json.Marshal(prepared.Meta[ManagedRunResultMetaKey])
	if err != nil || comiswire.ValidatePayload(comiswire.PayloadMCPManagedRunGroup, extension) != nil {
		t.Fatalf("managed-run group extension = %s, %v", extension, err)
	}

	if _, err := session.CallTool(context.Background(), &mcp.CallToolParams{
		Meta: callMeta("get-initiative-mcp", "service-instance-0001"), Name: ToolGetInitiative,
		Arguments: InitiativeInput{InitiativeHandle: "initiative-mcp"},
	}); err != nil {
		t.Fatalf("CallTool(get_initiative) error = %v", err)
	}
	if _, err := session.CallTool(context.Background(), &mcp.CallToolParams{
		Meta: callMeta("list-backlog-mcp", "service-instance-0001"), Name: ToolBacklogList,
		Arguments: BacklogListInput{RepositoryID: "repo-primary", Readiness: domain.BacklogReady},
	}); err != nil {
		t.Fatalf("CallTool(backlog_list) error = %v", err)
	}
	if got := strings.Join(client.calls, ","); got !=
		"prepare-initiative:prepare-initiative-mcp,get-initiative:get-initiative-mcp:initiative-mcp,list-backlog:list-backlog-mcp:repo-primary:ready" {
		t.Fatalf("canonical initiative calls = %q", got)
	}
}

func TestFacade_PrepareInitiativeSchemaCannotSelectServiceOrHostAuthority(t *testing.T) {
	facade, err := New(Config{
		Client:            &initiativeMCPClient{fakeClient: &fakeClient{}},
		ServiceInstanceID: "service-instance-0001", Version: "test",
		NewOperationID: func() (string, error) { return "reconcile-0001", nil },
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	tools, err := connectFacade(t, facade).ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("ListTools() error = %v", err)
	}
	for _, listed := range tools.Tools {
		if listed.Name != ToolPrepareInitiative {
			continue
		}
		encoded, marshalErr := json.Marshal(listed.InputSchema)
		if marshalErr != nil {
			t.Fatal(marshalErr)
		}
		schema := string(encoded)
		for _, required := range []string{"baseRevisionSet", "components", "tasks", "contract", "integrationPolicyId"} {
			if !strings.Contains(schema, required) {
				t.Fatalf("prepare_initiative schema omits %q: %s", required, schema)
			}
		}
		for _, forbidden := range []string{"serviceInstanceId", "managedRunGroupId", "registrationNonce"} {
			if strings.Contains(schema, forbidden) {
				t.Fatalf("prepare_initiative schema exposes %q: %s", forbidden, schema)
			}
		}
		return
	}
	t.Fatal("prepare_initiative tool is absent")
}

type initiativeMCPClient struct {
	*fakeClient
	prepare applicationInitiativePreparationResult
	detail  application.InitiativeDetail
	backlog application.BacklogList
}

type applicationInitiativePreparationResult = localapi.PrepareInitiativeResult

func (client *initiativeMCPClient) PrepareInitiative(
	_ context.Context,
	operationID string,
	_ localapi.PrepareInitiativeInput,
) (localapi.PrepareInitiativeResult, error) {
	client.calls = append(client.calls, "prepare-initiative:"+operationID)
	return client.prepare, nil
}

func (client *initiativeMCPClient) GetInitiative(
	_ context.Context,
	operationID string,
	handle string,
) (application.InitiativeDetail, error) {
	client.calls = append(client.calls, "get-initiative:"+operationID+":"+handle)
	return client.detail, nil
}

func (client *initiativeMCPClient) ListInitiatives(
	context.Context,
	string,
	localapi.ListInitiativesInput,
) (application.InitiativeList, error) {
	return application.InitiativeList{}, nil
}

func (client *initiativeMCPClient) ListBacklog(
	_ context.Context,
	operationID string,
	input localapi.ListBacklogInput,
) (application.BacklogList, error) {
	client.calls = append(client.calls, "list-backlog:"+operationID+":"+input.RepositoryID+":"+string(input.Readiness))
	return client.backlog, nil
}

func prepareInitiativeMCPInput() PrepareInitiativeInput {
	return PrepareInitiativeInput{
		TitleRef: "title-ref",
		BaseRevisionSet: []PrepareInitiativeBaseRevision{{
			RepositoryID: "repo-primary", Revision: strings.Repeat("a", 40),
		}},
		Components: []PrepareInitiativeComponent{{
			ComponentHandle: "component-primary", RepositoryID: "repo-primary",
			ResponsibilityRef: "responsibility-primary",
			Tasks: []PrepareInitiativeTask{{
				TaskRef: "member-ref", Contract: PrepareInitiativeTaskContract{
					Shape: domain.ShapeShip, AcceptanceCriteria: []string{"The component is verified."},
					Constraints: []string{}, ValidationProfile: "go-default",
					DeliveryMode: domain.DeliveryPullRequest, WorkerProfileID: "codex-reviewed",
				},
			}},
		}},
		Edges: []PrepareInitiativeEdge{}, ContractArtifacts: []string{},
		IntegrationPolicyID: "integration-policy-a", IntegrationOwnerTask: "member-ref",
	}
}

func initiativeMCPPreparation() localapi.PrepareInitiativeResult {
	expiresAt := time.Date(2026, time.August, 20, 19, 0, 0, 0, time.UTC)
	return localapi.PrepareInitiativeResult{
		SchemaVersion: 1, OperationID: "prepare-initiative-mcp",
		InitiativeHandle: "initiative-mcp", State: domain.InitiativePreparing,
		StateVersion: 31, SideEffect: localapi.SideEffectMutate,
		TaskHandles: []string{"task-mcp-member"},
		ManagedRunGroup: application.ManagedRunGroupPreparation{
			ExternalGroupRef: "initiative-mcp", RegistrationNonce: "registration-nonce_group_mcp",
			ExpiresAt: expiresAt,
			Members: []application.ManagedRunPreparation{{
				ExternalRunRef: "task-mcp-member", RegistrationNonce: "registration-nonce_member_mcp",
				RequestedWorkspaceRoot: "/approved/worktrees/task-mcp-member",
				RequestedAttachment: application.PreparedRuntimeAttachment{
					Kind:          application.RuntimeAttachmentUnixSocket,
					SourcePath:    "/approved/runtime/task-mcp-member/attachment.sock",
					RelayIdentity: strings.Repeat("ab", 32),
				},
				ExpiresAt: expiresAt, State: application.PreparationOpen,
			}},
		},
	}
}
