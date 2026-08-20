package sqlite

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/comisai/comis-dev-crew/internal/application"
	"github.com/comisai/comis-dev-crew/internal/domain"
)

func TestFullStackInitiativeCampaignPreservesParallelLanesAndExactHeadAuthority(t *testing.T) {
	ctx := context.Background()
	fixture := newFullStackCampaignFixture(t)
	limits := &application.InitiativeSchedulingLimits{
		MaxConcurrentTasks: 3, MaxConcurrentTasksPerRepository: 3,
		WorkerProfileLimits: map[string]int{"fixture-worker": 3, "replacement-worker": 1},
	}

	initial := campaignSchedule(t, fixture, *limits)
	assertCampaignDecision(t, initial, fixture.handles.backend, true, "")
	assertCampaignDecision(t, initial, fixture.handles.frontend, true, "")
	assertCampaignDecision(t, initial, fixture.handles.integration, false, application.ScheduleIntegrationHeld)
	assertCampaignDecision(t, initial, fixture.handles.validation, false, application.ScheduleDependencyBlocked)

	backend := startCampaignTask(t, fixture, fixture.handles.backend, limits, fixture.at.Add(time.Minute))
	frontend := startCampaignTask(t, fixture, fixture.handles.frontend, limits, fixture.at.Add(2*time.Minute))
	if backend.State != domain.TaskWorking || frontend.State != domain.TaskWorking {
		t.Fatalf("parallel lanes = %q/%q, want both working", backend.State, frontend.State)
	}
	assertCampaignDecision(
		t, campaignSchedule(t, fixture, *limits), fixture.handles.integration, false, application.ScheduleIntegrationHeld,
	)

	backend = pauseCampaignTask(t, fixture, backend, "campaign-backend-pause", fixture.at.Add(3*time.Minute))
	replaced, err := fixture.store.CommitTaskReplace(ctx, application.TaskReplaceMutation{
		OperationID: "campaign-backend-replace", SubjectDigest: strings.Repeat("1", 64),
		TaskHandle: backend.Handle, WorkerProfileID: "replacement-worker",
		Snapshot: campaignWorkspaceSnapshot(fixture, backend, strings.Repeat("a", 40)),
		At:       fixture.at.Add(4 * time.Minute),
	})
	if err != nil {
		t.Fatalf("CommitTaskReplace() error = %v", err)
	}
	frontendDuringTakeover, err := fixture.store.GetTask(ctx, frontend.Handle)
	if err != nil || frontendDuringTakeover.State != domain.TaskWorking {
		t.Fatalf("frontend during backend takeover = %#v, %v", frontendDuringTakeover, err)
	}
	if replaced.Task.State != domain.TaskReady || replaced.Task.BriefRevision != backend.BriefRevision+1 ||
		replaced.Task.WorkerProfileID != "replacement-worker" {
		t.Fatalf("backend replacement = %#v", replaced.Task)
	}

	backend = startCampaignTask(t, fixture, replaced.Task.Handle, limits, fixture.at.Add(5*time.Minute))
	backend = pauseCampaignTask(t, fixture, backend, "campaign-backend-handback-pause", fixture.at.Add(6*time.Minute))
	backendHead := strings.Repeat("b", 40)
	handbackAt := fixture.at.Add(7 * time.Minute)
	handback, err := fixture.store.CommitTaskHandback(ctx, application.TaskHandbackMutation{
		OperationID: "campaign-backend-handback", SubjectDigest: strings.Repeat("2", 64),
		TaskHandle: backend.Handle, Action: application.HandbackValidateDeveloperWork,
		Snapshot: campaignWorkspaceSnapshot(fixture, backend, backendHead),
		CandidateReport: domain.WorkerReport{
			SchemaVersion: 1, LocalReportID: "campaign-backend-handback",
			BriefRevision: backend.BriefRevision, BriefRevisionHash: backend.BriefRevisionHash,
			Kind: domain.ReportCandidateComplete, Summary: "Developer work is ready for validation.",
		},
		CandidateReportDigest: strings.Repeat("3", 64), At: handbackAt,
	})
	if err != nil || handback.Task.State != domain.TaskValidating {
		t.Fatalf("CommitTaskHandback() = %#v, %v", handback, err)
	}
	frontendDuringHandback, err := fixture.store.GetTask(ctx, frontend.Handle)
	if err != nil || frontendDuringHandback.State != domain.TaskWorking {
		t.Fatalf("frontend during backend handback = %#v, %v", frontendDuringHandback, err)
	}

	backend = acceptCampaignCandidate(t, fixture, handback.Task, backendHead, fixture.at.Add(12*time.Minute))
	frontend = reportCampaignCandidate(t, fixture, frontend, "campaign-frontend-candidate", fixture.at.Add(13*time.Minute))
	frontendHead := strings.Repeat("c", 40)
	frontend = acceptCampaignCandidate(t, fixture, frontend, frontendHead, fixture.at.Add(18*time.Minute))
	if backend.State != domain.TaskCandidateComplete || frontend.State != domain.TaskCandidateComplete {
		t.Fatalf("component candidates = %q/%q", backend.State, frontend.State)
	}

	if _, err := fixture.store.CommitTaskStart(ctx, application.TaskStartMutation{
		TaskHandle: fixture.handles.validation, OperationID: "campaign-validation-too-early",
		SubjectDigest: strings.Repeat("4", 64), At: fixture.at.Add(18*time.Minute + 30*time.Second), SchedulingLimits: limits,
	}); !errors.Is(err, application.ErrPrecondition) {
		t.Fatalf("validation before integration error = %v, want ErrPrecondition", err)
	}
	integration := startCampaignTask(t, fixture, fixture.handles.integration, limits, fixture.at.Add(19*time.Minute))

	targetHead := strings.Repeat("d", 40)
	adapter := &campaignIntegrationAdapter{
		candidateHeads: map[string]string{backend.Handle: backendHead, frontend.Handle: frontendHead},
		targetHead:     targetHead,
	}
	integrationAt := fixture.at.Add(20 * time.Minute)
	integrations, err := application.NewIntegrations(application.IntegrationConfig{
		Store: fixture.store, Adapter: adapter,
		Policies: func(string) (application.IntegrationStrategy, error) {
			return application.IntegrationCherryPick, nil
		},
		Clock: func() time.Time { return integrationAt },
	})
	if err != nil {
		t.Fatalf("NewIntegrations() error = %v", err)
	}

	adapter.candidateHeads[backend.Handle] = strings.Repeat("e", 40)
	invalidated, err := integrations.ApplyCandidate(ctx, application.ApplyIntegrationCandidateCommand{
		OperationID: "campaign-integrate-backend-stale", InitiativeHandle: fixture.initiativeHandle,
		IntegrationTaskHandle: integration.Handle, CandidateTaskHandle: backend.Handle,
		CandidateHead: backendHead, ExpectedIntegrationHead: targetHead,
	})
	if err != nil || invalidated.Outcome != application.IntegrationInvalidated {
		t.Fatalf("ApplyCandidate(changed backend head) = %#v, %v", invalidated, err)
	}
	if adapter.targetHead != targetHead {
		t.Fatalf("integration target moved after stale backend: %q", adapter.targetHead)
	}
	invalidatedBackend, err := fixture.store.GetTask(ctx, backend.Handle)
	if err != nil || invalidatedBackend.State != domain.TaskValidating {
		t.Fatalf("invalidated backend = %#v, %v", invalidatedBackend, err)
	}
	unaffectedFrontend, err := fixture.store.GetTask(ctx, frontend.Handle)
	if err != nil || unaffectedFrontend.State != domain.TaskCandidateComplete {
		t.Fatalf("frontend after backend invalidation = %#v, %v", unaffectedFrontend, err)
	}
	staleCalls := adapter.calls
	if _, err := integrations.ApplyCandidate(ctx, application.ApplyIntegrationCandidateCommand{
		OperationID: "campaign-integrate-wrong-writer", InitiativeHandle: fixture.initiativeHandle,
		IntegrationTaskHandle: frontend.Handle, CandidateTaskHandle: backend.Handle,
		CandidateHead: backendHead, ExpectedIntegrationHead: targetHead,
	}); err == nil || adapter.calls != staleCalls {
		t.Fatalf("non-owner integration = calls:%d error:%v", adapter.calls, err)
	}

	frontendResult, err := integrations.ApplyCandidate(ctx, application.ApplyIntegrationCandidateCommand{
		OperationID: "campaign-integrate-frontend", InitiativeHandle: fixture.initiativeHandle,
		IntegrationTaskHandle: integration.Handle, CandidateTaskHandle: frontend.Handle,
		CandidateHead: frontendHead, ExpectedIntegrationHead: targetHead,
	})
	if err != nil || frontendResult.Outcome != application.IntegrationApplied {
		t.Fatalf("ApplyCandidate(unaffected frontend) = %#v, %v", frontendResult, err)
	}
	backendHead = strings.Repeat("e", 40)
	backend = acceptCampaignCandidate(
		t, fixture, invalidatedBackend, backendHead, fixture.at.Add(25*time.Minute),
	)
	integrationAt = fixture.at.Add(26 * time.Minute)
	backendResult, err := integrations.ApplyCandidate(ctx, application.ApplyIntegrationCandidateCommand{
		OperationID: "campaign-integrate-backend-current", InitiativeHandle: fixture.initiativeHandle,
		IntegrationTaskHandle: integration.Handle, CandidateTaskHandle: backend.Handle,
		CandidateHead: backendHead, ExpectedIntegrationHead: frontendResult.ResultingHead,
	})
	if err != nil || backendResult.Outcome != application.IntegrationApplied {
		t.Fatalf("ApplyCandidate(current backend) = %#v, %v", backendResult, err)
	}

	integration = reportCampaignCandidate(
		t, fixture, integration, "campaign-integration-candidate", fixture.at.Add(27*time.Minute),
	)
	integration = acceptCampaignCandidate(
		t, fixture, integration, backendResult.ResultingHead, fixture.at.Add(32*time.Minute),
	)
	if integration.State != domain.TaskCandidateComplete {
		t.Fatalf("integration state = %q", integration.State)
	}
	validation, err := fixture.store.CommitTaskStart(ctx, application.TaskStartMutation{
		TaskHandle: fixture.handles.validation, OperationID: "campaign-validation-release",
		SubjectDigest: strings.Repeat("5", 64), At: fixture.at.Add(33 * time.Minute), SchedulingLimits: limits,
	})
	if err != nil || validation.Task.State != domain.TaskLaunching {
		t.Fatalf("validation after integration = %#v, %v", validation, err)
	}
}

type fullStackCampaignHandles struct {
	contract    string
	backend     string
	frontend    string
	integration string
	validation  string
}

type fullStackCampaignFixture struct {
	store            *Store
	initiativeHandle string
	handles          fullStackCampaignHandles
	workspaces       map[string]string
	at               time.Time
}

func newFullStackCampaignFixture(t *testing.T) fullStackCampaignFixture {
	t.Helper()
	ctx := context.Background()
	store, err := Open(ctx, filepath.Join(canonicalTempDir(t), "campaign.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	mutation := sqlitePreparedInitiativeMutation()
	handles := fullStackCampaignHandles{
		contract: "task-contract", backend: "task-backend", frontend: "task-frontend",
		integration: "task-integration", validation: "task-validation",
	}
	ordered := []string{handles.contract, handles.backend, handles.frontend, handles.integration, handles.validation}
	mutation.Initiative.Components = nil
	mutation.Initiative.Edges = []domain.InitiativeEdge{
		{FromTaskHandle: handles.contract, ToTaskHandle: handles.backend, Kind: domain.EdgeConsumesArtifact, RequiredArtifactKind: domain.ArtifactAPISchema},
		{FromTaskHandle: handles.contract, ToTaskHandle: handles.frontend, Kind: domain.EdgeConsumesArtifact, RequiredArtifactKind: domain.ArtifactAPISchema},
		{FromTaskHandle: handles.backend, ToTaskHandle: handles.integration, Kind: domain.EdgeIntegratesAfter},
		{FromTaskHandle: handles.frontend, ToTaskHandle: handles.integration, Kind: domain.EdgeIntegratesAfter},
		{FromTaskHandle: handles.integration, ToTaskHandle: handles.validation, Kind: domain.EdgeBlocksStart},
	}
	mutation.Initiative.ContractArtifacts = []string{"artifact-api-v1"}
	mutation.Initiative.IntegrationOwnerTask = handles.integration
	mutation.Members = nil
	workspaces := make(map[string]string, len(ordered))
	for index, handle := range ordered {
		workspace := filepath.Join(canonicalTempDir(t), handle)
		workspaces[handle] = workspace
		mutation.Initiative.Components = append(mutation.Initiative.Components, domain.InitiativeComponent{
			ComponentHandle: "component-" + handle, RepositoryID: "repo-primary",
			ResponsibilityRef: "responsibility-" + handle, TaskHandles: []string{handle},
		})
		task := storeTask(handle, 1)
		task.RepositoryID = "repo-primary"
		task.BaseRevision = mutation.Initiative.BaseRevisionSet[0].Revision
		task.WorkerProfileID = "fixture-worker"
		if handle == handles.backend || handle == handles.frontend {
			task.ConsumedContracts = []domain.PinnedContract{{
				ArtifactHandle: "artifact-api-v1", Kind: domain.ArtifactAPISchema,
				ContentHash: strings.Repeat("a", 64),
			}}
		}
		task.CreatedAt = mutation.At
		task.UpdatedAt = mutation.At
		task, err = task.PinBriefRevision()
		if err != nil {
			t.Fatalf("PinBriefRevision(%q) error = %v", handle, err)
		}
		mutation.Members = append(mutation.Members, application.PreparedInitiativeMember{
			Task: task,
			Preparation: application.ManagedRunPreparation{
				ExternalRunRef: handle, RegistrationNonce: fmt.Sprintf("campaign-member-nonce-%02d", index),
				RequestedWorkspaceRoot: workspace,
				RequestedAttachment: application.PreparedRuntimeAttachment{
					Kind:          application.RuntimeAttachmentUnixSocket,
					SourcePath:    filepath.Join(canonicalTempDir(t), fmt.Sprintf("runtime-%02d", index), "attachment.sock"),
					RelayIdentity: strings.Repeat(fmt.Sprintf("%x", index+1), 64)[:64],
				},
				ExpiresAt: mutation.GroupExpiresAt, State: application.PreparationOpen,
			},
			OperationID:   fmt.Sprintf("campaign-prepare-member-%02d", index),
			SubjectDigest: strings.Repeat(fmt.Sprintf("%x", index+1), 64)[:64],
		})
	}
	recordInitiativeMemberIntents(t, store, mutation)
	if _, err := store.CommitPreparedInitiative(ctx, mutation); err != nil {
		t.Fatalf("CommitPreparedInitiative() error = %v", err)
	}
	activationMembers := make([]application.ManagedRunGroupActivationMember, 0, len(mutation.Members))
	for index, member := range mutation.Members {
		activationMembers = append(activationMembers, application.ManagedRunGroupActivationMember{
			ExternalRunRef: member.Task.Handle, RegistrationNonce: member.Preparation.RegistrationNonce,
			Binding: domain.TaskBinding{
				ManagedRunID:     "managed-run-" + member.Task.Handle,
				WorkspaceLeaseID: "workspace-lease-" + member.Task.Handle,
			},
			ExecutionAttachmentID: "execution-attachment-" + member.Task.Handle,
			AttachmentTargetName:  fmt.Sprintf("attachment-%032x.sock", index+1),
		})
	}
	activatedAt := mutation.At.Add(30 * time.Second)
	if _, err := store.CommitInitiativeActivation(ctx, application.ManagedRunGroupActivationMutation{
		ServiceInstanceID: mutation.Members[0].Task.ServiceInstanceID,
		ManagedRunGroupID: "managed-run-group-campaign", RegistrationNonce: mutation.GroupRegistrationNonce,
		Members: activationMembers, OperationID: "campaign-activate-group",
		SubjectDigest: strings.Repeat("f", 64), At: activatedAt,
	}); err != nil {
		t.Fatalf("CommitInitiativeActivation() error = %v", err)
	}
	return fullStackCampaignFixture{
		store: store, initiativeHandle: mutation.Initiative.Handle,
		handles: handles, workspaces: workspaces, at: activatedAt,
	}
}

func campaignSchedule(
	t *testing.T,
	fixture fullStackCampaignFixture,
	limits application.InitiativeSchedulingLimits,
) application.InitiativeSchedule {
	t.Helper()
	initiative, err := fixture.store.GetInitiative(context.Background(), fixture.initiativeHandle)
	if err != nil {
		t.Fatal(err)
	}
	tasks, err := fixture.store.ListTasks(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	schedules, err := application.ScheduleInitiatives([]domain.DevelopmentInitiative{initiative}, tasks, limits)
	if err != nil || len(schedules) != 1 {
		t.Fatalf("ScheduleInitiatives() = %#v, %v", schedules, err)
	}
	return schedules[0]
}

func assertCampaignDecision(
	t *testing.T,
	schedule application.InitiativeSchedule,
	taskHandle string,
	launchable bool,
	reason application.InitiativeScheduleReason,
) {
	t.Helper()
	for _, decision := range schedule.Tasks {
		if decision.TaskHandle == taskHandle {
			if decision.Launchable != launchable || decision.Reason != reason {
				t.Fatalf("schedule decision for %q = %#v", taskHandle, decision)
			}
			return
		}
	}
	t.Fatalf("schedule omitted %q", taskHandle)
}

func startCampaignTask(
	t *testing.T,
	fixture fullStackCampaignFixture,
	taskHandle string,
	limits *application.InitiativeSchedulingLimits,
	at time.Time,
) domain.Task {
	t.Helper()
	started, err := fixture.store.CommitTaskStart(context.Background(), application.TaskStartMutation{
		TaskHandle: taskHandle, OperationID: "campaign-start-" + taskHandle,
		SubjectDigest: strings.Repeat("6", 64), At: at, SchedulingLimits: limits,
	})
	if err != nil {
		t.Fatalf("CommitTaskStart(%q) error = %v", taskHandle, err)
	}
	if _, err := fixture.store.CommitTerminalEvent(context.Background(), campaignTerminalEventMutation(
		started.Task, "campaign-terminal-running-"+taskHandle,
		application.TerminalRunning, at.Add(time.Second),
	)); err != nil {
		t.Fatalf("CommitTerminalEvent(%q) error = %v", taskHandle, err)
	}
	acknowledged, err := fixture.store.CommitWorkerLaunchAcknowledgement(
		context.Background(), application.WorkerLaunchAcknowledgementMutation{
			OperationID: "campaign-ack-" + taskHandle, SubjectDigest: strings.Repeat("7", 64),
			Acknowledgement: terminalLaunchAcknowledgement(started.Task, fixture.workspaces[taskHandle]),
			At:              at.Add(2 * time.Second),
		},
	)
	if err != nil {
		t.Fatalf("CommitWorkerLaunchAcknowledgement(%q) error = %v", taskHandle, err)
	}
	return acknowledged.Task
}

func pauseCampaignTask(
	t *testing.T,
	fixture fullStackCampaignFixture,
	task domain.Task,
	reportID string,
	at time.Time,
) domain.Task {
	t.Helper()
	client := reportClient(t, fixture.store, task, at)
	if _, err := client.Report(context.Background(), sqliteWorkerReport(task, reportID, domain.ReportPaused)); err != nil {
		t.Fatalf("Report(paused %q) error = %v", task.Handle, err)
	}
	if _, err := fixture.store.CommitTerminalEvent(context.Background(), campaignTerminalEventMutation(
		task, "campaign-terminal-exited-"+reportID, application.TerminalExited, at.Add(time.Second),
	)); err != nil {
		t.Fatalf("CommitTerminalEvent(exited %q) error = %v", task.Handle, err)
	}
	paused, err := fixture.store.GetTask(context.Background(), task.Handle)
	if err != nil || paused.State != domain.TaskPaused {
		t.Fatalf("paused task %q = %#v, %v", task.Handle, paused, err)
	}
	return paused
}

func campaignTerminalEventMutation(
	task domain.Task,
	operationID string,
	transition application.TerminalTransition,
	at time.Time,
) application.TerminalEventMutation {
	mutation := terminalEventMutation(task, operationID, transition, at)
	mutation.TerminalSessionID = "terminal-session-" + task.Handle
	return mutation
}

func reportCampaignCandidate(
	t *testing.T,
	fixture fullStackCampaignFixture,
	task domain.Task,
	reportID string,
	at time.Time,
) domain.Task {
	t.Helper()
	report := sqliteWorkerReport(task, reportID, domain.ReportCandidateComplete)
	if _, err := fixture.store.CommitReport(context.Background(), application.ReportMutation{
		Report:        domain.AuthenticatedReport{TaskHandle: task.Handle, Report: report},
		SubjectDigest: strings.Repeat("8", 64), AcceptedAt: at,
	}); err != nil {
		t.Fatalf("CommitReport(candidate %q) error = %v", task.Handle, err)
	}
	validating, err := fixture.store.GetTask(context.Background(), task.Handle)
	if err != nil || validating.State != domain.TaskValidating {
		t.Fatalf("validating task %q = %#v, %v", task.Handle, validating, err)
	}
	return validating
}

func acceptCampaignCandidate(
	t *testing.T,
	fixture fullStackCampaignFixture,
	task domain.Task,
	head string,
	judgedAt time.Time,
) domain.Task {
	t.Helper()
	evidence := candidateEvidence(t, task, head)
	publications := candidateEvidencePublications(t, task, evidence)
	publicationSuffix := "-" + task.Handle + "-" + evidence.Digest()[:16]
	for index := range publications {
		publications[index].OperationID += publicationSuffix
		publications[index].EvidenceRef += publicationSuffix
	}
	accepted, judgment, err := fixture.store.CommitCandidateEvidence(
		context.Background(), task.Handle, evidence,
		[]string{"unit"}, []string{"ci/unit"}, judgedAt,
		publications,
	)
	if err != nil || judgment.Outcome != domain.CandidateAccepted {
		t.Fatalf("CommitCandidateEvidence(%q) = %#v, %#v, %v", task.Handle, accepted, judgment, err)
	}
	return accepted
}

func campaignWorkspaceSnapshot(
	fixture fullStackCampaignFixture,
	task domain.Task,
	head string,
) application.WorkspaceSnapshot {
	return application.WorkspaceSnapshot{
		TaskHandle: task.Handle, RepositoryID: task.RepositoryID,
		WorktreePath: fixture.workspaces[task.Handle], Branch: "devcrew/" + task.Handle,
		HeadRevision: head, Cleanliness: application.WorkspaceClean,
	}
}

type campaignIntegrationAdapter struct {
	candidateHeads map[string]string
	targetHead     string
	calls          int
}

func (adapter *campaignIntegrationAdapter) ApplyIntegrationCandidate(
	_ context.Context,
	request application.IntegrationAdapterRequest,
) (application.IntegrationAdapterResult, error) {
	adapter.calls++
	if adapter.candidateHeads[request.Candidate.TaskHandle] != request.Candidate.HeadRevision {
		return application.IntegrationAdapterResult{
			Outcome: application.IntegrationInvalidated, PreviousHead: request.Target.ExpectedHead,
		}, nil
	}
	if adapter.targetHead != request.Target.ExpectedHead {
		return application.IntegrationAdapterResult{}, errors.New("integration head changed")
	}
	resulting := fmt.Sprintf("%040x", adapter.calls+32)
	adapter.targetHead = resulting
	return application.IntegrationAdapterResult{
		Outcome: application.IntegrationApplied, PreviousHead: request.Target.ExpectedHead,
		ResultingHead: resulting,
	}, nil
}
