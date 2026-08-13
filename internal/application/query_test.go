package application

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/comisai/comis-dev-crew/internal/domain"
)

func TestQueries_ProduceStablePartialFleetAndDiagnosticSnapshots(t *testing.T) {
	now := time.Date(2026, time.August, 8, 21, 0, 0, 123000000, time.UTC)
	repository := &queryRepository{
		tasks: []domain.Task{
			queryTask("task-0002", domain.TaskWorking, 2),
			queryTask("task-0001", domain.TaskPrepared, 1),
		},
		stateVersion: 2,
	}
	queries, err := NewQueries(QueryConfig{Repository: repository, Clock: func() time.Time { return now }})
	if err != nil {
		t.Fatalf("NewQueries() error = %v", err)
	}

	diagnostic, err := queries.Diagnose(context.Background())
	if err != nil {
		t.Fatalf("Diagnose() error = %v", err)
	}
	if diagnostic.SchemaVersion != 1 || diagnostic.CapturedAtMs != now.UnixMilli() {
		t.Fatalf("Diagnose() identity = %#v, want schema 1 and injected capture time", diagnostic)
	}
	if diagnostic.Completeness != CompletenessPartial || diagnostic.ServiceHealth != HealthHealthy || diagnostic.ComisHealth != HealthUnavailable {
		t.Fatalf("Diagnose() health = %#v, want explicit partial host-integration posture", diagnostic)
	}
	if len(diagnostic.Checks) != 3 || diagnostic.Checks[2].Status != CheckUnknown {
		t.Fatalf("Diagnose() checks = %#v, want service/store pass and host unknown", diagnostic.Checks)
	}

	fleet, err := queries.Fleet(context.Background())
	if err != nil {
		t.Fatalf("Fleet() error = %v", err)
	}
	if fleet.StateVersion != 2 || fleet.Completeness != CompletenessPartial || len(fleet.Tasks) != 2 {
		t.Fatalf("Fleet() = %#v, want versioned partial two-task snapshot", fleet)
	}
	if !repository.snapshotCalled {
		t.Fatal("Fleet() did not use the atomic task snapshot port")
	}
	if fleet.Tasks[0].TaskHandle != "task-0002" || fleet.Tasks[0].StateSource != StateSourceStore || fleet.Tasks[0].Freshness != FreshnessCurrent {
		t.Fatalf("Fleet() first task = %#v, want stable source and freshness", fleet.Tasks[0])
	}
	if fleet.Tasks[0].ElapsedMs != now.Sub(repository.tasks[0].CreatedAt).Milliseconds() {
		t.Fatalf("Fleet() elapsed = %d, want injected-clock duration", fleet.Tasks[0].ElapsedMs)
	}
}

func TestQueries_ProjectConfiguredDisconnectedHostAsDegraded(t *testing.T) {
	queries, err := NewQueries(QueryConfig{
		Repository: &queryRepository{}, Host: queryHostStatus(false), Clock: time.Now,
	})
	if err != nil {
		t.Fatalf("NewQueries() error = %v", err)
	}
	report, err := queries.Diagnose(context.Background())
	if err != nil {
		t.Fatalf("Diagnose() error = %v", err)
	}
	if report.Completeness != CompletenessComplete || report.ServiceHealth != HealthDegraded ||
		report.ComisHealth != HealthUnavailable || len(report.Checks) != 3 || report.Checks[2].Status != CheckFail {
		t.Fatalf("Diagnose() = %#v, want complete disconnected-host failure", report)
	}
	fleet, err := queries.Fleet(context.Background())
	if err != nil {
		t.Fatalf("Fleet() error = %v", err)
	}
	if fleet.Completeness != CompletenessComplete || fleet.ServiceHealth != HealthDegraded ||
		fleet.ComisHealth != HealthUnavailable {
		t.Fatalf("Fleet() = %#v, want degraded disconnected-host posture", fleet)
	}
}

type queryHostStatus bool

func (status queryHostStatus) Connected() bool { return bool(status) }

func TestQueries_ListShowExplainAndOperationShareCanonicalProjections(t *testing.T) {
	now := time.Date(2026, time.August, 8, 21, 0, 0, 0, time.UTC)
	task := queryTask("task-0001", domain.TaskBlocked, 4)
	operation := queryOperation("op-0001", 5)
	repository := &queryRepository{tasks: []domain.Task{task}, operation: operation, stateVersion: 5}
	queries, err := NewQueries(QueryConfig{Repository: repository, Clock: func() time.Time { return now }})
	if err != nil {
		t.Fatalf("NewQueries() error = %v", err)
	}

	list, err := queries.ListTasks(context.Background())
	if err != nil {
		t.Fatalf("ListTasks() error = %v", err)
	}
	show, err := queries.ShowTask(context.Background(), task.Handle)
	if err != nil {
		t.Fatalf("ShowTask() error = %v", err)
	}
	explanation, err := queries.ExplainTask(context.Background(), task.Handle)
	if err != nil {
		t.Fatalf("ExplainTask() error = %v", err)
	}
	if len(list.Tasks) != 1 || !reflect.DeepEqual(list.Tasks[0], show.Summary) || !reflect.DeepEqual(explanation.Summary, show.Summary) {
		t.Fatalf("query projections diverged: list=%#v show=%#v explain=%#v", list, show, explanation)
	}
	if explanation.ReasonCode != "task_blocked" || len(explanation.NextSafeActions) == 0 {
		t.Fatalf("ExplainTask() = %#v, want explicit reason and repair action", explanation)
	}
	if show.BaseRevision != task.BaseRevision || show.DeliveryMode != task.DeliveryMode || show.CreatedAtMs != task.CreatedAt.UnixMilli() {
		t.Fatalf("ShowTask() = %#v, want durable task detail", show)
	}

	operationView, err := queries.Operation(context.Background(), operation.ID)
	if err != nil {
		t.Fatalf("Operation() error = %v", err)
	}
	if operationView.OperationID != operation.ID || operationView.Status != operation.Status || operationView.SubjectDigest != operation.SubjectDigest {
		t.Fatalf("Operation() = %#v, want durable operation projection", operationView)
	}
}

func TestQueries_ProjectDurableCandidateAndReportEvidence(t *testing.T) {
	now := time.Date(2026, time.August, 8, 21, 0, 0, 0, time.UTC)
	task := queryTask("task-evidence-0001", domain.TaskDelivered, 9)
	task.ReportCursor = 3
	sealed := queryCandidateEvidence(t, task, now.Add(-time.Minute))
	repository := &queryRepository{
		tasks:             []domain.Task{task},
		stateVersion:      task.StateVersion,
		candidateEvidence: sealed,
		candidateJudgment: domain.CandidateJudgment{
			Outcome: domain.CandidateAccepted,
			Reason:  domain.CandidateEvidenceAccepted,
		},
	}
	queries, err := NewQueries(QueryConfig{Repository: repository, Clock: func() time.Time { return now }})
	if err != nil {
		t.Fatalf("NewQueries() error = %v", err)
	}

	show, err := queries.ShowTask(context.Background(), task.Handle)
	if err != nil {
		t.Fatalf("ShowTask() error = %v", err)
	}
	if show.Summary.Head != sealed.Bundle().HeadRevision || show.Summary.Activity != "authenticated_report" ||
		show.Summary.Validation != string(domain.CandidateAccepted) || show.Summary.BlockedBy != "none" ||
		show.Summary.Attention != "none" {
		t.Fatalf("ShowTask() summary = %#v, want durable candidate and report posture", show.Summary)
	}
	if !repository.candidateCalled {
		t.Fatal("ShowTask() did not read durable candidate evidence")
	}
}

func TestQueries_ExposeContentFreeEvidenceAcrossTaskViews(t *testing.T) {
	task := queryTask("task-evidence-view", domain.TaskCleanupHeld, 12)
	evidence := TaskEvidenceView{
		Candidate: CandidateEvidenceView{
			Status: CandidateEvidenceJudged, HeadRevision: strings.Repeat("b", 40),
			EvidenceDigest: strings.Repeat("c", 64), ReconciliationOperationID: "reconcile-view-0001",
		},
		Activity: ActivityEvidenceView{
			Status: ActivityEvidenceAuthenticatedReport, ReportID: "report-view-0001",
			ReportKind: domain.ReportCandidateComplete, AcceptedAtMs: task.UpdatedAt.UnixMilli(),
		},
		Decision:   DecisionEvidenceView{Status: DecisionEvidenceResolved, DecisionReportID: "decision-view-0001", ResolutionReportID: "resolution-view-0001"},
		Validation: ValidationEvidenceView{Status: ValidationEvidenceAccepted, EvidenceDigest: strings.Repeat("c", 64)},
		Delivery: DeliveryEvidenceView{
			Status: DeliveryEvidenceDelivered, EvidenceOperationID: "delivery-view-0001",
			EvidenceRef: "evidence-view-0001", PullRequestID: "github-pr-17",
		},
		Cleanup: CleanupEvidenceView{Status: CleanupEvidenceHeld, OperationID: "cleanup-view-0001", OpenHoldCount: 1},
		Authority: TaskAuthorityView{
			ManagedRunID: task.ManagedRunID, WorkspaceLeaseID: task.WorkspaceLeaseID,
			ExecutionAttachmentID: task.ExecutionAttachmentID, PreparationOperationID: "prepare-view-0001",
		},
	}
	repository := &queryRepository{tasks: []domain.Task{task}, taskEvidence: evidence}
	queries, err := NewQueries(QueryConfig{Repository: repository, Clock: time.Now})
	if err != nil {
		t.Fatalf("NewQueries() error = %v", err)
	}

	show, err := queries.ShowTask(context.Background(), task.Handle)
	if err != nil {
		t.Fatalf("ShowTask() error = %v", err)
	}
	explanation, err := queries.ExplainTask(context.Background(), task.Handle)
	if err != nil {
		t.Fatalf("ExplainTask() error = %v", err)
	}
	if !reflect.DeepEqual(show.Evidence, evidence) || !reflect.DeepEqual(explanation.Evidence, evidence) {
		t.Fatalf("task evidence diverged: show=%#v explain=%#v", show.Evidence, explanation.Evidence)
	}
	if show.Summary.Head != evidence.Candidate.HeadRevision || show.Summary.Activity != "authenticated_report" ||
		show.Summary.Validation != "accepted" || show.Summary.BlockedBy != "none" ||
		show.Summary.Attention != "cleanup_hold" || !repository.taskEvidenceCalled {
		t.Fatalf("task summary = %#v, evidence read = %v", show.Summary, repository.taskEvidenceCalled)
	}
}

func TestQueries_UseAtomicTaskEvidenceRepositoryWhenAvailable(t *testing.T) {
	task := queryTask("task-atomic-view", domain.TaskWorking, 5)
	repository := &queryRepository{
		tasks: []domain.Task{task}, stateVersion: task.StateVersion,
		taskEvidence: TaskEvidenceView{
			Candidate:  CandidateEvidenceView{Status: CandidateEvidenceNone},
			Activity:   ActivityEvidenceView{Status: ActivityEvidenceNone},
			Decision:   DecisionEvidenceView{Status: DecisionEvidenceNone},
			Validation: ValidationEvidenceView{Status: ValidationEvidenceNotStarted},
			Delivery:   DeliveryEvidenceView{Status: DeliveryEvidenceNotStarted},
			Cleanup:    CleanupEvidenceView{Status: CleanupEvidenceNotStarted},
		},
	}
	queries, err := NewQueries(QueryConfig{Repository: repository, Clock: time.Now})
	if err != nil {
		t.Fatalf("NewQueries() error = %v", err)
	}
	if _, err := queries.ShowTask(context.Background(), task.Handle); err != nil {
		t.Fatalf("ShowTask() error = %v", err)
	}
	if !repository.observationCalled {
		t.Fatal("ShowTask() did not use the atomic task observation")
	}
	if _, err := queries.Fleet(context.Background()); err != nil {
		t.Fatalf("Fleet() error = %v", err)
	}
	if !repository.evidenceSnapshotCalled {
		t.Fatal("Fleet() did not use the atomic task evidence snapshot")
	}
}

func TestQueries_ExplainFailedCandidateFromDurableJudgment(t *testing.T) {
	for _, test := range []struct {
		name      string
		reason    domain.CandidateReason
		wantCode  string
		wantCause string
	}{
		{name: "local validation failure", reason: domain.CandidateValidationFailed, wantCode: "candidate_validation_failed", wantCause: "required local validation check failed"},
		{name: "forge validation failure", reason: domain.CandidateForgeFailed, wantCode: "candidate_forge_failed", wantCause: "required forge check failed"},
	} {
		t.Run(test.name, func(t *testing.T) {
			task := queryTask("task-candidate-failed", domain.TaskFailed, 8)
			repository := &queryRepository{
				tasks: []domain.Task{task},
				candidateJudgment: domain.CandidateJudgment{
					Outcome: domain.CandidateRejected,
					Reason:  test.reason,
				},
			}
			queries, err := NewQueries(QueryConfig{Repository: repository, Clock: time.Now})
			if err != nil {
				t.Fatalf("NewQueries() error = %v", err)
			}
			explanation, err := queries.ExplainTask(context.Background(), task.Handle)
			if err != nil {
				t.Fatalf("ExplainTask() error = %v", err)
			}
			if !repository.candidateCalled || explanation.ReasonCode != test.wantCode ||
				!strings.Contains(explanation.LikelyRootCause, test.wantCause) ||
				len(explanation.NextSafeActions) != 1 || explanation.NextSafeActions[0] != ActionInspectTask {
				t.Fatalf("ExplainTask(rejected candidate) = %#v, judgment read = %v", explanation, repository.candidateCalled)
			}
		})
	}
}

func TestQueries_ExplainFailedCandidateFallbacksRemainFailClosed(t *testing.T) {
	task := queryTask("task-candidate-fallback", domain.TaskFailed, 9)

	t.Run("missing candidate evidence keeps generic task failure", func(t *testing.T) {
		repository := &queryRepository{tasks: []domain.Task{task}, candidateErr: ErrNotFound}
		queries, err := NewQueries(QueryConfig{Repository: repository, Clock: time.Now})
		if err != nil {
			t.Fatalf("NewQueries() error = %v", err)
		}
		explanation, err := queries.ExplainTask(context.Background(), task.Handle)
		if err != nil || !repository.candidateCalled || explanation.ReasonCode != "task_failed" {
			t.Fatalf("ExplainTask(missing judgment) = %#v, %v, judgment read = %v", explanation, err, repository.candidateCalled)
		}
	})

	t.Run("unreadable candidate evidence returns safe internal failure", func(t *testing.T) {
		repository := &queryRepository{tasks: []domain.Task{task}, candidateErr: errors.New("private candidate store failure")}
		queries, err := NewQueries(QueryConfig{Repository: repository, Clock: time.Now})
		if err != nil {
			t.Fatalf("NewQueries() error = %v", err)
		}
		if _, err := queries.ExplainTask(context.Background(), task.Handle); failureCode(err) != domain.ErrorInternal {
			t.Fatalf("ExplainTask(unreadable judgment) error = %v, want safe internal failure", err)
		}
	})

	t.Run("non-rejected candidate judgment keeps generic task failure", func(t *testing.T) {
		repository := &queryRepository{
			tasks:             []domain.Task{task},
			candidateJudgment: domain.CandidateJudgment{Outcome: domain.CandidateAccepted, Reason: domain.CandidateEvidenceAccepted},
		}
		queries, err := NewQueries(QueryConfig{Repository: repository, Clock: time.Now})
		if err != nil {
			t.Fatalf("NewQueries() error = %v", err)
		}
		explanation, err := queries.ExplainTask(context.Background(), task.Handle)
		if err != nil || explanation.ReasonCode != "task_failed" {
			t.Fatalf("ExplainTask(non-rejected judgment) = %#v, %v", explanation, err)
		}
	})
}

func TestQueries_RejectInvalidRefsAndTranslateRepositoryFailuresSafely(t *testing.T) {
	privateCause := errors.New("private database path and row detail")
	tests := []struct {
		name     string
		invoke   func(*Queries) error
		repoErr  error
		wantCode domain.ErrorCode
		wantRead bool
	}{
		{
			name: "invalid task handle",
			invoke: func(queries *Queries) error {
				_, err := queries.ShowTask(context.Background(), "../escape")
				return err
			},
			wantCode: domain.ErrorInvalidArgument,
		},
		{
			name: "missing task",
			invoke: func(queries *Queries) error {
				_, err := queries.ExplainTask(context.Background(), "missing-task")
				return err
			},
			repoErr:  ErrNotFound,
			wantCode: domain.ErrorNotFound,
			wantRead: true,
		},
		{
			name: "invalid operation id",
			invoke: func(queries *Queries) error {
				_, err := queries.Operation(context.Background(), "bad id")
				return err
			},
			wantCode: domain.ErrorInvalidArgument,
		},
		{
			name: "private repository failure",
			invoke: func(queries *Queries) error {
				_, err := queries.ShowTask(context.Background(), "task-0001")
				return err
			},
			repoErr:  privateCause,
			wantCode: domain.ErrorInternal,
			wantRead: true,
		},
		{
			name: "cancelled read",
			invoke: func(queries *Queries) error {
				_, err := queries.ListTasks(context.Background())
				return err
			},
			repoErr:  context.Canceled,
			wantCode: domain.ErrorDeadlineExceeded,
			wantRead: true,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			repository := &queryRepository{readErr: test.repoErr}
			queries, err := NewQueries(QueryConfig{Repository: repository, Clock: time.Now})
			if err != nil {
				t.Fatalf("NewQueries() error = %v", err)
			}
			err = test.invoke(queries)
			var failure *domain.Failure
			if !errors.As(err, &failure) || failure.Code != test.wantCode {
				t.Fatalf("query error = %v, want Failure code %q", err, test.wantCode)
			}
			if strings.Contains(err.Error(), privateCause.Error()) {
				t.Fatalf("query error leaked private cause: %q", err)
			}
			if repository.readCalled != test.wantRead {
				t.Fatalf("repository read called = %v, want %v", repository.readCalled, test.wantRead)
			}
		})
	}
}

func TestNewQueries_RejectsMissingDependencies(t *testing.T) {
	if _, err := NewQueries(QueryConfig{Clock: time.Now}); err == nil {
		t.Fatal("NewQueries(nil repository) error = nil")
	}
	if _, err := NewQueries(QueryConfig{Repository: &queryRepository{}}); err == nil {
		t.Fatal("NewQueries(nil clock) error = nil")
	}
}

func TestQueries_FailureBranchesAndClosedStateExplanations(t *testing.T) {
	privateCause := errors.New("private adapter detail")

	t.Run("diagnostic read failure", func(t *testing.T) {
		queries, err := NewQueries(QueryConfig{Repository: &queryRepository{readErr: privateCause}, Clock: time.Now})
		if err != nil {
			t.Fatalf("NewQueries() error = %v", err)
		}
		if _, err := queries.Diagnose(context.Background()); failureCode(err) != domain.ErrorInternal {
			t.Fatalf("Diagnose() error = %v, want internal failure", err)
		}
	})

	t.Run("fleet list failure", func(t *testing.T) {
		queries, err := NewQueries(QueryConfig{Repository: &queryRepository{readErr: privateCause}, Clock: time.Now})
		if err != nil {
			t.Fatalf("NewQueries() error = %v", err)
		}
		if _, err := queries.Fleet(context.Background()); failureCode(err) != domain.ErrorInternal {
			t.Fatalf("Fleet() error = %v, want internal failure", err)
		}
	})

	t.Run("state version failure", func(t *testing.T) {
		queries, err := NewQueries(QueryConfig{Repository: &queryRepository{stateVersionErr: privateCause}, Clock: time.Now})
		if err != nil {
			t.Fatalf("NewQueries() error = %v", err)
		}
		if _, err := queries.ListTasks(context.Background()); failureCode(err) != domain.ErrorInternal {
			t.Fatalf("ListTasks() error = %v, want internal failure", err)
		}
	})

	t.Run("operation read failure", func(t *testing.T) {
		queries, err := NewQueries(QueryConfig{Repository: &queryRepository{readErr: ErrNotFound}, Clock: time.Now})
		if err != nil {
			t.Fatalf("NewQueries() error = %v", err)
		}
		if _, err := queries.Operation(context.Background(), "op-0001"); failureCode(err) != domain.ErrorNotFound {
			t.Fatalf("Operation() error = %v, want not-found failure", err)
		}
	})

	t.Run("future task clamps elapsed duration", func(t *testing.T) {
		now := time.Date(2026, time.August, 8, 20, 0, 0, 0, time.UTC)
		task := queryTask("task-0001", domain.TaskPrepared, 1)
		task.CreatedAt = now.Add(time.Hour)
		queries, err := NewQueries(QueryConfig{Repository: &queryRepository{tasks: []domain.Task{task}}, Clock: func() time.Time { return now }})
		if err != nil {
			t.Fatalf("NewQueries() error = %v", err)
		}
		fleet, err := queries.Fleet(context.Background())
		if err != nil {
			t.Fatalf("Fleet() error = %v", err)
		}
		if fleet.Tasks[0].ElapsedMs != 0 {
			t.Fatalf("Fleet() elapsed = %d, want zero", fleet.Tasks[0].ElapsedMs)
		}
	})

	for _, state := range []domain.TaskState{
		domain.TaskPrepared,
		domain.TaskBlocked,
		domain.TaskUnknown,
		domain.TaskFailed,
		domain.TaskCancelled,
		domain.TaskCleanupHeld,
		domain.TaskDelivered,
		domain.TaskCleaned,
		domain.TaskWorking,
	} {
		reason, explanation, rootCause, actions := explainState(state)
		if reason == "" || explanation == "" || rootCause == "" || len(actions) == 0 {
			t.Fatalf("explainState(%q) returned incomplete output", state)
		}
	}

	if err := newSafeFailure(domain.ErrorCode("invalid"), false, "message", "hint", nil); err == nil {
		t.Fatal("newSafeFailure(invalid code) error = nil")
	}
}

func failureCode(err error) domain.ErrorCode {
	var failure *domain.Failure
	if !errors.As(err, &failure) {
		return ""
	}
	return failure.Code
}

type queryRepository struct {
	tasks                  []domain.Task
	operation              domain.OperationRecord
	preparation            ManagedRunPreparation
	preparationErr         error
	stateVersion           int64
	stateVersionErr        error
	readErr                error
	readCalled             bool
	snapshotCalled         bool
	candidateJudgment      domain.CandidateJudgment
	candidateEvidence      *domain.SealedDeliveryEvidence
	candidateErr           error
	candidateCalled        bool
	taskEvidence           TaskEvidenceView
	taskEvidenceErr        error
	taskEvidenceCalled     bool
	observationCalled      bool
	evidenceSnapshotCalled bool
}

func (repository *queryRepository) ReadTaskObservation(ctx context.Context, handle string) (TaskObservation, error) {
	repository.observationCalled = true
	repository.taskEvidenceCalled = true
	task, err := repository.GetTask(ctx, handle)
	if err != nil {
		return TaskObservation{}, err
	}
	return TaskObservation{Task: task, Evidence: repository.taskEvidence}, nil
}

func (repository *queryRepository) TaskEvidenceSnapshot(context.Context) ([]TaskObservation, int64, error) {
	repository.evidenceSnapshotCalled = true
	repository.readCalled = true
	repository.snapshotCalled = true
	if repository.readErr != nil {
		return nil, 0, repository.readErr
	}
	if repository.stateVersionErr != nil {
		return nil, 0, repository.stateVersionErr
	}
	observations := make([]TaskObservation, 0, len(repository.tasks))
	for _, task := range repository.tasks {
		observations = append(observations, TaskObservation{Task: task, Evidence: repository.taskEvidence})
	}
	return observations, repository.stateVersion, nil
}

func (repository *queryRepository) ReadTaskEvidence(context.Context, string) (TaskEvidenceView, error) {
	repository.taskEvidenceCalled = true
	if repository.taskEvidenceErr != nil {
		return TaskEvidenceView{}, repository.taskEvidenceErr
	}
	return repository.taskEvidence, nil
}

func (repository *queryRepository) LatestCandidateEvidence(
	context.Context,
	string,
) (*domain.SealedDeliveryEvidence, domain.CandidateJudgment, error) {
	repository.candidateCalled = true
	return repository.candidateEvidence, repository.candidateJudgment, repository.candidateErr
}

func (repository *queryRepository) GetManagedRunPreparation(context.Context, string) (ManagedRunPreparation, error) {
	repository.readCalled = true
	if repository.readErr != nil {
		return ManagedRunPreparation{}, repository.readErr
	}
	if repository.preparationErr != nil {
		return ManagedRunPreparation{}, repository.preparationErr
	}
	return repository.preparation, nil
}

type queryHarnesses struct {
	adapter WorkerHarnessAdapter
	err     error
}

func (harnesses *queryHarnesses) ResolveWorkerHarness(string) (WorkerHarnessAdapter, error) {
	return harnesses.adapter, harnesses.err
}

type queryHarnessAdapter struct {
	request    WorkerLaunchRequest
	called     bool
	err        error
	descriptor *WorkerLaunchDescriptor
}

func (*queryHarnessAdapter) ID() string { return "codex" }

func (*queryHarnessAdapter) ProbeVersion(context.Context) (HarnessVersionProbe, error) {
	return HarnessVersionProbe{Version: "codex-cli 1.2.3", Availability: HarnessAvailable}, nil
}

func (adapter *queryHarnessAdapter) BuildLaunchDescriptor(_ context.Context, request WorkerLaunchRequest) (WorkerLaunchDescriptor, error) {
	adapter.called = true
	adapter.request = request
	if adapter.err != nil {
		return WorkerLaunchDescriptor{}, adapter.err
	}
	if adapter.descriptor != nil {
		return *adapter.descriptor, nil
	}
	return WorkerLaunchDescriptor{
		ProfileID: request.ProfileID, Harness: "codex", Executable: "/usr/local/bin/codex",
		Arguments: []string{"--model", "reviewed-model"}, WorkingDirectory: request.WorkingDirectory,
		EnvironmentKeys:     []string{"COMIS_EXECUTION_ATTACHMENT"},
		EnvironmentBindings: map[string]string{"COMIS_EXECUTION_ATTACHMENT": request.Attachment.MountSocketPath},
		TerminalAllowEntry:  "terminal-codex-reviewed", Attachment: request.Attachment,
		ExpectedAcknowledgement: LaunchAcknowledgement{
			TaskHandle: request.TaskHandle, ManagedRunID: request.ManagedRunID,
			WorkspaceLeaseID: request.WorkspaceLeaseID, WorkingDirectory: request.WorkingDirectory,
			BriefRevision: request.BriefRevision, BriefRevisionHash: request.BriefRevisionHash,
		},
	}, nil
}

func (*queryHarnessAdapter) ClassifySemanticActivity(HarnessObservation) SemanticActivityResult {
	return SemanticActivityResult{State: ActivityUnknown, Reason: SemanticReasonMissing}
}

func (repository *queryRepository) CreateTask(context.Context, domain.Task) error { return nil }

func (repository *queryRepository) RecordOperation(context.Context, domain.OperationRecord) error {
	return nil
}

func (repository *queryRepository) ListTasks(context.Context) ([]domain.Task, error) {
	repository.readCalled = true
	return repository.tasks, repository.readErr
}

func (repository *queryRepository) GetTask(_ context.Context, handle string) (domain.Task, error) {
	repository.readCalled = true
	if repository.readErr != nil {
		return domain.Task{}, repository.readErr
	}
	for _, task := range repository.tasks {
		if task.Handle == handle {
			return task, nil
		}
	}
	return domain.Task{}, ErrNotFound
}

func (repository *queryRepository) GetOperation(context.Context, string) (domain.OperationRecord, error) {
	repository.readCalled = true
	if repository.readErr != nil {
		return domain.OperationRecord{}, repository.readErr
	}
	return repository.operation, nil
}

func (repository *queryRepository) CurrentStateVersion(context.Context) (int64, error) {
	repository.readCalled = true
	if repository.stateVersionErr != nil {
		return 0, repository.stateVersionErr
	}
	return repository.stateVersion, repository.readErr
}

func (repository *queryRepository) TaskSnapshot(context.Context) ([]domain.Task, int64, error) {
	repository.readCalled = true
	repository.snapshotCalled = true
	if repository.readErr != nil {
		return nil, 0, repository.readErr
	}
	if repository.stateVersionErr != nil {
		return nil, 0, repository.stateVersionErr
	}
	return repository.tasks, repository.stateVersion, nil
}

func queryTask(handle string, state domain.TaskState, stateVersion int64) domain.Task {
	created := time.Date(2026, time.August, 8, 20, 0, 0, 0, time.UTC)
	task := domain.Task{
		SchemaVersion:      1,
		Handle:             handle,
		ServiceInstanceID:  "service-instance-0001",
		State:              state,
		Shape:              domain.ShapeShip,
		RepositoryID:       "product-api",
		BaseRevision:       strings.Repeat("a", 40),
		BriefRevision:      1,
		AcceptanceCriteria: []string{"The requested behavior is proven."},
		ValidationProfile:  "go-default",
		DeliveryMode:       domain.DeliveryPullRequest,
		WorkerProfileID:    "codex-standard",
		StateVersion:       stateVersion,
		CreatedAt:          created,
		UpdatedAt:          created.Add(time.Minute),
	}
	if state != domain.TaskPrepared && state != domain.TaskReconciling && state != domain.TaskUnknown {
		task.ManagedRunID = "managed-run-0001"
		task.WorkspaceLeaseID = "workspace-lease-0001"
	}
	pinned, err := task.PinBriefRevision()
	if err != nil {
		panic(err)
	}
	return pinned
}

func queryCandidateEvidence(t *testing.T, task domain.Task, producedAt time.Time) *domain.SealedDeliveryEvidence {
	t.Helper()
	head := strings.Repeat("b", 40)
	sealed, err := domain.SealDeliveryEvidence(domain.DeliveryEvidenceBundle{
		SchemaVersion: 1, TaskHandle: task.Handle, RepositoryIdentity: task.RepositoryID,
		BaseRevision: task.BaseRevision, HeadRevision: head, WorktreeCleanliness: domain.WorktreeClean,
		ValidationReceipts: []domain.ValidationEvidenceReceipt{{
			CheckID: "unit", ProgramID: "go-test", HeadRevision: head,
			Conclusion: domain.CheckPassed, Required: true, OutputHash: strings.Repeat("c", 64),
			StartedAt: producedAt.Add(-time.Second), CompletedAt: producedAt,
		}},
		ForgeEvidence: &domain.ForgeEvidence{
			Repository: task.RepositoryID, PullRequestID: "github-pr-17", HeadRevision: head,
			CheckConclusions: []domain.ForgeCheckEvidence{{Name: "ci/unit", Conclusion: domain.CheckPassed}},
		},
		ProducedAt: producedAt, ExpiresAt: producedAt.Add(time.Hour),
	})
	if err != nil {
		t.Fatalf("SealDeliveryEvidence() error = %v", err)
	}
	return sealed
}

func queryOperation(id string, stateVersion int64) domain.OperationRecord {
	created := time.Date(2026, time.August, 8, 20, 0, 0, 0, time.UTC)
	return domain.OperationRecord{
		SchemaVersion: 1,
		ID:            id,
		Command:       "PrepareTask",
		SubjectDigest: strings.Repeat("b", 64),
		Status:        domain.OperationCompleted,
		StateVersion:  stateVersion,
		CreatedAt:     created,
		UpdatedAt:     created.Add(time.Minute),
	}
}
