package service

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/comisai/comis-dev-crew/internal/application"
	"github.com/comisai/comis-dev-crew/internal/domain"
	"github.com/comisai/comis-dev-crew/internal/reporter"
	"github.com/comisai/comis-dev-crew/internal/store/sqlite"
)

func TestRuntimeAttachmentCoordinator_PreparesServingTaskSocketUnderOwnedRoot(t *testing.T) {
	root := shortTempDir(t)
	runtimeRoot := filepath.Join(root, "runtime")
	workspace := filepath.Join(root, "workspace")
	if err := os.Mkdir(workspace, 0o700); err != nil {
		t.Fatal(err)
	}
	store, err := sqlite.Open(context.Background(), filepath.Join(root, "state", "devcrew.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	now := time.Date(2026, time.August, 10, 16, 0, 0, 0, time.UTC)
	coordinator, err := newRuntimeAttachmentCoordinator(runtimeAttachmentCoordinatorConfig{
		RuntimeRoot: runtimeRoot, Reports: store, Clock: func() time.Time { return now },
		NewCredential: func() (string, error) { return "runtime-credential-0123456789abcdef", nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- coordinator.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		if err := <-done; err != nil {
			t.Errorf("runtime coordinator stop error = %v", err)
		}
	})
	mutations, err := application.NewMutations(application.MutationConfig{
		Store: store, Repositories: serviceRepositoryCatalog{},
		Workspaces: serviceWorkspacePreparer{root: workspace}, RuntimeAttachments: coordinator,
		TaskIDs:            func(string) (string, error) { return "task-runtime-owned-0001", nil },
		RegistrationNonces: func() (string, error) { return "registration-nonce_runtime_owned", nil },
		PreparationTTL:     time.Hour, Clock: func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	prepared, err := mutations.PrepareTask(context.Background(), application.PrepareTaskCommand{
		OperationID: "operation-runtime-owned-0001", ServiceInstanceID: "service-instance-runtime-owned",
		Shape: domain.ShapeScout, RepositoryID: "product-api", BaseRevision: strings.Repeat("a", 40),
		AcceptanceCriteria: []string{"Serve the exact protected brief."}, ValidationProfile: "go-default",
		DeliveryMode: domain.DeliveryReport, WorkerProfileID: "codex-reviewed",
	})
	if err != nil || prepared.Preparation == nil {
		t.Fatalf("PrepareTask() = %#v, %v", prepared, err)
	}
	attachment := prepared.Preparation.RequestedAttachment
	if attachment.Kind != application.RuntimeAttachmentUnixSocket || filepath.Dir(filepath.Dir(attachment.SourcePath)) != runtimeRoot {
		t.Fatalf("prepared attachment = %#v, runtime root %q", attachment, runtimeRoot)
	}
	info, err := os.Lstat(attachment.SourcePath)
	if err != nil || info.Mode()&os.ModeSocket == 0 || info.Mode().Perm() != 0o600 {
		t.Fatalf("prepared socket = %#v, %v", info, err)
	}
	client, err := reporter.NewRuntimeClient(attachment.SourcePath, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	brief, err := client.Brief(context.Background())
	if err != nil || !strings.Contains(brief.Content, "taskHandle: task-runtime-owned-0001") {
		t.Fatalf("protected brief = %#v, %v", brief, err)
	}
}
