package cli

import (
	"context"
	"strconv"
	"strings"

	"github.com/comisai/comis-dev-crew/internal/application"
	"github.com/comisai/comis-dev-crew/internal/domain"
	"github.com/comisai/comis-dev-crew/internal/localapi"
)

// fakeClient is the shared canonical-client double for the console tests. It
// records every call so a test can prove what reached the service, and what
// never did.
type fakeClient struct {
	diagnostic   application.DiagnosticReport
	fleet        application.FleetSnapshot
	list         application.TaskList
	profiles     application.WorkerProfileList
	detail       application.TaskDetail
	explanation  application.TaskExplanation
	operation    application.OperationView
	launchPlan   application.LaunchPlan
	decisions    application.DecisionList
	decision     application.TaskDecision
	diff         application.TaskDiffView
	repairs      application.RepairSurvey
	events       application.EventPage
	logs         application.TaskLogPage
	prepared     localapi.PrepareTaskResult
	taskMutation localapi.TaskMutationResult
	err          error
	calls        []string
	operationID  string
}

func (client *fakeClient) RespondDecision(
	_ context.Context,
	_ string,
	input localapi.RespondDecisionInput,
) (localapi.TaskMutationResult, error) {
	client.calls = append(client.calls,
		"respond-decision:"+input.TaskHandle+":"+input.ExternalKey+":"+input.Response)
	return localapi.TaskMutationResult{SchemaVersion: 1, TaskHandle: input.TaskHandle, StateVersion: 1}, nil
}

func (client *fakeClient) CancelDecision(
	_ context.Context,
	operationID string,
	input localapi.CancelDecisionInput,
) (localapi.TaskMutationResult, error) {
	client.record(operationID, "cancel-decision:"+input.TaskHandle+":"+input.ExternalKey)
	return client.taskMutation, client.err
}

func (client *fakeClient) ReadTaskLogs(
	_ context.Context,
	operationID string,
	input localapi.ReadTaskLogsInput,
) (application.TaskLogPage, error) {
	client.record(operationID, "logs:"+input.TaskHandle+":"+string(input.Source)+":"+strconv.FormatInt(input.AfterSequence, 10))
	return client.logs, client.err
}

func (client *fakeClient) ReadEvents(
	_ context.Context,
	operationID string,
	input localapi.ReadEventsInput,
) (application.EventPage, error) {
	client.record(operationID, "events:"+strconv.FormatInt(input.AfterSequence, 10))
	return client.events, client.err
}

func (client *fakeClient) SurveyRepairs(
	_ context.Context,
	operationID string,
	input localapi.SurveyRepairsInput,
) (application.RepairSurvey, error) {
	client.record(operationID, "repairs:"+input.TaskHandle)
	return client.repairs, client.err
}

func (client *fakeClient) DiffTask(
	_ context.Context,
	operationID string,
	taskHandle string,
) (application.TaskDiffView, error) {
	client.record(operationID, "diff:"+taskHandle)
	return client.diff, client.err
}

func (client *fakeClient) ListDecisions(
	_ context.Context,
	operationID string,
	input localapi.ListDecisionsInput,
) (application.DecisionList, error) {
	client.record(operationID, "decisions:"+input.TaskHandle)
	return client.decisions, client.err
}

func (client *fakeClient) ShowDecision(
	_ context.Context,
	operationID string,
	input localapi.ShowDecisionInput,
) (application.TaskDecision, error) {
	client.record(operationID, "decision:"+input.TaskHandle+":"+input.ExternalKey)
	return client.decision, client.err
}

func (client *fakeClient) ReconcileTask(
	_ context.Context,
	operationID string,
	input localapi.ReconcileTaskInput,
) (localapi.TaskMutationResult, error) {
	client.record(operationID, "reconcile:"+input.TaskHandle+":"+string(input.Action))
	return client.taskMutation, client.err
}

func (client *fakeClient) SteerTask(
	_ context.Context,
	operationID string,
	input localapi.SteerTaskInput,
) (localapi.TaskMutationResult, error) {
	client.record(operationID, "steer:"+input.TaskHandle+":"+input.Instruction)
	return client.taskMutation, client.err
}

func (client *fakeClient) ReplaceWorker(
	_ context.Context,
	operationID string,
	input localapi.ReplaceWorkerInput,
) (localapi.TaskMutationResult, error) {
	client.record(operationID, "replace:"+input.TaskHandle+":"+input.WorkerProfileID)
	return client.taskMutation, client.err
}

func (client *fakeClient) PromoteScout(
	_ context.Context,
	operationID string,
	input localapi.PromoteScoutInput,
) (localapi.PrepareTaskResult, error) {
	client.record(operationID, "promote:"+input.ScoutTaskHandle)
	return client.prepared, client.err
}

func (client *fakeClient) VerifyTask(
	_ context.Context,
	operationID string,
	input localapi.VerifyTaskInput,
) (localapi.TaskMutationResult, error) {
	client.record(operationID, "verify:"+input.TaskHandle)
	return client.taskMutation, client.err
}

func (client *fakeClient) ResumeTask(
	_ context.Context,
	operationID string,
	input localapi.ResumeTaskInput,
) (localapi.TaskMutationResult, error) {
	client.record(operationID, "resume:"+input.TaskHandle)
	return client.taskMutation, client.err
}

func (client *fakeClient) CancelTask(
	_ context.Context,
	operationID string,
	input localapi.CancelTaskInput,
) (localapi.TaskMutationResult, error) {
	client.record(operationID, "cancel:"+input.TaskHandle)
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

func (client *fakeClient) DiscardTask(
	_ context.Context,
	operationID string,
	input localapi.DiscardTaskInput,
) (localapi.TaskMutationResult, error) {
	client.record(operationID, "discard:"+input.TaskHandle)
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
