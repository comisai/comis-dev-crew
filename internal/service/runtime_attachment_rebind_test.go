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

func TestRuntimeAttachmentCoordinator_RebindsResumeAndReplacementGenerations(t *testing.T) {
	root := shortTempDir(t)
	workspace := filepath.Join(root, "workspace")
	if err := os.Mkdir(workspace, 0o700); err != nil {
		t.Fatal(err)
	}
	store, err := sqlite.Open(context.Background(), filepath.Join(root, "state", "devcrew.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	now := time.Date(2026, time.August, 23, 18, 0, 0, 0, time.UTC)
	coordinator, err := newRuntimeAttachmentCoordinator(runtimeAttachmentCoordinatorConfig{
		RuntimeRoot: filepath.Join(root, "runtime"), Store: store, Clock: func() time.Time { return now },
		NewCredential:           func() (string, error) { return "rebind-credential-0123456789abcdef", nil },
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
		WorkerProfiles:     func(string, domain.TaskShape) error { return nil },
		ValidationProfiles: func(string, domain.TaskShape) error { return nil },
		Workspaces:         serviceWorkspacePreparer{root: workspace}, RuntimeAttachments: coordinator,
		TaskIDs:            func(string) (string, error) { return "task-runtime-rebind-0001", nil },
		RegistrationNonces: func() (string, error) { return "registration-nonce_runtime_rebind", nil },
		PreparationTTL:     time.Hour, Clock: func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	prepared, err := mutations.PrepareTask(context.Background(), application.PrepareTaskCommand{
		OperationID: "operation-runtime-rebind-0001", ServiceInstanceID: "service-instance-runtime-rebind",
		Shape: domain.ShapeScout, RepositoryID: "product-api", BaseRevision: strings.Repeat("b", 40),
		AcceptanceCriteria: []string{"Rebind the exact protected worker generation."},
		ValidationProfile:  "go-default", DeliveryMode: domain.DeliveryReport, WorkerProfileID: "codex-reviewed",
	})
	if err != nil || prepared.Preparation == nil {
		t.Fatalf("PrepareTask() = %#v, %v", prepared, err)
	}
	activated, err := mutations.ActivateManagedRun(context.Background(), application.ActivateManagedRunCommand{
		OperationID: "activate-runtime-rebind-0001", ServiceInstanceID: "service-instance-runtime-rebind",
		ManagedRunID: "managed-run.runtime-rebind", ExternalRunRef: prepared.Task.Handle,
		RegistrationNonce:     prepared.Preparation.RegistrationNonce,
		WorkspaceLeaseID:      "workspace-lease.runtime-rebind",
		ExecutionAttachmentID: "execution-attachment.runtime-rebind",
		AttachmentTargetName:  "attachment-0123456789abcdef0123456789abcdef.sock",
	})
	if err != nil {
		t.Fatal(err)
	}
	client, err := reporter.NewRuntimeClient(
		prepared.Preparation.RequestedAttachment.SourcePath,
		prepared.Preparation.RequestedAttachment.RelayIdentity, time.Second,
	)
	if err != nil {
		t.Fatal(err)
	}
	brief, err := client.Brief(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	resumeOperationID, err := application.RuntimeRelaunchAcknowledgementOperationID(
		prepared.Task.Handle, activated.Task.StateVersion,
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := coordinator.RebindRuntimeAttachmentLaunch(context.Background(), application.RuntimeAttachmentLaunchRebindRequest{
		TaskHandle: prepared.Task.Handle, ReadyStateVersion: activated.Task.StateVersion,
		LaunchOperationID: resumeOperationID, Brief: brief,
	}); err != nil {
		t.Fatalf("RebindRuntimeAttachmentLaunch(resume) error = %v", err)
	}
	replacement := activated.Task
	replacement.BriefRevision++
	replacement.WorkerProfileID = "claude-reviewed"
	replacement, err = replacement.PinBriefRevision()
	if err != nil {
		t.Fatal(err)
	}
	replacementBrief, err := replacement.RenderWorkerBrief()
	if err != nil {
		t.Fatal(err)
	}
	replacementOperationID, err := application.RuntimeRelaunchAcknowledgementOperationID(
		replacement.Handle, activated.Task.StateVersion+1,
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := coordinator.RebindRuntimeAttachmentLaunch(context.Background(), application.RuntimeAttachmentLaunchRebindRequest{
		TaskHandle: replacement.Handle, ReadyStateVersion: activated.Task.StateVersion + 1,
		LaunchOperationID: replacementOperationID, Brief: replacementBrief,
	}); err != nil {
		t.Fatalf("RebindRuntimeAttachmentLaunch(replacement) error = %v", err)
	}
	if got, err := client.Brief(context.Background()); err != nil || got != replacementBrief {
		t.Fatalf("Brief(replacement) = %#v, %v, want %#v", got, err, replacementBrief)
	}
	// The generation mutation is fail-closed at every authority boundary.
	//lint:ignore SA1012 The boundary test proves a nil context is refused.
	if err := coordinator.RebindRuntimeAttachmentLaunch(nil, application.RuntimeAttachmentLaunchRebindRequest{}); err == nil {
		t.Fatal("RebindRuntimeAttachmentLaunch(nil context) error = nil")
	}
	cancelled, stop := context.WithCancel(context.Background())
	stop()
	if err := coordinator.RebindRuntimeAttachmentLaunch(cancelled, application.RuntimeAttachmentLaunchRebindRequest{}); err == nil {
		t.Fatal("RebindRuntimeAttachmentLaunch(cancelled context) error = nil")
	}
	if err := coordinator.RebindRuntimeAttachmentLaunch(context.Background(), application.RuntimeAttachmentLaunchRebindRequest{
		TaskHandle: replacement.Handle, ReadyStateVersion: activated.Task.StateVersion + 1,
		LaunchOperationID: "launch-ack-forged-generation", Brief: replacementBrief,
	}); err == nil {
		t.Fatal("RebindRuntimeAttachmentLaunch(forged generation) error = nil")
	}
	missingOperationID, err := application.RuntimeRelaunchAcknowledgementOperationID(
		"task-runtime-rebind-missing", 3,
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := coordinator.RebindRuntimeAttachmentLaunch(context.Background(), application.RuntimeAttachmentLaunchRebindRequest{
		TaskHandle: "task-runtime-rebind-missing", ReadyStateVersion: 3,
		LaunchOperationID: missingOperationID, Brief: replacementBrief,
	}); err == nil {
		t.Fatal("RebindRuntimeAttachmentLaunch(missing task) error = nil")
	}
}
