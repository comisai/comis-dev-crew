package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/comisai/comis-dev-crew/internal/application"
	"github.com/comisai/comis-dev-crew/internal/domain"
	"github.com/comisai/comis-dev-crew/internal/localapi"
)

func TestRun_PrepareTaskReadsStrictJSONAndUsesStableOperation(t *testing.T) {
	client := fixtureClient()
	preparation := application.ManagedRunPreparation{
		ExternalRunRef: "task-prepare-cli", RegistrationNonce: "registration-nonce_cli",
		ExpiresAt: time.Date(2026, time.August, 9, 21, 0, 0, 0, time.UTC), State: application.PreparationOpen,
	}
	client.prepared = localapi.PrepareTaskResult{
		SchemaVersion: 1, OperationID: "operation-prepare-cli", TaskHandle: "task-prepare-cli",
		State: domain.TaskPrepared, StateVersion: 9, SideEffect: localapi.SideEffectMutate,
		ManagedRun: preparation,
	}
	contract := `{"shape":"scout","repositoryId":"product-api","baseRevision":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","acceptanceCriteria":["Return one report."],"constraints":["Do not deliver."],"validationProfile":"go-default","deliveryMode":"report","workerProfileId":"fixture-worker"}`
	config := testConfig(client)
	config.Stdin = strings.NewReader(contract)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := Run(context.Background(), []string{
		"task", "prepare", "--input", "-", "--operation", "operation-prepare-cli", "--format", "json",
	}, &stdout, &stderr, config)
	if exitCode != ExitSuccess {
		t.Fatalf("Run() exit = %d, stderr = %q", exitCode, stderr.String())
	}
	if len(client.calls) != 1 || client.calls[0] != "prepare:product-api" || client.operationID != "operation-prepare-cli" {
		t.Fatalf("client calls/operation = %v/%q", client.calls, client.operationID)
	}
	var result localapi.PrepareTaskResult
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("prepare JSON = %q: %v", stdout.String(), err)
	}
	if result != client.prepared {
		t.Fatalf("prepare JSON = %#v, want %#v", result, client.prepared)
	}
}

func TestRun_PrepareTaskRejectsAuthorityFieldsBeforeConnecting(t *testing.T) {
	config := testConfig(fixtureClient())
	config.Stdin = strings.NewReader(`{"shape":"scout","repositoryId":"product-api","baseRevision":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","acceptanceCriteria":["Return one report."],"constraints":[],"validationProfile":"go-default","deliveryMode":"report","workerProfileId":"fixture-worker","serviceInstanceId":"forged"}`)
	called := false
	config.NewClient = func(string) (ReadClient, error) {
		called = true
		return fixtureClient(), nil
	}
	var stderr bytes.Buffer
	if exit := Run(context.Background(), []string{"task", "prepare", "--input", "-", "--format", "json"}, io.Discard, &stderr, config); exit != ExitUsage {
		t.Fatalf("Run() exit = %d, stderr = %q", exit, stderr.String())
	}
	if called {
		t.Fatal("client factory called for forged task contract")
	}
}

func TestRun_HandbackValidatesDeveloperWorkThroughCanonicalClient(t *testing.T) {
	client := fixtureClient()
	client.taskMutation = localapi.TaskMutationResult{
		SchemaVersion: 1, OperationID: "operation-handback-cli", TaskHandle: "task-0001",
		State: domain.TaskValidating, StateVersion: 14, SideEffect: localapi.SideEffectMutate,
	}
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := Run(context.Background(), []string{
		"task", "handback", "task-0001", "--action", "validate-developer-work",
		"--operation", "operation-handback-cli", "--format", "json",
	}, &stdout, &stderr, testConfig(client))
	if exitCode != ExitSuccess {
		t.Fatalf("Run() exit = %d, stderr = %q", exitCode, stderr.String())
	}
	if len(client.calls) != 1 || client.calls[0] != "handback:task-0001:validate-developer-work" ||
		client.operationID != "operation-handback-cli" {
		t.Fatalf("client calls/operation = %v/%q", client.calls, client.operationID)
	}
	var result localapi.TaskMutationResult
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil || result != client.taskMutation {
		t.Fatalf("handback JSON = %#v, %v", result, err)
	}
}

func TestRun_ReconcilesCleanUnknownCandidateThroughCanonicalClient(t *testing.T) {
	client := fixtureClient()
	client.taskMutation = localapi.TaskMutationResult{
		SchemaVersion: 1, OperationID: "operation-reconcile-cli", TaskHandle: "task-0001",
		State: domain.TaskValidating, StateVersion: 16, SideEffect: localapi.SideEffectMutate,
	}
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := Run(context.Background(), []string{
		"task", "reconcile", "task-0001", "--action", "validate-clean-candidate",
		"--operation", "operation-reconcile-cli", "--format", "json",
	}, &stdout, &stderr, testConfig(client))
	if exitCode != ExitSuccess {
		t.Fatalf("Run() exit = %d, stderr = %q", exitCode, stderr.String())
	}
	if len(client.calls) != 1 || client.calls[0] != "reconcile:task-0001:validate-clean-candidate" ||
		client.operationID != "operation-reconcile-cli" {
		t.Fatalf("client calls/operation = %v/%q", client.calls, client.operationID)
	}
	var result localapi.TaskMutationResult
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil || result != client.taskMutation {
		t.Fatalf("reconciliation JSON = %#v, %v", result, err)
	}
}

func TestRun_CleanupUsesStableReleaseBeforeRemovalCommand(t *testing.T) {
	client := fixtureClient()
	client.taskMutation = localapi.TaskMutationResult{
		SchemaVersion: 1, OperationID: "operation-cleanup-cli", TaskHandle: "task-0001",
		State: domain.TaskCleaned, StateVersion: 22, SideEffect: localapi.SideEffectMutate,
	}
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := Run(context.Background(), []string{
		"task", "cleanup", "task-0001", "--operation", "operation-cleanup-cli", "--format", "json",
	}, &stdout, &stderr, testConfig(client))
	if exitCode != ExitSuccess {
		t.Fatalf("Run() exit = %d, stderr = %q", exitCode, stderr.String())
	}
	if len(client.calls) != 1 || client.calls[0] != "cleanup:task-0001" || client.operationID != "operation-cleanup-cli" {
		t.Fatalf("client calls/operation = %v/%q", client.calls, client.operationID)
	}
	var result localapi.TaskMutationResult
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil || result != client.taskMutation {
		t.Fatalf("cleanup JSON = %#v, %v", result, err)
	}
}

func TestRun_CleanupReportsOpenHoldWithoutLeakingOperatorReason(t *testing.T) {
	privateReason := "release review contains operator-authored private detail"
	failure, err := domain.NewFailure(
		domain.ErrorPrecondition,
		true,
		"cleanup is blocked by an open task hold",
		"close the exact task cleanup hold, then retry cleanup",
		errors.New(privateReason),
	)
	if err != nil {
		t.Fatalf("NewFailure() error = %v", err)
	}
	client := fixtureClient()
	client.err = failure
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := Run(context.Background(), []string{
		"task", "cleanup", "task-0001", "--operation", "operation-cleanup-cli", "--format", "json",
	}, &stdout, &stderr, testConfig(client))
	want := "devcrew: precondition: cleanup is blocked by an open task hold\n" +
		"Hint: close the exact task cleanup hold, then retry cleanup\n"
	if exitCode != ExitRejected || stdout.Len() != 0 || stderr.String() != want {
		t.Fatalf("Run() = %d, stdout=%q stderr=%q, want rejected cleanup diagnostic", exitCode, stdout.String(), stderr.String())
	}
	if strings.Contains(stderr.String(), privateReason) {
		t.Fatalf("cleanup diagnostic leaked operator-authored reason: %q", stderr.String())
	}
	if len(client.calls) != 1 || client.calls[0] != "cleanup:task-0001" || client.operationID != "operation-cleanup-cli" {
		t.Fatalf("client calls/operation = %v/%q", client.calls, client.operationID)
	}
	t.Logf("operator CLI transcript:\n%s", stderr.String())
}

func TestRun_HelpAndVersionDoNotConnect(t *testing.T) {
	for _, test := range []struct {
		name string
		args []string
		want string
	}{
		{name: "help", args: []string{"--help"}, want: "Usage: devcrew"},
		{name: "short help", args: []string{"-h"}, want: "Usage: devcrew"},
		{name: "version", args: []string{"--version"}, want: "devcrew test-version"},
	} {
		t.Run(test.name, func(t *testing.T) {
			var stdout bytes.Buffer
			var stderr bytes.Buffer
			factoryCalled := false
			exitCode := Run(context.Background(), test.args, &stdout, &stderr, Config{
				DefaultSocketPath: "/private/tmp/devcrew.sock",
				Version:           "test-version",
				NewClient: func(string) (ReadClient, error) {
					factoryCalled = true
					return &fakeClient{}, nil
				},
				NewOperationID: func() (string, error) { return "read-0001", nil },
			})
			if exitCode != ExitSuccess || !strings.Contains(stdout.String(), test.want) || stderr.Len() != 0 {
				t.Fatalf("Run() = %d, stdout=%q stderr=%q", exitCode, stdout.String(), stderr.String())
			}
			if factoryCalled {
				t.Fatal("client factory called for help/version")
			}
		})
	}
}

func TestRun_HumanReadCommandsUseOneCanonicalClient(t *testing.T) {
	tests := []struct {
		name       string
		args       []string
		wantCall   string
		wantOutput string
	}{
		{name: "service status", args: []string{"service", "status"}, wantCall: "diagnose", wantOutput: "SERVICE"},
		{name: "doctor", args: []string{"doctor"}, wantCall: "diagnose", wantOutput: "CHECK"},
		{name: "fleet status", args: []string{"status"}, wantCall: "fleet", wantOutput: "INIT/COMPONENT"},
		{name: "task list", args: []string{"tasks", "list"}, wantCall: "list", wantOutput: "task-0001"},
		{name: "task show", args: []string{"task", "show", "task-0001"}, wantCall: "show:task-0001", wantOutput: "preparationOperationId: \"prepare-view-0001\""},
		{name: "task explain", args: []string{"task", "explain", "task-0001"}, wantCall: "explain:task-0001", wantOutput: "REASON"},
		{name: "task operation", args: []string{"task", "operation", "op-0001"}, wantCall: "operation:op-0001", wantOutput: "OPERATION"},
		{name: "task launch plan", args: []string{"task", "launch-plan", "task-0001"}, wantCall: "launch-plan:task-0001", wantOutput: `"terminalAllowEntryId"`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			client := fixtureClient()
			var stdout bytes.Buffer
			var stderr bytes.Buffer
			exitCode := Run(context.Background(), append([]string{"--socket", "/private/tmp/explicit.sock"}, test.args...), &stdout, &stderr, testConfig(client))
			if exitCode != ExitSuccess {
				t.Fatalf("Run() exit = %d, stderr=%q", exitCode, stderr.String())
			}
			if !strings.Contains(stdout.String(), test.wantOutput) {
				t.Fatalf("stdout = %q, want %q", stdout.String(), test.wantOutput)
			}
			if len(client.calls) != 1 || client.calls[0] != test.wantCall {
				t.Fatalf("client calls = %v, want [%s]", client.calls, test.wantCall)
			}
			if client.operationID != "read-0001" {
				t.Fatalf("operation ID = %q, want read-0001", client.operationID)
			}
		})
	}
}

func TestRun_JSONFormatsAreStableVersionedProjections(t *testing.T) {
	tests := [][]string{
		{"doctor", "--format", "json"},
		{"status", "--format", "json"},
		{"tasks", "list", "--format", "json"},
		{"task", "show", "task-0001", "--format", "json"},
		{"task", "explain", "task-0001", "--format", "json"},
		{"task", "operation", "op-0001", "--format", "json"},
		{"task", "launch-plan", "task-0001", "--format", "json"},
	}
	for _, args := range tests {
		t.Run(strings.Join(args, "_"), func(t *testing.T) {
			var stdout bytes.Buffer
			var stderr bytes.Buffer
			exitCode := Run(context.Background(), args, &stdout, &stderr, testConfig(fixtureClient()))
			if exitCode != ExitSuccess {
				t.Fatalf("Run() exit = %d, stderr=%q", exitCode, stderr.String())
			}
			var decoded map[string]any
			if err := json.Unmarshal(stdout.Bytes(), &decoded); err != nil {
				t.Fatalf("JSON output = %q: %v", stdout.String(), err)
			}
			if decoded["schemaVersion"] != float64(1) {
				t.Fatalf("schemaVersion = %#v, want 1", decoded["schemaVersion"])
			}
		})
	}
}

func TestRun_RejectsInvalidSyntaxAndReferencesBeforeConnecting(t *testing.T) {
	privateArgument := "credential-shaped-private-value"
	tests := [][]string{
		nil,
		{"--socket"},
		{"--unknown", privateArgument},
		{"doctor", "--format", "yaml"},
		{"status", "--watch"},
		{"tasks"},
		{"tasks", "list", "extra"},
		{"task", "show"},
		{"task", "show", "../escape"},
		{"task", "explain", "bad id"},
		{"task", "operation", "bad id"},
		{"task", "reconcile", "task-0001"},
		{"task", "reconcile", "task-0001", "--action", "validate-developer-work"},
		{"task", "reconcile", "task-0001", "--action", "validate-clean-candidate", "--worktree", "/forged"},
		{"initiative", "list"},
	}
	for _, args := range tests {
		t.Run(strings.Join(args, "_"), func(t *testing.T) {
			factoryCalled := false
			config := testConfig(fixtureClient())
			config.NewClient = func(string) (ReadClient, error) {
				factoryCalled = true
				return fixtureClient(), nil
			}
			var stdout bytes.Buffer
			var stderr bytes.Buffer
			exitCode := Run(context.Background(), args, &stdout, &stderr, config)
			if exitCode != ExitUsage {
				t.Fatalf("Run(%v) exit = %d, want %d", args, exitCode, ExitUsage)
			}
			if factoryCalled {
				t.Fatal("client factory called for invalid syntax")
			}
			if strings.Contains(stdout.String()+stderr.String(), privateArgument) {
				t.Fatalf("diagnostic leaked rejected argument: %q", stderr.String())
			}
			if !strings.Contains(stderr.String(), "invalid command") {
				t.Fatalf("stderr = %q, want safe usage diagnostic", stderr.String())
			}
		})
	}
}

func TestRun_MapsTypedFailuresToStableExitCodes(t *testing.T) {
	tests := []struct {
		code domain.ErrorCode
		exit int
	}{
		{code: domain.ErrorInvalidArgument, exit: ExitUsage},
		{code: domain.ErrorNotFound, exit: ExitRejected},
		{code: domain.ErrorConflict, exit: ExitRejected},
		{code: domain.ErrorUnauthorized, exit: ExitRejected},
		{code: domain.ErrorPrecondition, exit: ExitRejected},
		{code: domain.ErrorUnavailable, exit: ExitUnavailable},
		{code: domain.ErrorInternal, exit: ExitUnavailable},
		{code: domain.ErrorDeadlineExceeded, exit: ExitUncertain},
		{code: domain.ErrorUnknown, exit: ExitUncertain},
	}
	for _, test := range tests {
		t.Run(string(test.code), func(t *testing.T) {
			failure, err := domain.NewFailure(test.code, false, "safe failure", "safe hint", errors.New("private cause"))
			if err != nil {
				t.Fatalf("NewFailure() error = %v", err)
			}
			client := fixtureClient()
			client.err = failure
			var stdout bytes.Buffer
			var stderr bytes.Buffer
			exitCode := Run(context.Background(), []string{"doctor"}, &stdout, &stderr, testConfig(client))
			if exitCode != test.exit {
				t.Fatalf("Run() exit = %d, want %d", exitCode, test.exit)
			}
			if !strings.Contains(stderr.String(), string(test.code)) || !strings.Contains(stderr.String(), "safe hint") || strings.Contains(stderr.String(), "private cause") {
				t.Fatalf("stderr = %q, want safe typed failure", stderr.String())
			}
		})
	}
}

func TestRun_FactoryIDAndWriterFailuresAreSafe(t *testing.T) {
	privateCause := errors.New("private socket and randomness detail")
	tests := []struct {
		name   string
		mutate func(*Config)
		stdout io.Writer
		want   int
	}{
		{name: "factory", mutate: func(config *Config) {
			config.NewClient = func(string) (ReadClient, error) { return nil, privateCause }
		}, stdout: &bytes.Buffer{}, want: ExitUnavailable},
		{name: "operation ID", mutate: func(config *Config) {
			config.NewOperationID = func() (string, error) { return "", privateCause }
		}, stdout: &bytes.Buffer{}, want: ExitUnavailable},
		{name: "invalid generated ID", mutate: func(config *Config) {
			config.NewOperationID = func() (string, error) { return "bad id", nil }
		}, stdout: &bytes.Buffer{}, want: ExitUnavailable},
		{name: "writer", mutate: func(*Config) {}, stdout: failingWriter{}, want: ExitUnavailable},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			config := testConfig(fixtureClient())
			test.mutate(&config)
			var stderr bytes.Buffer
			exitCode := Run(context.Background(), []string{"doctor"}, test.stdout, &stderr, config)
			if exitCode != test.want {
				t.Fatalf("Run() exit = %d, want %d", exitCode, test.want)
			}
			if strings.Contains(stderr.String(), privateCause.Error()) {
				t.Fatalf("stderr leaked private cause: %q", stderr.String())
			}
		})
	}
}

type fakeClient struct {
	diagnostic   application.DiagnosticReport
	fleet        application.FleetSnapshot
	list         application.TaskList
	profiles     application.WorkerProfileList
	detail       application.TaskDetail
	explanation  application.TaskExplanation
	operation    application.OperationView
	launchPlan   application.LaunchPlan
	prepared     localapi.PrepareTaskResult
	taskMutation localapi.TaskMutationResult
	err          error
	calls        []string
	operationID  string
}

func (client *fakeClient) ReconcileTask(
	_ context.Context,
	operationID string,
	input localapi.ReconcileTaskInput,
) (localapi.TaskMutationResult, error) {
	client.record(operationID, "reconcile:"+input.TaskHandle+":"+string(input.Action))
	return client.taskMutation, client.err
}

func (client *fakeClient) PauseTask(
	_ context.Context,
	operationID string,
	input localapi.PauseTaskInput,
) (localapi.TaskMutationResult, error) {
	client.record(operationID, "pause:"+input.TaskHandle)
	return client.taskMutation, client.err
}

func (client *fakeClient) CleanupTask(
	_ context.Context,
	operationID string,
	input localapi.CleanupTaskInput,
) (localapi.TaskMutationResult, error) {
	client.record(operationID, "cleanup:"+input.TaskHandle)
	return client.taskMutation, client.err
}

func (client *fakeClient) HandbackTask(
	_ context.Context,
	operationID string,
	input localapi.HandbackTaskInput,
) (localapi.TaskMutationResult, error) {
	client.record(operationID, "handback:"+input.TaskHandle+":"+string(input.Action))
	return client.taskMutation, client.err
}

func (client *fakeClient) PrepareTask(_ context.Context, operationID string, input localapi.PrepareTaskInput) (localapi.PrepareTaskResult, error) {
	client.record(operationID, "prepare:"+input.RepositoryID)
	return client.prepared, client.err
}

func (client *fakeClient) record(operationID, call string) {
	client.operationID = operationID
	client.calls = append(client.calls, call)
}

func (client *fakeClient) Diagnose(_ context.Context, operationID string) (application.DiagnosticReport, error) {
	client.record(operationID, "diagnose")
	return client.diagnostic, client.err
}

func (client *fakeClient) Fleet(_ context.Context, operationID string) (application.FleetSnapshot, error) {
	client.record(operationID, "fleet")
	return client.fleet, client.err
}

func (client *fakeClient) ListWorkerProfiles(
	_ context.Context,
	operationID string,
) (application.WorkerProfileList, error) {
	client.record(operationID, "workers")
	return client.profiles, client.err
}

func (client *fakeClient) ListTasks(_ context.Context, operationID string) (application.TaskList, error) {
	client.record(operationID, "list")
	return client.list, client.err
}

func (client *fakeClient) ShowTask(_ context.Context, operationID, handle string) (application.TaskDetail, error) {
	client.record(operationID, "show:"+handle)
	return client.detail, client.err
}

func (client *fakeClient) ExplainTask(_ context.Context, operationID, handle string) (application.TaskExplanation, error) {
	client.record(operationID, "explain:"+handle)
	return client.explanation, client.err
}

func (client *fakeClient) Operation(_ context.Context, operationID, targetID string) (application.OperationView, error) {
	client.record(operationID, "operation:"+targetID)
	return client.operation, client.err
}

func (client *fakeClient) GetLaunchPlan(_ context.Context, operationID, taskHandle string) (application.LaunchPlan, error) {
	client.record(operationID, "launch-plan:"+taskHandle)
	return client.launchPlan, client.err
}

func fixtureClient() *fakeClient {
	summary := application.TaskSummary{
		TaskHandle:       "task-0001",
		State:            domain.TaskBlocked,
		StateReason:      "task_blocked",
		StateSource:      application.StateSourceStore,
		StateConfidence:  application.ConfidenceVerified,
		Freshness:        application.FreshnessCurrent,
		Custody:          "unknown",
		WorkerProfileID:  "codex-standard",
		RepositoryID:     "product-api",
		Head:             "unknown",
		Activity:         "unknown",
		Processes:        "unknown",
		Validation:       "unknown",
		BlockedBy:        "unknown",
		Attention:        "unknown",
		StateVersion:     7,
		ElapsedMs:        1000,
		LastActivityAtMs: 1234,
		NextSafeActions:  []application.NextAction{application.ActionInspectTask},
	}
	return &fakeClient{
		diagnostic: application.DiagnosticReport{
			SchemaVersion: 1, CapturedAtMs: 1234, StateVersion: 7,
			Completeness: application.CompletenessPartial, ServiceHealth: application.HealthHealthy,
			ComisHealth: application.HealthUnavailable,
			Checks:      []application.DiagnosticCheck{{Name: "store", Status: application.CheckPass, Message: "durable state is readable", Hint: "none"}},
		},
		fleet: application.FleetSnapshot{
			SchemaVersion: 1, CapturedAtMs: 1234, StateVersion: 7,
			Completeness: application.CompletenessPartial, ServiceHealth: application.HealthHealthy,
			ComisHealth: application.HealthUnavailable, Tasks: []application.TaskSummary{summary},
		},
		list: application.TaskList{SchemaVersion: 1, CapturedAtMs: 1234, StateVersion: 7, Completeness: application.CompletenessPartial, Tasks: []application.TaskSummary{summary}},
		detail: application.TaskDetail{
			SchemaVersion: 1, CapturedAtMs: 1234, Completeness: application.CompletenessPartial,
			Summary: summary, Shape: domain.ShapeShip, BaseRevision: strings.Repeat("a", 40),
			Evidence: application.TaskEvidenceView{
				Candidate: application.CandidateEvidenceView{
					Status: application.CandidateEvidenceJudged, HeadRevision: strings.Repeat("b", 40),
					EvidenceDigest: strings.Repeat("c", 64),
				},
				Activity: application.ActivityEvidenceView{
					Status:   application.ActivityEvidenceAuthenticatedReport,
					ReportID: "report-view-0001", ReportKind: domain.ReportCandidateComplete, AcceptedAtMs: 1234,
				},
				Decision:   application.DecisionEvidenceView{Status: application.DecisionEvidenceNone},
				Validation: application.ValidationEvidenceView{Status: application.ValidationEvidenceAccepted, EvidenceDigest: strings.Repeat("c", 64)},
				Delivery: application.DeliveryEvidenceView{
					Status: application.DeliveryEvidenceDelivered, EvidenceOperationID: "delivery-view-0001",
					EvidenceRef: "evidence-view-0001", PullRequestID: "github-pr-17",
				},
				Cleanup: application.CleanupEvidenceView{Status: application.CleanupEvidenceNotStarted},
				Authority: application.TaskAuthorityView{
					ManagedRunID: "managed-run-0001", WorkspaceLeaseID: "workspace-lease-0001",
					ExecutionAttachmentID: "attachment-view-0001", PreparationOperationID: "prepare-view-0001",
				},
			},
			BriefRevision: 1, ValidationProfile: "go-default", DeliveryMode: domain.DeliveryPullRequest,
			StateVersion: 7, CreatedAtMs: 1, UpdatedAtMs: 2,
		},
		explanation: application.TaskExplanation{
			SchemaVersion: 1, CapturedAtMs: 1234, Completeness: application.CompletenessPartial,
			Summary: summary, ReasonCode: "task_blocked", Explanation: "Task progress is blocked.",
			LikelyRootCause: "A required input is unresolved.", NextSafeActions: []application.NextAction{application.ActionInspectTask},
		},
		operation: application.OperationView{
			SchemaVersion: 1, CapturedAtMs: 1234, OperationID: "op-0001", Command: "PrepareTask",
			SubjectDigest: strings.Repeat("b", 64), Status: domain.OperationCompleted, StateVersion: 7,
		},
		launchPlan: application.LaunchPlan{
			SchemaVersion: 1, CapturedAtMs: 1234, StateVersion: 7,
			Completeness: application.CompletenessComplete, TaskHandle: "task-0001",
			State: domain.TaskReady, StateSource: application.StateSourceStore,
			StateConfidence: application.ConfidenceVerified, Freshness: application.FreshnessCurrent,
			WorkerProfileID: "codex-standard", TerminalAllowEntryID: "terminal-codex-reviewed",
			BriefRevisionHash:    strings.Repeat("c", 64),
			AttachmentTargetName: "attachment-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa.sock",
		},
	}
}

func testConfig(client *fakeClient) Config {
	return Config{
		DefaultSocketPath: "/private/tmp/default.sock",
		Version:           "test-version",
		NewClient: func(socketPath string) (ReadClient, error) {
			if socketPath != "/private/tmp/default.sock" && socketPath != "/private/tmp/explicit.sock" {
				return nil, errors.New("unexpected socket")
			}
			return client, nil
		},
		NewOperationID: func() (string, error) { return "read-0001", nil },
	}
}

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) {
	return 0, errors.New("write failed")
}

// One task contract, reused by the input-source cases below.
const cliPrepareContract = `{"shape":"scout","repositoryId":"product-api","baseRevision":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","acceptanceCriteria":["Return one report."],"constraints":["Do not deliver."],"validationProfile":"go-default","deliveryMode":"report","workerProfileId":"fixture-worker"}`

type cliStringFile struct{ *strings.Reader }

func (cliStringFile) Close() error { return nil }

func TestRun_PrepareTaskReadsAContractFromAFileSource(t *testing.T) {
	client := fixtureClient()
	config := testConfig(client)
	var opened string
	config.OpenInput = func(path string) (io.ReadCloser, error) {
		opened = path
		return cliStringFile{strings.NewReader(cliPrepareContract)}, nil
	}
	var stdout, stderr bytes.Buffer

	exitCode := Run(context.Background(), []string{
		"task", "prepare", "--input", "/tmp/contract.json",
		"--operation", "operation-prepare-file", "--format", "json",
	}, &stdout, &stderr, config)

	if exitCode != ExitSuccess {
		t.Fatalf("Run() exit = %d, stderr = %q", exitCode, stderr.String())
	}
	if opened != "/tmp/contract.json" {
		t.Errorf("opened %q, want the exact requested path", opened)
	}
}

func TestRun_PrepareTaskRefusesUnreadableAndUnboundedInput(t *testing.T) {
	// Each case must fail before the client is reached: a contract the CLI
	// could not read in full is not a contract, and guessing at a truncated one
	// would prepare work nobody described.
	for name, mutate := range map[string]func(*Config){
		"no file access": func(config *Config) { config.OpenInput = nil },
		"open fails": func(config *Config) {
			config.OpenInput = func(string) (io.ReadCloser, error) { return nil, errors.New("denied") }
		},
		"no reader": func(config *Config) {
			config.OpenInput = func(string) (io.ReadCloser, error) { return cliStringFile{strings.NewReader("")}, nil }
			config.Stdin = nil
		},
		"over the request bound": func(config *Config) {
			oversized := strings.Repeat("x", localapi.MaxRequestBytes+1)
			config.OpenInput = func(string) (io.ReadCloser, error) {
				return cliStringFile{strings.NewReader(oversized)}, nil
			}
		},
	} {
		client := fixtureClient()
		config := testConfig(client)
		mutate(&config)
		var stdout, stderr bytes.Buffer

		exitCode := Run(context.Background(), []string{
			"task", "prepare", "--input", "/tmp/contract.json", "--operation", "operation-prepare-bad",
		}, &stdout, &stderr, config)

		if exitCode == ExitSuccess {
			t.Errorf("%s: Run() succeeded on unreadable input", name)
		}
		if len(client.calls) != 0 {
			t.Errorf("%s: reached the service with %v", name, client.calls)
		}
	}
}
