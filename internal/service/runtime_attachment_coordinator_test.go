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
		RuntimeRoot: runtimeRoot, Store: store, Clock: func() time.Time { return now },
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

func TestRuntimeAttachmentCoordinator_RecoversPreparedTaskSocketAfterRestart(t *testing.T) {
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
	now := time.Date(2026, time.August, 10, 16, 30, 0, 0, time.UTC)
	newCoordinator := func() *runtimeAttachmentCoordinator {
		coordinator, err := newRuntimeAttachmentCoordinator(runtimeAttachmentCoordinatorConfig{
			RuntimeRoot: runtimeRoot, Store: store, Clock: func() time.Time { return now },
			NewCredential: func() (string, error) { return "restart-credential-0123456789abcdef", nil },
		})
		if err != nil {
			t.Fatal(err)
		}
		return coordinator
	}
	first := newCoordinator()
	firstContext, stopFirst := context.WithCancel(context.Background())
	firstDone := make(chan error, 1)
	go func() { firstDone <- first.Run(firstContext) }()
	newMutations := func(coordinator *runtimeAttachmentCoordinator) *application.Mutations {
		mutations, err := application.NewMutations(application.MutationConfig{
			Store: store, Repositories: serviceRepositoryCatalog{},
			Workspaces: serviceWorkspacePreparer{root: workspace}, RuntimeAttachments: coordinator,
			TaskIDs:            func(string) (string, error) { return "task-runtime-restart-0001", nil },
			RegistrationNonces: func() (string, error) { return "registration-nonce_runtime_restart", nil },
			PreparationTTL:     time.Hour, Clock: func() time.Time { return now },
		})
		if err != nil {
			t.Fatal(err)
		}
		return mutations
	}
	mutations := newMutations(first)
	prepared, err := mutations.PrepareTask(context.Background(), application.PrepareTaskCommand{
		OperationID: "operation-runtime-restart-0001", ServiceInstanceID: "service-instance-runtime-restart",
		Shape: domain.ShapeScout, RepositoryID: "product-api", BaseRevision: strings.Repeat("b", 40),
		AcceptanceCriteria: []string{"Recover the exact protected reporter."}, ValidationProfile: "go-default",
		DeliveryMode: domain.DeliveryReport, WorkerProfileID: "codex-reviewed",
	})
	if err != nil || prepared.Preparation == nil {
		t.Fatalf("PrepareTask() = %#v, %v", prepared, err)
	}
	socketPath := prepared.Preparation.RequestedAttachment.SourcePath
	if _, err := mutations.ActivateManagedRun(context.Background(), application.ActivateManagedRunCommand{
		OperationID: "activate-runtime-restart-0001", ServiceInstanceID: "service-instance-runtime-restart",
		ManagedRunID: "managed-run.runtime-restart", ExternalRunRef: prepared.Task.Handle,
		RegistrationNonce: prepared.Preparation.RegistrationNonce, WorkspaceLeaseID: "workspace-lease.runtime-restart",
		ExecutionAttachmentID: "execution-attachment.runtime-restart",
		AttachmentTargetName:  "attachment-0123456789abcdef0123456789abcdef.sock",
	}); err != nil {
		t.Fatalf("ActivateManagedRun() error = %v", err)
	}
	if _, err := mutations.StartTask(context.Background(), application.StartTaskCommand{
		OperationID: "start-runtime-restart-0001", TaskHandle: prepared.Task.Handle,
	}); err != nil {
		t.Fatalf("StartTask() error = %v", err)
	}
	stopFirst()
	if err := <-firstDone; err != nil {
		t.Fatalf("first coordinator stop error = %v", err)
	}
	if _, err := os.Lstat(socketPath); !os.IsNotExist(err) {
		t.Fatalf("socket after first stop error = %v, want not exist", err)
	}

	second := newCoordinator()
	restartedMutations := newMutations(second)
	if err := second.SetRecoveryAcknowledger(restartedMutations); err != nil {
		t.Fatal(err)
	}
	secondContext, stopSecond := context.WithCancel(context.Background())
	secondDone := make(chan error, 1)
	go func() { secondDone <- second.Run(secondContext) }()
	t.Cleanup(func() {
		stopSecond()
		if err := <-secondDone; err != nil {
			t.Errorf("second coordinator stop error = %v", err)
		}
	})
	deadline := time.Now().Add(time.Second)
	for {
		info, statErr := os.Lstat(socketPath)
		if statErr == nil && info.Mode()&os.ModeSocket != 0 && info.Mode().Perm() == 0o600 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("recovered socket error = %v, info = %#v", statErr, info)
		}
		time.Sleep(10 * time.Millisecond)
	}
	client, err := reporter.NewRuntimeClient(socketPath, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	brief, err := client.Brief(context.Background())
	if err != nil || brief.RevisionHash != prepared.Task.BriefRevisionHash {
		t.Fatalf("recovered brief = %#v, %v", brief, err)
	}
	if err := client.Acknowledge(context.Background(), workspace); err != nil {
		t.Fatalf("recovered launch acknowledgement error = %v", err)
	}
	task, err := store.GetTask(context.Background(), prepared.Task.Handle)
	if err != nil || task.State != domain.TaskLaunching || task.StateVersion != 4 {
		t.Fatalf("task after recovered acknowledgement = %#v, %v", task, err)
	}
}

func TestRuntimeAttachmentCoordinator_RejectsIntermediateSymlinkWithoutCreatingOutside(t *testing.T) {
	root := shortTempDir(t)
	outside := filepath.Join(root, "outside")
	if err := os.Mkdir(outside, 0o700); err != nil {
		t.Fatal(err)
	}
	linked := filepath.Join(root, "linked")
	if err := os.Symlink(outside, linked); err != nil {
		t.Fatal(err)
	}
	store, err := sqlite.Open(context.Background(), filepath.Join(root, "state", "devcrew.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if _, err := newRuntimeAttachmentCoordinator(runtimeAttachmentCoordinatorConfig{
		RuntimeRoot: filepath.Join(linked, "tasks"), Store: store,
		Clock: time.Now, NewCredential: func() (string, error) { return "unused-credential-0123456789abcdef", nil },
	}); err == nil {
		t.Fatal("newRuntimeAttachmentCoordinator(symlinked root) error = nil")
	}
	if _, err := os.Lstat(filepath.Join(outside, "tasks")); !os.IsNotExist(err) {
		t.Fatalf("outside runtime root was created through symlink: %v", err)
	}
}
