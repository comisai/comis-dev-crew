package service

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/comisai/comis-dev-crew/internal/application"
	"github.com/comisai/comis-dev-crew/internal/comiswire"
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
		NewCredential:           func() (string, error) { return "runtime-credential-0123456789abcdef", nil },
		NewAttentionOperationID: runtimeAttentionOperationID,
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
	request := coordinator.entries[prepared.Task.Handle].request
	if replay, err := coordinator.PrepareRuntimeAttachment(context.Background(), request); err != nil || replay != attachment {
		t.Fatalf("prepared attachment replay = %#v, %v", replay, err)
	}
	altered := request
	altered.OperationID = "operation-runtime-owned-altered"
	if _, err := coordinator.PrepareRuntimeAttachment(context.Background(), altered); err == nil {
		t.Fatal("PrepareRuntimeAttachment(altered replay) error = nil")
	}
	coordinator.newCredential = func() (string, error) { return "", errors.New("credential unavailable") }
	credentialFailure := request
	credentialFailure.OperationID = "operation-runtime-owned-0002"
	credentialFailure.TaskHandle = "task-runtime-owned-0002"
	if _, err := coordinator.PrepareRuntimeAttachment(context.Background(), credentialFailure); err == nil {
		t.Fatal("PrepareRuntimeAttachment(credential failure) error = nil")
	}
}

func TestRuntimeAttachmentCoordinator_MapsOnlyExactPrivateComisResponse(t *testing.T) {
	root := shortTempDir(t)
	store, err := sqlite.Open(context.Background(), filepath.Join(root, "state", "devcrew.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	coordinator, err := newRuntimeAttachmentCoordinator(runtimeAttachmentCoordinatorConfig{
		RuntimeRoot: filepath.Join(root, "runtime"), Store: store, Clock: time.Now,
		NewCredential:           func() (string, error) { return "attention-credential-0123456789abcdef", nil },
		NewAttentionOperationID: runtimeAttentionOperationID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := coordinator.SetAttentionResponseReceiver(nil); err == nil {
		t.Fatal("SetAttentionResponseReceiver(nil) error = nil")
	}
	request := reporter.AttentionResponseRequest{
		OperationID: "attention-response-runtime-test", ManagedRunID: "managed-run.attention", ExternalKey: "database-choice",
	}
	if _, err := coordinator.ReceiveRuntimeAttentionResponse(context.Background(), request); err == nil {
		t.Fatal("ReceiveRuntimeAttentionResponse(unconfigured) error = nil")
	}
	//lint:ignore SA1012 This boundary test exercises explicit nil-context rejection.
	if _, err := coordinator.ReceiveRuntimeAttentionResponse(nil, request); err == nil {
		t.Fatal("ReceiveRuntimeAttentionResponse(nil context) error = nil")
	}
	private := "Use the existing PostgreSQL adapter."
	receiver := &runtimeAttentionReceiver{}
	if err := coordinator.SetAttentionResponseReceiver(receiver); err != nil {
		t.Fatal(err)
	}
	if err := coordinator.SetAttentionResponseReceiver(receiver); err == nil {
		t.Fatal("SetAttentionResponseReceiver(duplicate) error = nil")
	}
	receiver.result = comiswire.ReceiveAttentionResponseResponseResult{
		ManagedRunID: "managed-run.attention", ExternalKey: "database-choice", State: comiswire.ManagedRunStatePending,
	}
	response, err := coordinator.ReceiveRuntimeAttentionResponse(context.Background(), request)
	if err != nil || response.State != reporter.AttentionResponsePending || response.Response != "" {
		t.Fatalf("ReceiveRuntimeAttentionResponse(pending) = %#v, %v", response, err)
	}
	receiver.result.Response = &private
	if _, err := coordinator.ReceiveRuntimeAttentionResponse(context.Background(), request); err == nil {
		t.Fatal("ReceiveRuntimeAttentionResponse(pending content) error = nil")
	}
	receiver.result.State = comiswire.ManagedRunStateDelivered
	receiver.result.Response = nil
	if _, err := coordinator.ReceiveRuntimeAttentionResponse(context.Background(), request); err == nil {
		t.Fatal("ReceiveRuntimeAttentionResponse(delivered without content) error = nil")
	}
	receiver.result.State = comiswire.ManagedRunStateActive
	if _, err := coordinator.ReceiveRuntimeAttentionResponse(context.Background(), request); err == nil {
		t.Fatal("ReceiveRuntimeAttentionResponse(unknown state) error = nil")
	}
	receiver.result.State = comiswire.ManagedRunStateDelivered
	receiver.result.Response = &private
	response, err = coordinator.ReceiveRuntimeAttentionResponse(context.Background(), request)
	if err != nil || response.State != reporter.AttentionResponseDelivered || response.Response != private ||
		receiver.request.OperationID != "attention-response-runtime-test" || receiver.request.ManagedRunID != "managed-run.attention" ||
		receiver.request.ExternalKey != "database-choice" {
		t.Fatalf("ReceiveRuntimeAttentionResponse() = %#v, %v, request %#v", response, err, receiver.request)
	}
	receiver.result.ExternalKey = "other-choice"
	request.OperationID = "attention-response-runtime-test-2"
	if _, err := coordinator.ReceiveRuntimeAttentionResponse(context.Background(), request); err == nil {
		t.Fatal("ReceiveRuntimeAttentionResponse(identity drift) error = nil")
	}
	receiver.result.ExternalKey = "database-choice"
	receiver.err = errors.New("private downstream detail: " + private)
	request.OperationID = "attention-response-runtime-test-3"
	if _, err := coordinator.ReceiveRuntimeAttentionResponse(context.Background(), request); err == nil || strings.Contains(err.Error(), private) {
		t.Fatalf("private downstream error = %v", err)
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
			NewCredential:           func() (string, error) { return "restart-credential-0123456789abcdef", nil },
			NewAttentionOperationID: runtimeAttentionOperationID,
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
	binding := *first.entries[prepared.Task.Handle].binding
	if err := first.BindRuntimeAttachment(context.Background(), binding); err != nil {
		t.Fatalf("BindRuntimeAttachment(replay) error = %v", err)
	}
	alteredBinding := binding
	alteredBinding.AttachmentTargetName = "attachment-ffffffffffffffffffffffffffffffff.sock"
	if err := first.BindRuntimeAttachment(context.Background(), alteredBinding); err == nil {
		t.Fatal("BindRuntimeAttachment(altered replay) error = nil")
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
	if err := second.SetRecoveryAcknowledger(nil); err == nil {
		t.Fatal("SetRecoveryAcknowledger(nil) error = nil")
	}
	if err := second.SetRecoveryAcknowledger(restartedMutations); err != nil {
		t.Fatal(err)
	}
	if err := second.SetRecoveryAcknowledger(restartedMutations); err == nil {
		t.Fatal("SetRecoveryAcknowledger(duplicate) error = nil")
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

func TestRuntimeAttachmentCoordinator_SkipsCleanedTaskAfterWorkspaceRemoval(t *testing.T) {
	root := shortTempDir(t)
	runtimeRoot := filepath.Join(root, "runtime")
	now := time.Date(2026, time.August, 10, 16, 45, 0, 0, time.UTC)
	task := domain.Task{
		SchemaVersion: 1, Handle: "task-runtime-cleaned-0001", State: domain.TaskCleaned,
		ServiceInstanceID: "service-instance-runtime-cleaned",
		ManagedRunID:      "managed-run.runtime-cleaned", WorkspaceLeaseID: "workspace-lease.runtime-cleaned",
		Shape: domain.ShapeScout, RepositoryID: "product-api", BaseRevision: strings.Repeat("c", 40),
		BriefRevision: 1, AcceptanceCriteria: []string{"Retain no reporter after cleanup."},
		ValidationProfile: "go-default", DeliveryMode: domain.DeliveryReport,
		WorkerProfileID: "codex-reviewed", StateVersion: 2, CreatedAt: now, UpdatedAt: now,
	}
	task, err := task.PinBriefRevision()
	if err != nil {
		t.Fatal(err)
	}
	cleanedRuntimeRoot := filepath.Join(runtimeRoot, task.Handle)
	if err := os.MkdirAll(cleanedRuntimeRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	store := &runtimeAttachmentRecoveryStore{tasks: []domain.Task{task}}
	coordinator, err := newRuntimeAttachmentCoordinator(runtimeAttachmentCoordinatorConfig{
		RuntimeRoot: runtimeRoot, Store: store, Clock: func() time.Time { return now },
		NewCredential:           func() (string, error) { return "unused-cleaned-credential-0123456789", nil },
		NewAttentionOperationID: runtimeAttentionOperationID,
	})
	if err != nil {
		t.Fatal(err)
	}
	servers, err := coordinator.recoverRuntimeAttachments(context.Background())
	if err != nil || len(servers) != 0 {
		t.Fatalf("recoverRuntimeAttachments(cleaned) = %d servers, %v", len(servers), err)
	}
	if store.preparationReads != 0 {
		t.Fatalf("cleaned task preparation reads = %d, want 0", store.preparationReads)
	}
	if _, err := os.Lstat(cleanedRuntimeRoot); !os.IsNotExist(err) {
		t.Fatalf("cleaned task runtime root error = %v, want not exist", err)
	}
}

func TestRuntimeAttachmentCoordinator_ReleasesOneLiveSocketWithoutStoppingSupervisor(t *testing.T) {
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
	coordinator, err := newRuntimeAttachmentCoordinator(runtimeAttachmentCoordinatorConfig{
		RuntimeRoot: runtimeRoot, Store: store, Clock: time.Now,
		NewCredential:           func() (string, error) { return "release-credential-0123456789abcdef", nil },
		NewAttentionOperationID: runtimeAttentionOperationID,
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	consumed := false
	go func() { done <- coordinator.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		if !consumed {
			if err := <-done; err != nil {
				t.Errorf("runtime coordinator stop error = %v", err)
			}
		}
	})
	firstRequest := runtimeAttachmentRequest(t, workspace, "task-runtime-release-0001")
	first, err := coordinator.PrepareRuntimeAttachment(context.Background(), firstRequest)
	if err != nil {
		t.Fatal(err)
	}
	secondRequest := runtimeAttachmentRequest(t, workspace, "task-runtime-release-0002")
	secondRequest.OperationID = "operation-runtime-release-0002"
	second, err := coordinator.PrepareRuntimeAttachment(context.Background(), secondRequest)
	if err != nil {
		t.Fatal(err)
	}
	if err := coordinator.ReleaseRuntimeAttachment(context.Background(), firstRequest.TaskHandle); err != nil {
		t.Fatalf("ReleaseRuntimeAttachment() error = %v", err)
	}
	if err := coordinator.ReleaseRuntimeAttachment(context.Background(), firstRequest.TaskHandle); err != nil {
		t.Fatalf("ReleaseRuntimeAttachment(replay) error = %v", err)
	}
	if _, err := os.Lstat(first.SourcePath); !os.IsNotExist(err) {
		t.Fatalf("released socket error = %v, want not exist", err)
	}
	if _, err := os.Lstat(filepath.Dir(first.SourcePath)); !os.IsNotExist(err) {
		t.Fatalf("released runtime directory error = %v, want not exist", err)
	}
	client, err := reporter.NewRuntimeClient(second.SourcePath, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.Brief(context.Background()); err != nil {
		t.Fatalf("retained sibling brief error = %v", err)
	}
	time.Sleep(20 * time.Millisecond)
	select {
	case runErr := <-done:
		consumed = true
		t.Fatalf("runtime coordinator stopped after exact release: %v", runErr)
	default:
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
		NewAttentionOperationID: runtimeAttentionOperationID,
	}); err == nil {
		t.Fatal("newRuntimeAttachmentCoordinator(symlinked root) error = nil")
	}
	if _, err := os.Lstat(filepath.Join(outside, "tasks")); !os.IsNotExist(err) {
		t.Fatalf("outside runtime root was created through symlink: %v", err)
	}
}

func TestRuntimeAttachmentCoordinator_RejectsInvalidLifecycleAndFilesystemBoundaries(t *testing.T) {
	root := shortTempDir(t)
	workspace := filepath.Join(root, "workspace")
	if err := os.Mkdir(workspace, 0o700); err != nil {
		t.Fatal(err)
	}
	store, err := sqlite.Open(context.Background(), filepath.Join(root, "state", "devcrew.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if _, err := newRuntimeAttachmentCoordinator(runtimeAttachmentCoordinatorConfig{}); err == nil {
		t.Fatal("newRuntimeAttachmentCoordinator(empty) error = nil")
	}
	if _, err := newRuntimeAttachmentCoordinator(runtimeAttachmentCoordinatorConfig{
		RuntimeRoot: "relative", Store: store, Clock: time.Now,
		NewCredential:           func() (string, error) { return "unused-credential-0123456789abcdef", nil },
		NewAttentionOperationID: runtimeAttentionOperationID,
	}); err == nil {
		t.Fatal("newRuntimeAttachmentCoordinator(relative root) error = nil")
	}
	unsafeRoot := filepath.Join(root, "unsafe")
	if err := os.Mkdir(unsafeRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := ensureOwnedRuntimeRoot(unsafeRoot); err == nil {
		t.Fatal("ensureOwnedRuntimeRoot(public mode) error = nil")
	}
	nonDirectory := filepath.Join(root, "regular")
	if err := os.WriteFile(nonDirectory, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := createRuntimeDirectoryPath(filepath.Join(nonDirectory, "child")); err == nil {
		t.Fatal("createRuntimeDirectoryPath(non-directory) error = nil")
	}
	if err := ensureTaskRuntimeDirectory(filepath.Join(root, "missing", "task")); err == nil {
		t.Fatal("ensureTaskRuntimeDirectory(missing parent) error = nil")
	}
	if err := ensureTaskRuntimeDirectory(nonDirectory); err == nil {
		t.Fatal("ensureTaskRuntimeDirectory(regular file) error = nil")
	}

	coordinator, err := newRuntimeAttachmentCoordinator(runtimeAttachmentCoordinatorConfig{
		RuntimeRoot: filepath.Join(root, "runtime"), Store: store, Clock: time.Now,
		NewCredential:           func() (string, error) { return "boundary-credential-0123456789abcdef", nil },
		NewAttentionOperationID: runtimeAttentionOperationID,
	})
	if err != nil {
		t.Fatal(err)
	}
	//lint:ignore SA1012 Boundary tests require explicit nil-context rejection.
	if err := coordinator.Run(nil); err == nil {
		t.Fatal("Run(nil) error = nil")
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if err := coordinator.Run(cancelled); !errors.Is(err, context.Canceled) {
		t.Fatalf("Run(cancelled) error = %v", err)
	}
	//lint:ignore SA1012 Boundary tests require explicit nil-context rejection.
	if _, err := coordinator.PrepareRuntimeAttachment(nil, application.RuntimeAttachmentPreparationRequest{}); err == nil {
		t.Fatal("PrepareRuntimeAttachment(nil) error = nil")
	}
	if _, err := coordinator.PrepareRuntimeAttachment(cancelled, application.RuntimeAttachmentPreparationRequest{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("PrepareRuntimeAttachment(cancelled) error = %v", err)
	}
	if _, err := coordinator.PrepareRuntimeAttachment(context.Background(), application.RuntimeAttachmentPreparationRequest{}); err == nil {
		t.Fatal("PrepareRuntimeAttachment(invalid) error = nil")
	}
	//lint:ignore SA1012 Boundary tests require explicit nil-context rejection.
	if err := coordinator.BindRuntimeAttachment(nil, application.RuntimeAttachmentBindingRequest{}); err == nil {
		t.Fatal("BindRuntimeAttachment(nil) error = nil")
	}
	if err := coordinator.BindRuntimeAttachment(cancelled, application.RuntimeAttachmentBindingRequest{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("BindRuntimeAttachment(cancelled) error = %v", err)
	}
	//lint:ignore SA1012 Boundary tests require explicit nil-context rejection.
	if err := coordinator.ReleaseRuntimeAttachment(nil, "task-boundary-release"); err == nil {
		t.Fatal("ReleaseRuntimeAttachment(nil) error = nil")
	}
	if err := coordinator.ReleaseRuntimeAttachment(cancelled, "task-boundary-release"); !errors.Is(err, context.Canceled) {
		t.Fatalf("ReleaseRuntimeAttachment(cancelled) error = %v", err)
	}
	if err := coordinator.ReleaseRuntimeAttachment(context.Background(), "invalid task"); err == nil {
		t.Fatal("ReleaseRuntimeAttachment(invalid task) error = nil")
	}
	waitingBinding := application.RuntimeAttachmentBindingRequest{
		TaskHandle: "task-boundary-waiting", ManagedRunID: "managed-run.boundary",
		WorkspaceLeaseID: "workspace-lease.boundary", ExecutionAttachmentID: "execution-attachment.boundary",
		AttachmentTargetName: "attachment-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa.sock",
		LaunchOperationID:    "launch-ack-boundary", Acknowledger: runtimeAttachmentAcknowledger{},
	}
	waitContext, stopWaiting := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer stopWaiting()
	if err := coordinator.BindRuntimeAttachment(waitContext, waitingBinding); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("BindRuntimeAttachment(waiting recovery) error = %v", err)
	}
	releaseWaitContext, stopReleaseWaiting := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer stopReleaseWaiting()
	if err := coordinator.ReleaseRuntimeAttachment(releaseWaitContext, "task-boundary-release"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("ReleaseRuntimeAttachment(waiting recovery) error = %v", err)
	}
	linkedWorkspace := filepath.Join(root, "linked-workspace")
	if err := os.Symlink(workspace, linkedWorkspace); err != nil {
		t.Fatal(err)
	}
	invalidWorkspace := runtimeAttachmentRequest(t, workspace, "task-boundary-workspace")
	invalidWorkspace.WorkingDirectory = linkedWorkspace
	if err := validateRuntimeAttachmentPreparation(invalidWorkspace); err == nil {
		t.Fatal("validateRuntimeAttachmentPreparation(symlinked workspace) error = nil")
	}
	coordinator.recoveryErr = errors.New("recovery unavailable")
	close(coordinator.recoveryReady)
	if _, err := coordinator.PrepareRuntimeAttachment(context.Background(), runtimeAttachmentRequest(t, workspace, "task-boundary-recovery")); err == nil {
		t.Fatal("PrepareRuntimeAttachment(recovery failure) error = nil")
	}
	if err := coordinator.ReleaseRuntimeAttachment(context.Background(), "task-boundary-release"); err == nil {
		t.Fatal("ReleaseRuntimeAttachment(recovery failure) error = nil")
	}
	coordinator.entries["task-boundary-populated"] = &runtimeAttachmentEntry{}
	if _, err := coordinator.recoverRuntimeAttachments(context.Background()); err == nil {
		t.Fatal("recoverRuntimeAttachments(populated) error = nil")
	}
	if err := closeRuntimeServers(nil); err != nil {
		t.Fatalf("closeRuntimeServers(nil) error = %v", err)
	}
}

func TestRuntimeAttachmentCoordinator_PreservesNonEmptyReleasedTaskRoot(t *testing.T) {
	root := shortTempDir(t)
	store, err := sqlite.Open(context.Background(), filepath.Join(root, "state", "devcrew.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	coordinator, err := newRuntimeAttachmentCoordinator(runtimeAttachmentCoordinatorConfig{
		RuntimeRoot: filepath.Join(root, "runtime"), Store: store, Clock: time.Now,
		NewCredential:           func() (string, error) { return "release-boundary-credential-0123456789", nil },
		NewAttentionOperationID: runtimeAttentionOperationID,
	})
	if err != nil {
		t.Fatal(err)
	}
	close(coordinator.recoveryReady)
	taskHandle := "task-release-non-empty-root"
	taskRoot := filepath.Join(coordinator.runtimeRoot, taskHandle)
	if err := os.Mkdir(taskRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(taskRoot, "unexpected")
	if err := os.WriteFile(marker, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := coordinator.ReleaseRuntimeAttachment(context.Background(), taskHandle); err == nil {
		t.Fatal("ReleaseRuntimeAttachment(non-empty root) error = nil")
	}
	if _, err := os.Lstat(marker); err != nil {
		t.Fatalf("non-empty task root marker was removed: %v", err)
	}
}

func TestRuntimeAttachmentCoordinator_ClosesSocketsOnRegistrationFailures(t *testing.T) {
	root := shortTempDir(t)
	workspace := filepath.Join(root, "workspace")
	if err := os.Mkdir(workspace, 0o700); err != nil {
		t.Fatal(err)
	}
	store, err := sqlite.Open(context.Background(), filepath.Join(root, "state", "devcrew.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	newPending := func(name string) *runtimeAttachmentCoordinator {
		coordinator, err := newRuntimeAttachmentCoordinator(runtimeAttachmentCoordinatorConfig{
			RuntimeRoot: filepath.Join(root, name), Store: store, Clock: time.Now,
			NewCredential:           func() (string, error) { return "registration-credential-0123456789abcdef", nil },
			NewAttentionOperationID: runtimeAttentionOperationID,
		})
		if err != nil {
			t.Fatal(err)
		}
		close(coordinator.recoveryReady)
		return coordinator
	}
	request := runtimeAttachmentRequest(t, workspace, "task-registration-failure")
	if _, err := newPending(strings.Repeat("long-runtime-root-", 5)).PrepareRuntimeAttachment(context.Background(), request); err == nil {
		t.Fatal("PrepareRuntimeAttachment(overlong socket) error = nil")
	}

	t.Run("registration rejected", func(t *testing.T) {
		coordinator := newPending("rejected")
		server := make(chan *reporter.RuntimeServer, 1)
		go func() {
			registration := <-coordinator.registrations
			server <- registration.server
			registration.ready <- errors.New("registration rejected")
		}()
		if _, err := coordinator.PrepareRuntimeAttachment(context.Background(), request); err == nil {
			t.Fatal("PrepareRuntimeAttachment(rejected registration) error = nil")
		}
		if err := closeRuntimeServers([]*reporter.RuntimeServer{<-server}); err != nil {
			t.Fatalf("closeRuntimeServers() error = %v", err)
		}
	})

	t.Run("registration send cancelled", func(t *testing.T) {
		coordinator := newPending("send-cancelled")
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
		defer cancel()
		if _, err := coordinator.PrepareRuntimeAttachment(ctx, request); !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("PrepareRuntimeAttachment(send cancelled) error = %v", err)
		}
	})

	t.Run("registration acknowledgement cancelled", func(t *testing.T) {
		coordinator := newPending("ack-cancelled")
		go func() { <-coordinator.registrations }()
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
		defer cancel()
		if _, err := coordinator.PrepareRuntimeAttachment(ctx, request); !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("PrepareRuntimeAttachment(ack cancelled) error = %v", err)
		}
	})

	coordinator := newPending("binding")
	if err := coordinator.BindRuntimeAttachment(context.Background(), application.RuntimeAttachmentBindingRequest{}); err == nil {
		t.Fatal("BindRuntimeAttachment(invalid) error = nil")
	}
	missingBinding := application.RuntimeAttachmentBindingRequest{
		TaskHandle: "task-registration-missing", ManagedRunID: "managed-run.registration",
		WorkspaceLeaseID: "workspace-lease.registration", ExecutionAttachmentID: "execution-attachment.registration",
		AttachmentTargetName: "attachment-eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee.sock",
		LaunchOperationID:    "launch-ack-registration", Acknowledger: runtimeAttachmentAcknowledger{},
	}
	if err := coordinator.BindRuntimeAttachment(context.Background(), missingBinding); err == nil {
		t.Fatal("BindRuntimeAttachment(missing preparation) error = nil")
	}
}

func runtimeAttachmentRequest(t *testing.T, workspace, taskHandle string) application.RuntimeAttachmentPreparationRequest {
	t.Helper()
	now := time.Date(2026, time.August, 10, 17, 0, 0, 0, time.UTC)
	task := domain.Task{
		SchemaVersion: 1, Handle: taskHandle, ServiceInstanceID: "service-instance-registration",
		State: domain.TaskPrepared, Shape: domain.ShapeScout, RepositoryID: "product-api",
		BaseRevision: strings.Repeat("a", 40), BriefRevision: 1,
		AcceptanceCriteria: []string{"Close the failed reporter socket."}, ValidationProfile: "go-default",
		DeliveryMode: domain.DeliveryReport, WorkerProfileID: "codex-reviewed",
		StateVersion: 1, CreatedAt: now, UpdatedAt: now,
	}
	task, err := task.PinBriefRevision()
	if err != nil {
		t.Fatal(err)
	}
	brief, err := task.RenderWorkerBrief()
	if err != nil {
		t.Fatal(err)
	}
	return application.RuntimeAttachmentPreparationRequest{
		OperationID: "operation-registration-failure", TaskHandle: task.Handle,
		BriefRevision: task.BriefRevision, BriefRevisionHash: task.BriefRevisionHash,
		Brief: brief, WorkingDirectory: workspace,
	}
}

type runtimeAttachmentRecoveryStore struct {
	tasks            []domain.Task
	preparationReads int
	cleanupRecord    application.TaskCleanupRecord
	cleanupFound     bool
	cleanupReads     int
	cleanupErr       error
}

func (store *runtimeAttachmentRecoveryStore) ListTasks(context.Context) ([]domain.Task, error) {
	return append([]domain.Task(nil), store.tasks...), nil
}

func (store *runtimeAttachmentRecoveryStore) GetManagedRunPreparation(
	context.Context,
	string,
) (application.ManagedRunPreparation, error) {
	store.preparationReads++
	return application.ManagedRunPreparation{}, errors.New("cleaned task preparation must not be read")
}

func (store *runtimeAttachmentRecoveryStore) GetTaskCleanupRecord(
	context.Context,
	string,
) (application.TaskCleanupRecord, bool, error) {
	store.cleanupReads++
	return store.cleanupRecord, store.cleanupFound, store.cleanupErr
}

func (*runtimeAttachmentRecoveryStore) CommitReport(
	context.Context,
	application.ReportMutation,
) (domain.ReportReceipt, error) {
	return domain.ReportReceipt{}, nil
}

type runtimeAttachmentAcknowledger struct{}

type runtimeAttentionReceiver struct {
	request comiswire.ReceiveAttentionResponseRequestParams
	result  comiswire.ReceiveAttentionResponseResponseResult
	err     error
}

func (receiver *runtimeAttentionReceiver) ReceiveAttentionResponse(
	_ context.Context,
	request comiswire.ReceiveAttentionResponseRequestParams,
) (comiswire.ReceiveAttentionResponseResponseResult, error) {
	receiver.request = request
	return receiver.result, receiver.err
}

func runtimeAttentionOperationID() (string, error) {
	return "attention-response-runtime-test", nil
}

func (runtimeAttachmentAcknowledger) AcknowledgeWorkerLaunch(
	context.Context,
	application.AcknowledgeWorkerLaunchCommand,
) (application.MutationResult, error) {
	return application.MutationResult{}, nil
}
