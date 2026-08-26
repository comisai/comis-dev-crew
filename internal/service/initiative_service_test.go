package service

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/comisai/comis-dev-crew/internal/application"
	"github.com/comisai/comis-dev-crew/internal/domain"
	"github.com/comisai/comis-dev-crew/internal/localapi"
)

func TestRun_ComposesInitiativePreparationOnDedicatedMCPEndpoint(t *testing.T) {
	root := shortTempDir(t)
	mcpSocket := filepath.Join(root, "run", "mcp.sock")
	operatorSocket := filepath.Join(root, "run", "operator.sock")
	ready := make(chan struct{})
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	done := make(chan error, 1)
	nonces := []string{"registration-nonce_initiative_group", "registration-nonce_initiative_member"}
	nonceIndex := 0
	go func() {
		done <- Run(ctx, Config{
			DatabasePath: filepath.Join(root, "state", "devcrew.db"),
			SocketPath:   operatorSocket, MCPSocketPath: mcpSocket,
			ServiceInstanceID: "service-instance_a", Repositories: serviceRepositoryCatalog{},
			WorkerProfiles:     func(string, domain.TaskShape) error { return nil },
			ValidationProfiles: func(string, domain.TaskShape) error { return nil },
			Workspaces:         serviceWorkspacePreparer{root: "/approved/worktrees/task-service-initiative"},
			RuntimeAttachments: serviceRuntimeAttachments{},
			TaskIDs:            func(string) (string, error) { return "task-service-initiative", nil },
			RegistrationNonces: func() (string, error) {
				nonce := nonces[nonceIndex]
				nonceIndex++
				return nonce, nil
			},
			PreparationTTL: time.Hour, Ready: func() { close(ready) },
		})
	}()
	select {
	case <-ready:
	case err := <-done:
		t.Fatalf("Run() before ready error = %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("Run() did not advertise ready")
	}
	client, err := localapi.NewClient(mcpSocket, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	result, err := client.PrepareInitiative(context.Background(), "operation-service-initiative", localapi.PrepareInitiativeInput{
		TitleRef: "title-ref",
		BaseRevisionSet: []domain.InitiativeBaseRevision{{
			RepositoryID: "product-api", Revision: strings.Repeat("a", 40),
		}},
		Components: []application.PrepareInitiativeComponent{{
			ComponentHandle: "component-api", RepositoryID: "product-api",
			ResponsibilityRef: "responsibility-api",
			Tasks: []application.PrepareInitiativeTask{{
				TaskRef: "member-ref", Contract: application.PrepareInitiativeTaskContract{
					Shape: domain.ShapeShip, AcceptanceCriteria: []string{"The component is verified."},
					Constraints: []string{}, ValidationProfile: "go-default",
					DeliveryMode: domain.DeliveryPullRequest, WorkerProfileID: "fixture-worker",
				},
			}},
		}},
		Edges: []application.PrepareInitiativeEdge{}, ContractArtifacts: []application.PrepareInitiativeContractArtifact{},
		IntegrationPolicyID: "integration-policy-a", IntegrationOwnerTask: "member-ref",
	})
	if err != nil {
		t.Fatalf("PrepareInitiative() error = %v", err)
	}
	if result.State != domain.InitiativePreparing || len(result.TaskHandles) != 1 ||
		result.TaskHandles[0] != "task-service-initiative" ||
		result.ManagedRunGroup.RegistrationNonce != "registration-nonce_initiative_group" ||
		result.ManagedRunGroup.Members[0].RegistrationNonce != "registration-nonce_initiative_member" {
		t.Fatalf("PrepareInitiative() = %#v", result)
	}
	detail, err := client.GetInitiative(context.Background(), "read-service-initiative", result.InitiativeHandle)
	if err != nil || detail.Initiative.Handle != result.InitiativeHandle ||
		len(detail.Graph.Nodes) != 1 || detail.Graph.Nodes[0].TaskHandle != "task-service-initiative" {
		t.Fatalf("GetInitiative() = %#v, %v", detail, err)
	}
	list, err := client.ListInitiatives(context.Background(), "list-service-initiatives", localapi.ListInitiativesInput{
		State: domain.InitiativePreparing,
	})
	if err != nil || len(list.Initiatives) != 1 || list.Initiatives[0].InitiativeHandle != result.InitiativeHandle {
		t.Fatalf("ListInitiatives() = %#v, %v", list, err)
	}
	backlog, err := client.ListBacklog(context.Background(), "list-service-backlog", localapi.ListBacklogInput{})
	if err != nil || len(backlog.Items) != 0 {
		t.Fatalf("ListBacklog() = %#v, %v", backlog, err)
	}
	if _, err := client.PauseInitiative(
		context.Background(), "pause-service-initiative-mcp",
		localapi.InitiativeControlInput{InitiativeHandle: result.InitiativeHandle},
	); err == nil {
		t.Fatal("MCP PauseInitiative() error = nil")
	} else {
		var failure *domain.Failure
		if !errors.As(err, &failure) || failure.Code != domain.ErrorUnauthorized {
			t.Fatalf("MCP PauseInitiative() error = %#v", err)
		}
	}
	operator, err := localapi.NewClient(operatorSocket, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	control, err := operator.PauseInitiative(
		context.Background(), "pause-service-initiative",
		localapi.InitiativeControlInput{InitiativeHandle: result.InitiativeHandle},
	)
	if err != nil || control.InitiativeHandle != result.InitiativeHandle || len(control.Members) != 1 ||
		control.Members[0].TaskHandle != "task-service-initiative" ||
		control.Members[0].Outcome != application.InitiativeControlRejected ||
		control.Members[0].ErrorCode != domain.ErrorPrecondition {
		t.Fatalf("operator PauseInitiative() = %#v, %v", control, err)
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("Run() error = %v", err)
	}
}
