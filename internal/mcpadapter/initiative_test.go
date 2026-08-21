package mcpadapter

import (
	"context"
	"encoding/json"
	"errors"
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
	if !strings.Contains(string(extension), `"relayIdentity":"`+strings.Repeat("ab", 32)+`"`) {
		t.Fatalf("managed-run group extension omitted the prepared relay identity: %s", extension)
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

func TestFacade_PrepareInitiativeReplaysOnlyAfterDurableCompletion(t *testing.T) {
	retryable, err := domain.NewFailure(domain.ErrorUnavailable, true, "unavailable", "reconcile", nil)
	if err != nil {
		t.Fatal(err)
	}
	client := &initiativeMCPClient{
		fakeClient: &fakeClient{operation: application.OperationView{
			SchemaVersion: 1, OperationID: "prepare-initiative-mcp", Command: "PrepareInitiative",
			Status: domain.OperationCompleted, StateVersion: 31,
		}},
		prepare: initiativeMCPPreparation(), prepareErrors: []error{retryable, nil},
	}
	facade, err := New(Config{
		Client: client, ServiceInstanceID: "service-instance-0001", Version: "test",
		NewOperationID: func() (string, error) { return "reconcile-0001", nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := connectFacade(t, facade).CallTool(context.Background(), &mcp.CallToolParams{
		Meta: callMeta("prepare-initiative-mcp", "service-instance-0001"),
		Name: ToolPrepareInitiative, Arguments: prepareInitiativeMCPInput(),
	})
	if err != nil || result.IsError {
		t.Fatalf("CallTool(prepare_initiative) = %#v, %v", result, err)
	}
	if got := strings.Join(client.calls, ","); got != "prepare-initiative:prepare-initiative-mcp,operation:reconcile-0001:prepare-initiative-mcp,prepare-initiative:prepare-initiative-mcp" {
		t.Fatalf("reconciliation calls = %q", got)
	}
}

func TestFacade_ReconcileInitiativePreparationRejectsUncertainOutcomes(t *testing.T) {
	original := errors.New("original uncertain result")
	valid := application.OperationView{
		SchemaVersion: 1, OperationID: "prepare-initiative-mcp", Command: "PrepareInitiative",
		Status: domain.OperationAccepted, StateVersion: 31,
	}
	tests := []struct {
		name      string
		operation application.OperationView
		opErr     error
		newID     func() (string, error)
		wantCode  domain.ErrorCode
	}{
		{name: "operation source failure", operation: valid, newID: func() (string, error) { return "", errors.New("entropy") }},
		{name: "invalid operation source", operation: valid, newID: func() (string, error) { return "BAD ID", nil }},
		{name: "query failure", operation: valid, opErr: errors.New("disconnect")},
		{name: "identity mismatch", operation: func() application.OperationView { value := valid; value.OperationID = "other-0001"; return value }()},
		{name: "command mismatch", operation: func() application.OperationView { value := valid; value.Command = "Other"; return value }()},
		{name: "accepted", operation: valid},
		{name: "unknown", operation: func() application.OperationView { value := valid; value.Status = domain.OperationUnknown; return value }()},
		{name: "rejected invalid code", operation: func() application.OperationView {
			value := valid
			value.Status = domain.OperationRejected
			return value
		}()},
		{name: "rejected", operation: func() application.OperationView {
			value := valid
			value.Status = domain.OperationRejected
			value.ErrorCode = domain.ErrorConflict
			return value
		}(), wantCode: domain.ErrorConflict},
		{name: "invalid status", operation: func() application.OperationView { value := valid; value.Status = "invented"; return value }(), wantCode: domain.ErrorUnknown},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			newID := test.newID
			if newID == nil {
				newID = func() (string, error) { return "reconcile-0001", nil }
			}
			client := &initiativeMCPClient{fakeClient: &fakeClient{operation: test.operation, operationError: test.opErr}}
			facade, err := New(Config{
				Client: client, ServiceInstanceID: "service-instance-0001",
				NewOperationID: newID, ReconcileTimeout: time.Second,
			})
			if err != nil {
				t.Fatal(err)
			}
			_, gotErr := facade.reconcileInitiativePreparation(
				context.Background(), "prepare-initiative-mcp", prepareInitiativeMCPInput().local(), original,
			)
			if test.wantCode == domain.ErrorConflict {
				var failure *domain.Failure
				if !errors.As(gotErr, &failure) || failure.Code != test.wantCode {
					t.Fatalf("error = %v, want %s", gotErr, test.wantCode)
				}
			} else if test.wantCode == domain.ErrorUnknown {
				if gotErr == nil || !strings.Contains(gotErr.Error(), "unknown initiative") {
					t.Fatalf("error = %v, want unknown status", gotErr)
				}
			} else if !errors.Is(gotErr, original) {
				t.Fatalf("error = %v, want original", gotErr)
			}
		})
	}
	facade := &Facade{}
	//lint:ignore SA1012 Boundary test proves reconciliation rejects nil contexts.
	if _, err := facade.reconcileInitiativePreparation(nil, "prepare-initiative-mcp", localapi.PrepareInitiativeInput{}, original); !errors.Is(err, original) {
		t.Fatalf("nil context error = %v, want original", err)
	}
}

func TestInitiativePreparationMetadataRejectsInconsistentPrivateAuthority(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*localapi.PrepareInitiativeResult)
	}{
		{name: "operation mismatch", mutate: func(result *localapi.PrepareInitiativeResult) { result.OperationID = "other-operation" }},
		{name: "group mismatch", mutate: func(result *localapi.PrepareInitiativeResult) {
			result.ManagedRunGroup.ExternalGroupRef = "other-initiative"
		}},
		{name: "empty members", mutate: func(result *localapi.PrepareInitiativeResult) {
			result.TaskHandles = nil
			result.ManagedRunGroup.Members = nil
		}},
		{name: "member mismatch", mutate: func(result *localapi.PrepareInitiativeResult) {
			result.ManagedRunGroup.Members[0].ExternalRunRef = "other-task"
		}},
		{name: "member closed", mutate: func(result *localapi.PrepareInitiativeResult) {
			result.ManagedRunGroup.Members[0].State = application.PreparationAbandoned
		}},
		{name: "expiry mismatch", mutate: func(result *localapi.PrepareInitiativeResult) {
			result.ManagedRunGroup.Members[0].ExpiresAt = result.ManagedRunGroup.ExpiresAt.Add(time.Second)
		}},
		{name: "invalid attachment", mutate: func(result *localapi.PrepareInitiativeResult) {
			result.ManagedRunGroup.Members[0].RequestedAttachment.SourcePath = "relative.sock"
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result := initiativeMCPPreparation()
			test.mutate(&result)
			if metadata, err := initiativePreparationMetadata("prepare-initiative-mcp", result); err == nil || metadata != nil {
				t.Fatalf("initiativePreparationMetadata() = %#v, %v", metadata, err)
			}
		})
	}
}

type initiativeMCPClient struct {
	*fakeClient
	prepare       applicationInitiativePreparationResult
	prepareErrors []error
	detail        application.InitiativeDetail
	backlog       application.BacklogList
}

type applicationInitiativePreparationResult = localapi.PrepareInitiativeResult

func (client *initiativeMCPClient) PrepareInitiative(
	_ context.Context,
	operationID string,
	_ localapi.PrepareInitiativeInput,
) (localapi.PrepareInitiativeResult, error) {
	client.calls = append(client.calls, "prepare-initiative:"+operationID)
	if len(client.prepareErrors) == 0 {
		return client.prepare, nil
	}
	err := client.prepareErrors[0]
	client.prepareErrors = client.prepareErrors[1:]
	return client.prepare, err
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
