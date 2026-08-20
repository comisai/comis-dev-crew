package service

import (
	"context"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/comisai/comis-dev-crew/internal/domain"
	"github.com/comisai/comis-dev-crew/internal/localapi"
)

func TestRunComposesBacklogAdditionAndNormalPromotion(t *testing.T) {
	root := shortTempDir(t)
	mcpSocket := filepath.Join(root, "run", "mcp.sock")
	ready := make(chan struct{})
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	done := make(chan error, 1)
	go func() {
		done <- Run(ctx, Config{
			DatabasePath: filepath.Join(root, "state", "devcrew.db"),
			SocketPath:   filepath.Join(root, "run", "operator.sock"), MCPSocketPath: mcpSocket,
			ServiceInstanceID: "service-instance_a", Repositories: serviceRepositoryCatalog{},
			WorkerProfiles:     func(string, domain.TaskShape) error { return nil },
			ValidationProfiles: func(string, domain.TaskShape) error { return nil },
			Workspaces:         serviceWorkspacePreparer{root: "/approved/worktrees/task-service-backlog"},
			RuntimeAttachments: serviceRuntimeAttachments{},
			TaskIDs:            func(string) (string, error) { return "task-service-backlog", nil },
			RegistrationNonces: func() (string, error) { return "registration-nonce_service_backlog", nil },
			PreparationTTL:     time.Hour, Ready: func() { close(ready) },
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
	addInput := localapi.AddBacklogInput{
		RepositoryID: "product-api", Shape: domain.ShapeShip,
		RequestedOutcome: "Implement the bounded service request.", DependsOn: []string{},
		Priority: domain.BacklogPriorityHigh, Readiness: domain.BacklogReady,
		SourceConversationRef: "cv_abcdefghijklmnopqrstuvwxyz0123456789ABCDEFG",
	}
	added, err := client.AddBacklog(context.Background(), "operation-service-backlog-add", addInput)
	if err != nil {
		t.Fatalf("AddBacklog() error = %v", err)
	}
	if !strings.HasPrefix(added.Item.Handle, "backlog-") || added.Item.RepositoryID != addInput.RepositoryID ||
		added.Item.Readiness != domain.BacklogReady || added.StateVersion < 1 {
		t.Fatalf("AddBacklog() = %#v", added)
	}
	replayedAddition, err := client.AddBacklog(context.Background(), "operation-service-backlog-add", addInput)
	if err != nil || !reflect.DeepEqual(replayedAddition, added) {
		t.Fatalf("AddBacklog(replay) = %#v, %v, want %#v", replayedAddition, err, added)
	}
	promoteInput := localapi.PromoteBacklogInput{
		BacklogHandle: added.Item.Handle, BaseRevision: strings.Repeat("a", 40),
		AcceptanceCriteria: []string{"The service path is verified."}, Constraints: []string{},
		ValidationProfile: "go-default", DeliveryMode: domain.DeliveryPullRequest,
		WorkerProfileID: "fixture-worker",
	}
	promoted, err := client.PromoteBacklog(
		context.Background(), "operation-service-backlog-promote", promoteInput,
	)
	if err != nil {
		t.Fatalf("PromoteBacklog() error = %v", err)
	}
	if promoted.BacklogHandle != added.Item.Handle || promoted.Readiness != domain.BacklogPromoted ||
		promoted.TaskHandle != "task-service-backlog" || promoted.State != domain.TaskPrepared ||
		promoted.TaskStateVersion >= promoted.StateVersion ||
		promoted.ManagedRun.RegistrationNonce != "registration-nonce_service_backlog" {
		t.Fatalf("PromoteBacklog() = %#v", promoted)
	}
	replayedPromotion, err := client.PromoteBacklog(
		context.Background(), "operation-service-backlog-promote", promoteInput,
	)
	if err != nil || !reflect.DeepEqual(replayedPromotion, promoted) {
		t.Fatalf("PromoteBacklog(replay) = %#v, %v, want %#v", replayedPromotion, err, promoted)
	}
	backlog, err := client.ListBacklog(context.Background(), "operation-service-backlog-list", localapi.ListBacklogInput{})
	if err != nil || len(backlog.Items) != 1 || backlog.Items[0].Handle != added.Item.Handle ||
		backlog.Items[0].Readiness != domain.BacklogPromoted || backlog.StateVersion != promoted.StateVersion {
		t.Fatalf("ListBacklog() = %#v, %v", backlog, err)
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("Run() error = %v", err)
	}
}
