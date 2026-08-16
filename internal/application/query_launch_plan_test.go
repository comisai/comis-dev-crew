package application

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/comisai/comis-dev-crew/internal/domain"
)

func TestQueries_GetLaunchPlanBuildsAndSafelyProjectsReviewedDescriptor(t *testing.T) {
	now := time.Date(2026, time.August, 10, 9, 30, 0, 0, time.UTC)
	workspace := t.TempDir()
	task := queryTask("task-launch-plan", domain.TaskReady, 7)
	task.ExecutionAttachmentID = "execution-attachment-0001"
	task.AttachmentTargetName = "attachment-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa.sock"
	repository := &queryRepository{
		tasks: []domain.Task{task},
		preparation: ManagedRunPreparation{
			ExternalRunRef: task.Handle, RequestedWorkspaceRoot: workspace,
			RequestedAttachment: PreparedRuntimeAttachment{RelayIdentity: strings.Repeat("ab", 32)},
			State:               PreparationOpen,
		},
	}
	adapter := &queryHarnessAdapter{}
	queries, err := NewQueries(QueryConfig{
		Repository: repository,
		Harnesses:  &queryHarnesses{adapter: adapter},
		Clock:      func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("NewQueries() error = %v", err)
	}

	plan, err := queries.GetLaunchPlan(context.Background(), task.Handle)
	if err != nil {
		t.Fatalf("GetLaunchPlan() error = %v", err)
	}
	if adapter.request.ProfileID != task.WorkerProfileID || adapter.request.Shape != task.Shape ||
		adapter.request.WorkingDirectory != workspace || adapter.request.TaskHandle != task.Handle ||
		adapter.request.ManagedRunID != task.ManagedRunID || adapter.request.WorkspaceLeaseID != task.WorkspaceLeaseID ||
		adapter.request.BriefRevision != task.BriefRevision || adapter.request.BriefRevisionHash != task.BriefRevisionHash ||
		adapter.request.Attachment.ExecutionAttachmentID != task.ExecutionAttachmentID ||
		adapter.request.Attachment.AttachmentTargetName != task.AttachmentTargetName ||
		adapter.request.Attachment.RelayIdentity != repository.preparation.RequestedAttachment.RelayIdentity ||
		adapter.request.Attachment.MountSocketPath != "/run/comis/attachments/"+task.AttachmentTargetName {
		t.Fatalf("BuildLaunchDescriptor() request = %#v, want exact durable launch binding", adapter.request)
	}
	if plan.SchemaVersion != 1 || plan.CapturedAtMs != now.UnixMilli() || plan.StateVersion != task.StateVersion ||
		plan.Completeness != CompletenessComplete || plan.TaskHandle != task.Handle || plan.State != domain.TaskReady ||
		plan.StateSource != StateSourceStore || plan.StateConfidence != ConfidenceVerified || plan.Freshness != FreshnessCurrent ||
		plan.WorkerProfileID != task.WorkerProfileID || plan.TerminalAllowEntryID != "terminal-codex-reviewed" ||
		plan.BriefRevisionHash != task.BriefRevisionHash || plan.AttachmentTargetName != task.AttachmentTargetName {
		t.Fatalf("GetLaunchPlan() = %#v, want exact safe reviewed projection", plan)
	}
	encoded, err := json.Marshal(plan)
	if err != nil {
		t.Fatalf("Marshal(plan) error = %v", err)
	}
	for _, required := range []string{
		`"managedRunId":"` + task.ManagedRunID + `"`,
		`"workspaceLeaseId":"` + task.WorkspaceLeaseID + `"`,
	} {
		if !strings.Contains(string(encoded), required) {
			t.Fatalf("launch plan omitted required managed terminal authority %q: %s", required, encoded)
		}
	}
	for _, forbidden := range []string{
		workspace, "/usr/local/bin/codex", "--model", "COMIS_EXECUTION_ATTACHMENT",
		task.ExecutionAttachmentID, repository.preparation.RequestedAttachment.RelayIdentity,
	} {
		if strings.Contains(string(encoded), forbidden) {
			t.Fatalf("launch plan leaked protected process material %q: %s", forbidden, encoded)
		}
	}
}

func TestQueries_GetLaunchPlanAllowsLaunchingRecoveryReread(t *testing.T) {
	now := time.Date(2026, time.August, 10, 9, 45, 0, 0, time.UTC)
	workspace := t.TempDir()
	task := queryTask("task-launch-plan-recovery", domain.TaskLaunching, 8)
	task.ExecutionAttachmentID = "execution-attachment-recovery"
	task.AttachmentTargetName = "attachment-bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb.sock"
	adapter := &queryHarnessAdapter{}
	queries, err := NewQueries(QueryConfig{
		Repository: &queryRepository{
			tasks: []domain.Task{task},
			preparation: ManagedRunPreparation{
				ExternalRunRef: task.Handle, RequestedWorkspaceRoot: workspace,
				RequestedAttachment: PreparedRuntimeAttachment{RelayIdentity: strings.Repeat("ab", 32)},
				State:               PreparationOpen,
			},
		},
		Harnesses: &queryHarnesses{adapter: adapter},
		Clock:     func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	plan, err := queries.GetLaunchPlan(context.Background(), task.Handle)
	if err != nil {
		t.Fatalf("GetLaunchPlan(launching recovery) error = %v", err)
	}
	if plan.State != domain.TaskLaunching || plan.StateVersion != task.StateVersion ||
		adapter.request.ManagedRunID != task.ManagedRunID || adapter.request.WorkspaceLeaseID != task.WorkspaceLeaseID {
		t.Fatalf("GetLaunchPlan(launching recovery) = %#v, request %#v", plan, adapter.request)
	}
}

func TestBuildWorkerLaunchDescriptorRejectsIncompleteDirectCallers(t *testing.T) {
	task := queryTask("task-launch-direct-boundary", domain.TaskReady, 9)
	task.ExecutionAttachmentID = "execution-attachment-direct"
	task.AttachmentTargetName = "attachment-eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee.sock"
	preparation := ManagedRunPreparation{
		ExternalRunRef: task.Handle, RequestedWorkspaceRoot: t.TempDir(), State: PreparationOpen,
	}
	harnesses := &queryHarnesses{adapter: &queryHarnessAdapter{}}
	//lint:ignore SA1012 The boundary test proves nil contexts fail closed.
	if _, err := BuildWorkerLaunchDescriptor(nil, task, preparation, harnesses); err == nil {
		t.Fatal("BuildWorkerLaunchDescriptor(nil context) error = nil")
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := BuildWorkerLaunchDescriptor(cancelled, task, preparation, harnesses); !errors.Is(err, context.Canceled) {
		t.Fatalf("BuildWorkerLaunchDescriptor(cancelled) error = %v", err)
	}
	invalid := task
	invalid.State = domain.TaskWorking
	if _, err := BuildWorkerLaunchDescriptor(context.Background(), invalid, preparation, harnesses); err == nil {
		t.Fatal("BuildWorkerLaunchDescriptor(working) error = nil")
	}
	if _, err := BuildWorkerLaunchDescriptor(context.Background(), task, preparation, nil); err == nil {
		t.Fatal("BuildWorkerLaunchDescriptor(no harnesses) error = nil")
	}
	if _, err := BuildWorkerLaunchDescriptor(context.Background(), task, preparation, &queryHarnesses{}); err == nil {
		t.Fatal("BuildWorkerLaunchDescriptor(nil adapter) error = nil")
	}
}

func TestQueries_GetLaunchPlanRejectsUnactivatedTaskWithoutCallingHarness(t *testing.T) {
	task := queryTask("task-not-activated", domain.TaskPrepared, 3)
	adapter := &queryHarnessAdapter{}
	queries, err := NewQueries(QueryConfig{
		Repository: &queryRepository{tasks: []domain.Task{task}},
		Harnesses:  &queryHarnesses{adapter: adapter},
		Clock:      time.Now,
	})
	if err != nil {
		t.Fatalf("NewQueries() error = %v", err)
	}
	if _, err := queries.GetLaunchPlan(context.Background(), task.Handle); failureCode(err) != domain.ErrorPrecondition {
		t.Fatalf("GetLaunchPlan(prepared) error = %v, want precondition", err)
	}
	if adapter.called {
		t.Fatal("GetLaunchPlan(prepared) called worker harness without activation authority")
	}
}

func TestQueries_GetLaunchPlanFailsClosedAcrossReadAndAdapterBoundaries(t *testing.T) {
	privateCause := errors.New("private launch adapter and workspace detail")
	readyTask := queryTask("task-launch-failures", domain.TaskReady, 8)
	readyTask.ExecutionAttachmentID = "execution-attachment-0002"
	readyTask.AttachmentTargetName = "attachment-bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb.sock"
	preparation := ManagedRunPreparation{
		ExternalRunRef: readyTask.Handle, RequestedWorkspaceRoot: t.TempDir(),
		RequestedAttachment: PreparedRuntimeAttachment{RelayIdentity: strings.Repeat("ab", 32)},
		State:               PreparationOpen,
	}
	tests := []struct {
		name       string
		handle     string
		repository *queryRepository
		harnesses  WorkerHarnessResolver
		wantCode   domain.ErrorCode
	}{
		{name: "invalid handle", handle: "../redirect", repository: &queryRepository{}, wantCode: domain.ErrorInvalidArgument},
		{name: "missing task", handle: readyTask.Handle, repository: &queryRepository{readErr: ErrNotFound}, wantCode: domain.ErrorNotFound},
		{name: "missing harness registry", handle: readyTask.Handle, repository: &queryRepository{tasks: []domain.Task{readyTask}}, wantCode: domain.ErrorUnavailable},
		{
			name: "private preparation failure", handle: readyTask.Handle,
			repository: &queryRepository{tasks: []domain.Task{readyTask}, preparationErr: privateCause},
			harnesses:  &queryHarnesses{adapter: &queryHarnessAdapter{}}, wantCode: domain.ErrorInternal,
		},
		{
			name: "profile resolution failure", handle: readyTask.Handle,
			repository: &queryRepository{tasks: []domain.Task{readyTask}, preparation: preparation},
			harnesses:  &queryHarnesses{err: privateCause}, wantCode: domain.ErrorUnavailable,
		},
		{
			name: "descriptor construction failure", handle: readyTask.Handle,
			repository: &queryRepository{tasks: []domain.Task{readyTask}, preparation: preparation},
			harnesses:  &queryHarnesses{adapter: &queryHarnessAdapter{err: privateCause}}, wantCode: domain.ErrorUnavailable,
		},
		{
			name: "descriptor deadline", handle: readyTask.Handle,
			repository: &queryRepository{tasks: []domain.Task{readyTask}, preparation: preparation},
			harnesses:  &queryHarnesses{adapter: &queryHarnessAdapter{err: context.DeadlineExceeded}}, wantCode: domain.ErrorDeadlineExceeded,
		},
		{
			name: "inconsistent descriptor", handle: readyTask.Handle,
			repository: &queryRepository{tasks: []domain.Task{readyTask}, preparation: preparation},
			harnesses:  &queryHarnesses{adapter: &queryHarnessAdapter{descriptor: &WorkerLaunchDescriptor{}}}, wantCode: domain.ErrorInternal,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			queries, err := NewQueries(QueryConfig{Repository: test.repository, Harnesses: test.harnesses, Clock: time.Now})
			if err != nil {
				t.Fatalf("NewQueries() error = %v", err)
			}
			_, err = queries.GetLaunchPlan(context.Background(), test.handle)
			if failureCode(err) != test.wantCode {
				t.Fatalf("GetLaunchPlan() error = %v, want %s", err, test.wantCode)
			}
			if strings.Contains(err.Error(), privateCause.Error()) {
				t.Fatalf("GetLaunchPlan() leaked private cause: %q", err)
			}
		})
	}
}
