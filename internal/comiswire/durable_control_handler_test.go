package comiswire_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/comisai/comis-dev-crew/internal/application"
	"github.com/comisai/comis-dev-crew/internal/comiswire"
	"github.com/comisai/comis-dev-crew/internal/domain"
	"github.com/comisai/comis-dev-crew/internal/store/sqlite"
)

func TestDurableControlHandler_ActivationReplaysAcrossRestartAndRejectsAlteration(t *testing.T) {
	harness := newDurableControlHarness(t, "task-activate")
	prepared := harness.prepare(t, "operation-prepare-activate")
	harness.now = harness.now.Add(time.Minute)
	lease := comiswire.WorkspaceLeaseID("workspace-lease-activate")
	attachmentID := comiswire.ExecutionAttachmentID("execution-attachment-activate")
	attachmentTarget := comiswire.AttachmentTargetName("attachment-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa.sock")
	params := comiswire.ActivateRequestParams{
		OperationID: "operation-activate", ManagedRunID: "managed-run-activate",
		ExternalRunRef:        comiswire.ExternalRunRef(prepared.ExternalRunRef),
		RegistrationNonce:     comiswire.RegistrationNonce(prepared.RegistrationNonce),
		WorkspaceLeaseID:      &lease,
		ExecutionAttachmentID: &attachmentID,
		AttachmentTargetName:  &attachmentTarget,
	}

	first, err := harness.handler.Activate(context.Background(), params)
	if err != nil {
		t.Fatalf("Activate() error = %v", err)
	}
	if first.ManagedRunID != params.ManagedRunID || first.ExternalRunRef != params.ExternalRunRef ||
		first.State != comiswire.ManagedRunStateActive || first.ActivatedAtMs != harness.now.UnixMilli() {
		t.Fatalf("Activate() = %#v, want exact durable acknowledgement", first)
	}
	harness.restart(t)
	replayed, err := harness.handler.Activate(context.Background(), params)
	if err != nil || !reflect.DeepEqual(replayed, first) {
		t.Fatalf("Activate(restart replay) = %#v, %v, want %#v", replayed, err, first)
	}
	task, err := harness.store.GetTask(context.Background(), prepared.ExternalRunRef)
	if err != nil || task.State != domain.TaskReady || task.ManagedRunID != string(params.ManagedRunID) ||
		task.WorkspaceLeaseID != string(lease) {
		t.Fatalf("durable activated task = %#v, %v", task, err)
	}

	altered := params
	altered.ManagedRunID = "managed-run-altered"
	if _, err := harness.handler.Activate(context.Background(), altered); !wireErrorKind(err, comiswire.ErrorKindReplayConflict) {
		t.Fatalf("Activate(altered replay) error = %v, want replay_conflict", err)
	}
}

func TestDurableControlHandler_ActivationValidatesPrivateJoinAndLeaseInvariant(t *testing.T) {
	tests := []struct {
		name   string
		id     string
		mutate func(*durableControlHarness, *comiswire.ActivateRequestParams)
		kind   comiswire.ErrorKind
	}{
		{name: "nonce", id: "nonce", mutate: func(_ *durableControlHarness, params *comiswire.ActivateRequestParams) {
			params.RegistrationNonce = "registration-nonce-wrong"
		}, kind: comiswire.ErrorKindPreconditionFailed},
		{name: "expired", id: "expired", mutate: func(harness *durableControlHarness, _ *comiswire.ActivateRequestParams) {
			harness.now = harness.now.Add(2 * time.Hour)
		}, kind: comiswire.ErrorKindPreconditionFailed},
		{name: "missing workspace lease", id: "missing-lease", mutate: func(_ *durableControlHarness, params *comiswire.ActivateRequestParams) {
			params.WorkspaceLeaseID = nil
		}, kind: comiswire.ErrorKindInvalidParams},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			harness := newDurableControlHarness(t, "task-"+test.id)
			prepared := harness.prepare(t, "operation-prepare-"+test.id)
			lease := comiswire.WorkspaceLeaseID("workspace-lease-" + test.id)
			params := comiswire.ActivateRequestParams{
				OperationID:           comiswire.OperationID("operation-activate-" + test.id),
				ManagedRunID:          comiswire.ManagedRunID("managed-run-" + test.id),
				ExternalRunRef:        comiswire.ExternalRunRef(prepared.ExternalRunRef),
				RegistrationNonce:     comiswire.RegistrationNonce(prepared.RegistrationNonce),
				WorkspaceLeaseID:      &lease,
				ExecutionAttachmentID: testExecutionAttachmentID("execution-attachment-" + test.id),
				AttachmentTargetName:  testAttachmentTargetName(),
			}
			test.mutate(harness, &params)
			if _, err := harness.handler.Activate(context.Background(), params); !wireErrorKind(err, test.kind) {
				t.Fatalf("Activate() error = %v, want %s", err, test.kind)
			}
			task, err := harness.store.GetTask(context.Background(), prepared.ExternalRunRef)
			if err != nil || task.State != domain.TaskPrepared || task.ManagedRunID != "" || task.WorkspaceLeaseID != "" {
				t.Fatalf("rejected activation changed task = %#v, %v", task, err)
			}
		})
	}
}

func TestDurableControlHandler_AbandonDispositionIsDurableAndClosed(t *testing.T) {
	for _, test := range []struct {
		disposition comiswire.AbandonDisposition
		wantState   domain.TaskState
	}{
		{disposition: comiswire.AbandonDispositionPreserve, wantState: domain.TaskPrepared},
		{disposition: comiswire.AbandonDispositionReapSafe, wantState: domain.TaskCancelled},
	} {
		t.Run(string(test.disposition), func(t *testing.T) {
			id := strings.ReplaceAll(string(test.disposition), "_", "-")
			harness := newDurableControlHarness(t, "task-abandon-"+id)
			prepared := harness.prepare(t, "operation-prepare-"+id)
			harness.now = harness.now.Add(time.Minute)
			params := comiswire.AbandonRequestParams{
				OperationID:       comiswire.OperationID("operation-abandon-" + id),
				ExternalRunRef:    comiswire.ExternalRunRef(prepared.ExternalRunRef),
				RegistrationNonce: comiswire.RegistrationNonce(prepared.RegistrationNonce),
				Reason:            comiswire.AbandonReasonOwnerCancelled, Disposition: test.disposition,
			}
			first, err := harness.handler.Abandon(context.Background(), params)
			if err != nil {
				t.Fatalf("Abandon() error = %v", err)
			}
			if first.ExternalRunRef != params.ExternalRunRef || first.State != comiswire.ManagedRunStateAbandoned ||
				first.Disposition != params.Disposition ||
				first.TerminalTransition != comiswire.AbandonTerminalTransitionUnboundPreparationAbandoned {
				t.Fatalf("Abandon() = %#v, want closed acknowledgement", first)
			}
			harness.restart(t)
			replayed, err := harness.handler.Abandon(context.Background(), params)
			if err != nil || !reflect.DeepEqual(replayed, first) {
				t.Fatalf("Abandon(restart replay) = %#v, %v, want %#v", replayed, err, first)
			}
			altered := params
			altered.Reason = comiswire.AbandonReasonServiceUnavailable
			if _, err := harness.handler.Abandon(context.Background(), altered); !wireErrorKind(err, comiswire.ErrorKindReplayConflict) {
				t.Fatalf("Abandon(altered replay) error = %v, want replay_conflict", err)
			}
			task, err := harness.store.GetTask(context.Background(), prepared.ExternalRunRef)
			if err != nil || task.State != test.wantState || task.ManagedRunID != "" || task.WorkspaceLeaseID != "" {
				t.Fatalf("abandoned task = %#v, %v, want %s and unbound", task, err, test.wantState)
			}
			stored, err := harness.store.GetManagedRunPreparation(context.Background(), task.Handle)
			if err != nil {
				t.Fatal(err)
			}
			if stored.State != application.PreparationAbandoned ||
				stored.AbandonReason != application.AbandonReasonOwnerCancelled ||
				stored.Disposition != application.AbandonDisposition(test.disposition) || stored.ClosedAt == nil {
				t.Fatalf("durable preparation closure = %#v", stored)
			}

			lease := comiswire.WorkspaceLeaseID("workspace-lease-after-abandon")
			_, activateErr := harness.handler.Activate(context.Background(), comiswire.ActivateRequestParams{
				OperationID:  comiswire.OperationID("operation-activate-after-" + id),
				ManagedRunID: "managed-run-after-abandon", ExternalRunRef: params.ExternalRunRef,
				RegistrationNonce: params.RegistrationNonce, WorkspaceLeaseID: &lease,
				ExecutionAttachmentID: testExecutionAttachmentID("execution-attachment-after-abandon"),
				AttachmentTargetName:  testAttachmentTargetName(),
			})
			if !wireErrorKind(activateErr, comiswire.ErrorKindPreconditionFailed) {
				t.Fatalf("Activate(after abandon) error = %v, want precondition_failed", activateErr)
			}
		})
	}
}

func TestDurableControlHandler_JoinsTerminalRunningAndExactWrapperAcknowledgement(t *testing.T) {
	harness := newDurableControlHarness(t, "task-terminal-join")
	harness.workerProfileID = "codex-test"
	prepared := harness.prepare(t, "operation-prepare-terminal-join")
	harness.now = harness.now.Add(time.Minute)
	lease := comiswire.WorkspaceLeaseID("workspace-lease-terminal-join")
	activation := comiswire.ActivateRequestParams{
		OperationID: "operation-activate-terminal-join", ManagedRunID: "managed-run-terminal-join",
		ExternalRunRef:    comiswire.ExternalRunRef(prepared.ExternalRunRef),
		RegistrationNonce: comiswire.RegistrationNonce(prepared.RegistrationNonce), WorkspaceLeaseID: &lease,
		ExecutionAttachmentID: testExecutionAttachmentID("execution-attachment-terminal-join"),
		AttachmentTargetName:  testAttachmentTargetName(),
	}
	if _, err := harness.handler.Activate(context.Background(), activation); err != nil {
		t.Fatal(err)
	}
	harness.now = harness.now.Add(time.Minute)
	started, err := harness.mutations.StartTask(context.Background(), application.StartTaskCommand{
		OperationID: "operation-start-terminal-join", TaskHandle: prepared.ExternalRunRef,
	})
	if err != nil || started.Task.State != domain.TaskLaunching {
		t.Fatalf("StartTask() = %#v, %v", started, err)
	}

	harness.now = harness.now.Add(time.Minute)
	terminal := comiswire.TerminalEventRequestParams{
		OperationID: "operation-terminal-running-join", ManagedRunID: activation.ManagedRunID,
		WorkspaceLeaseID: lease, TerminalSessionID: "terminal-session-join",
		Transition: comiswire.CapabilityTerminalTransitionRunning,
	}
	acknowledged, err := harness.handler.TerminalEvent(context.Background(), terminal)
	if err != nil || acknowledged.ManagedRunID != terminal.ManagedRunID ||
		acknowledged.TerminalSessionID != terminal.TerminalSessionID || acknowledged.Transition != terminal.Transition {
		t.Fatalf("TerminalEvent() = %#v, %v", acknowledged, err)
	}
	launching, err := harness.store.GetTask(context.Background(), prepared.ExternalRunRef)
	if err != nil || launching.State != domain.TaskLaunching {
		t.Fatalf("terminal running alone changed task = %#v, %v", launching, err)
	}

	harness.now = harness.now.Add(time.Minute)
	working, err := harness.mutations.AcknowledgeWorkerLaunch(context.Background(), application.AcknowledgeWorkerLaunchCommand{
		OperationID: "operation-wrapper-ack-join",
		Acknowledgement: application.LaunchAcknowledgement{
			TaskHandle: prepared.ExternalRunRef, ManagedRunID: string(activation.ManagedRunID),
			WorkspaceLeaseID: string(lease), WorkingDirectory: harness.workspaceRoot,
			BriefRevision: started.Task.BriefRevision, BriefRevisionHash: started.Task.BriefRevisionHash,
		},
	})
	if err != nil || working.Task.State != domain.TaskWorking {
		t.Fatalf("AcknowledgeWorkerLaunch() = %#v, %v", working, err)
	}

	harness.restart(t)
	replayed, err := harness.handler.TerminalEvent(context.Background(), terminal)
	if err != nil || replayed != acknowledged {
		t.Fatalf("TerminalEvent(restart replay) = %#v, %v, want %#v", replayed, err, acknowledged)
	}
	altered := terminal
	altered.Transition = comiswire.CapabilityTerminalTransitionExited
	if _, err := harness.handler.TerminalEvent(context.Background(), altered); !wireErrorKind(err, comiswire.ErrorKindReplayConflict) {
		t.Fatalf("TerminalEvent(altered replay) error = %v", err)
	}
	crossLease := terminal
	crossLease.OperationID = "operation-terminal-cross-lease"
	crossLease.WorkspaceLeaseID = "workspace-lease-other"
	if _, err := harness.handler.TerminalEvent(context.Background(), crossLease); !wireErrorKind(err, comiswire.ErrorKindPreconditionFailed) {
		t.Fatalf("TerminalEvent(cross lease) error = %v", err)
	}
}

func TestDurableControlHandler_TerminalExitNeverMeansSuccess(t *testing.T) {
	harness := newDurableControlHarness(t, "task-terminal-exit")
	harness.workerProfileID = "codex-test"
	prepared := harness.prepare(t, "operation-prepare-terminal-exit")
	harness.now = harness.now.Add(time.Minute)
	lease := comiswire.WorkspaceLeaseID("workspace-lease-terminal-exit")
	activation := comiswire.ActivateRequestParams{
		OperationID: "operation-activate-terminal-exit", ManagedRunID: "managed-run-terminal-exit",
		ExternalRunRef:    comiswire.ExternalRunRef(prepared.ExternalRunRef),
		RegistrationNonce: comiswire.RegistrationNonce(prepared.RegistrationNonce), WorkspaceLeaseID: &lease,
		ExecutionAttachmentID: testExecutionAttachmentID("execution-attachment-terminal-exit"),
		AttachmentTargetName:  testAttachmentTargetName(),
	}
	if _, err := harness.handler.Activate(context.Background(), activation); err != nil {
		t.Fatal(err)
	}
	harness.now = harness.now.Add(time.Minute)
	if _, err := harness.mutations.StartTask(context.Background(), application.StartTaskCommand{
		OperationID: "operation-start-terminal-exit", TaskHandle: prepared.ExternalRunRef,
	}); err != nil {
		t.Fatal(err)
	}
	harness.now = harness.now.Add(time.Minute)
	response, err := harness.handler.TerminalEvent(context.Background(), comiswire.TerminalEventRequestParams{
		OperationID: "operation-terminal-exited", ManagedRunID: activation.ManagedRunID,
		WorkspaceLeaseID: lease, TerminalSessionID: "terminal-session-exit",
		Transition: comiswire.CapabilityTerminalTransitionExited,
	})
	if err != nil || response.Transition != comiswire.CapabilityTerminalTransitionExited {
		t.Fatalf("TerminalEvent(exited) = %#v, %v", response, err)
	}
	task, err := harness.store.GetTask(context.Background(), prepared.ExternalRunRef)
	if err != nil || task.State != domain.TaskUnknown {
		t.Fatalf("exited task = %#v, %v, want unknown", task, err)
	}
}

type durableControlHarness struct {
	t                 *testing.T
	databasePath      string
	workspaceRoot     string
	now               time.Time
	store             *sqlite.Store
	mutations         *application.Mutations
	handler           *comiswire.DurableControlHandler
	nextTaskID        string
	nextNonce         string
	serviceInstanceID string
	workerProfileID   string
}

func newDurableControlHarness(t *testing.T, taskID string) *durableControlHarness {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	workspaceRoot := filepath.Join(root, "workspace")
	if err := os.Mkdir(workspaceRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	harness := &durableControlHarness{
		t: t, databasePath: filepath.Join(root, "devcrew.db"), workspaceRoot: workspaceRoot,
		now:        time.Date(2026, time.August, 9, 20, 0, 0, 0, time.UTC),
		nextTaskID: taskID, nextNonce: "registration-nonce-" + taskID,
		serviceInstanceID: "service-instance-control",
		workerProfileID:   "fixture-worker",
	}
	harness.open(t)
	t.Cleanup(func() { _ = harness.store.Close() })
	return harness
}

func (harness *durableControlHarness) open(t *testing.T) {
	t.Helper()
	store, err := sqlite.Open(context.Background(), harness.databasePath)
	if err != nil {
		t.Fatal(err)
	}
	mutations, err := application.NewMutations(application.MutationConfig{
		Store: store, Repositories: acceptingCatalog{},
		WorkerProfiles: func(string, domain.TaskShape) error { return nil }, ValidationProfiles: func(string) error { return nil },
		Workspaces:         acceptingWorkspace{root: harness.workspaceRoot},
		RuntimeAttachments: acceptingRuntimeAttachments{},
		TaskIDs:            func(string) (string, error) { return harness.nextTaskID, nil },
		RegistrationNonces: func() (string, error) { return harness.nextNonce, nil },
		PreparationTTL:     time.Hour, Clock: func() time.Time { return harness.now },
	})
	if err != nil {
		_ = store.Close()
		t.Fatal(err)
	}
	handler, err := comiswire.NewDurableControlHandler(comiswire.DurableControlHandlerConfig{
		Mutations: mutations, ServiceInstanceID: comiswire.ServiceInstanceID(harness.serviceInstanceID),
	})
	if err != nil {
		_ = store.Close()
		t.Fatal(err)
	}
	harness.store, harness.mutations, harness.handler = store, mutations, handler
}

func (harness *durableControlHarness) prepare(t *testing.T, operationID string) application.ManagedRunPreparation {
	t.Helper()
	result, err := harness.mutations.PrepareTask(context.Background(), application.PrepareTaskCommand{
		OperationID: operationID, ServiceInstanceID: harness.serviceInstanceID,
		Shape: domain.ShapeScout, RepositoryID: "fixture-repository", BaseRevision: "0123456789012345678901234567890123456789",
		AcceptanceCriteria: []string{"The deterministic fixture completes."},
		Constraints:        []string{"Do not broaden authority."}, ValidationProfile: "fixture-validation",
		DeliveryMode: domain.DeliveryReport, WorkerProfileID: harness.workerProfileID,
	})
	if err != nil || result.Preparation == nil {
		t.Fatalf("PrepareTask() = %#v, %v", result, err)
	}
	return *result.Preparation
}

func (harness *durableControlHarness) restart(t *testing.T) {
	t.Helper()
	if err := harness.store.Close(); err != nil {
		t.Fatal(err)
	}
	harness.open(t)
}

type acceptingCatalog struct{}

func (acceptingCatalog) ValidateRepository(context.Context, string) error { return nil }

type acceptingWorkspace struct{ root string }

func (workspace acceptingWorkspace) PrepareWorkspace(
	context.Context,
	application.WorkspacePreparationRequest,
) (application.PreparedWorkspace, error) {
	return application.PreparedWorkspace{CanonicalRoot: workspace.root}, nil
}

type acceptingRuntimeAttachments struct{}

func (acceptingRuntimeAttachments) PrepareRuntimeAttachment(
	_ context.Context,
	request application.RuntimeAttachmentPreparationRequest,
) (application.PreparedRuntimeAttachment, error) {
	return application.PreparedRuntimeAttachment{
		Kind:       application.RuntimeAttachmentUnixSocket,
		SourcePath: "/approved/runtime/" + request.TaskHandle + "/attachment.sock",
	}, nil
}

func (acceptingRuntimeAttachments) ReleaseRuntimeAttachment(context.Context, string) error {
	return nil
}

func (acceptingRuntimeAttachments) BindRuntimeAttachment(context.Context, application.RuntimeAttachmentBindingRequest) error {
	return nil
}

func wireErrorKind(err error, want comiswire.ErrorKind) bool {
	var failure comiswire.RPCError
	return errors.As(err, &failure) && failure.Kind == want
}

func testExecutionAttachmentID(value string) *comiswire.ExecutionAttachmentID {
	attachmentID := comiswire.ExecutionAttachmentID(value)
	return &attachmentID
}

func testAttachmentTargetName() *comiswire.AttachmentTargetName {
	target := comiswire.AttachmentTargetName("attachment-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa.sock")
	return &target
}
