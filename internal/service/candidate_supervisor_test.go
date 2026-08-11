package service

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/comisai/comis-dev-crew/internal/application"
	"github.com/comisai/comis-dev-crew/internal/delivery"
	"github.com/comisai/comis-dev-crew/internal/domain"
	"github.com/comisai/comis-dev-crew/internal/forge"
	devgit "github.com/comisai/comis-dev-crew/internal/git"
	"github.com/comisai/comis-dev-crew/internal/validation"
)

func TestCandidateSupervisor_BuildsShipEvidenceFromChecksAndRereadForgeTruth(t *testing.T) {
	fixture := newCandidateSupervisorFixture(t, domain.ShapeShip)
	supervisor, err := newCandidateSupervisor(fixture.config())
	if err != nil {
		t.Fatalf("newCandidateSupervisor() error = %v", err)
	}
	updated, judgment, err := supervisor.ValidateTask(context.Background(), fixture.task.Handle)
	if err != nil {
		t.Fatalf("ValidateTask() error = %v", err)
	}
	if judgment.Outcome != domain.CandidateAccepted || updated.State != domain.TaskCandidateComplete {
		t.Fatalf("ValidateTask() = %#v, %#v", updated, judgment)
	}
	if fixture.runner.calls != 1 || fixture.pullRequests.calls != 1 || fixture.artifact.calls != 0 {
		t.Fatalf("dependency calls: runner=%d forge=%d artifact=%d", fixture.runner.calls, fixture.pullRequests.calls, fixture.artifact.calls)
	}
	bundle := fixture.store.evidence.Bundle()
	if bundle.TaskHandle != fixture.task.Handle || bundle.RepositoryIdentity != fixture.task.RepositoryID ||
		bundle.HeadRevision != fixture.snapshot.HeadRevision || bundle.ForgeEvidence == nil || bundle.ReportArtifact != nil ||
		len(bundle.ValidationReceipts) != 1 || bundle.ValidationReceipts[0].Conclusion != domain.CheckPassed {
		t.Fatalf("sealed ship evidence = %#v", bundle)
	}
	if fixture.pullRequests.request.HeadRevision != fixture.snapshot.HeadRevision ||
		!reflect.DeepEqual(fixture.pullRequests.request.RequiredChecks, []string{"ci/unit"}) {
		t.Fatalf("pull request input = %#v", fixture.pullRequests.request)
	}
	if !reflect.DeepEqual(fixture.store.publicationKinds, []string{"candidate_bundle", "delivery_reference"}) ||
		!reflect.DeepEqual(fixture.store.publicationDeliveries, []string{"", "reference"}) ||
		len(fixture.store.publicationBodies) != 2 ||
		!bytes.Equal(fixture.store.publicationBodies[0], fixture.store.evidence.Canonical()) ||
		string(fixture.store.publicationBodies[1]) != fixture.pullRequests.truth.URL {
		t.Fatalf("ship evidence publications = %#v, %#v", fixture.store.publicationKinds, fixture.store.publicationDeliveries)
	}
}

func TestCandidateSupervisor_BuildsScoutEvidenceOnlyFromReviewedArtifactPath(t *testing.T) {
	fixture := newCandidateSupervisorFixture(t, domain.ShapeScout)
	supervisor, err := newCandidateSupervisor(fixture.config())
	if err != nil {
		t.Fatalf("newCandidateSupervisor() error = %v", err)
	}
	updated, judgment, err := supervisor.ValidateTask(context.Background(), fixture.task.Handle)
	if err != nil || judgment.Outcome != domain.CandidateAccepted || updated.State != domain.TaskCandidateComplete {
		t.Fatalf("ValidateTask() = %#v, %#v, %v", updated, judgment, err)
	}
	wantPath := filepath.Join(fixture.preparation.RequestedWorkspaceRoot, "report.md")
	if fixture.artifact.path != wantPath || fixture.artifact.maximumBytes != 16<<10 || fixture.artifact.mediaType != "text/markdown" {
		t.Fatalf("artifact inspection = %#v", fixture.artifact)
	}
	if fixture.pullRequests.calls != 0 || fixture.store.evidence.Bundle().ReportArtifact == nil {
		t.Fatal("scout evidence used the wrong delivery authority")
	}
	if !reflect.DeepEqual(fixture.store.publicationKinds, []string{"candidate_bundle", "report_artifact"}) ||
		!reflect.DeepEqual(fixture.store.publicationDeliveries, []string{"", "attachment"}) ||
		len(fixture.store.publicationBodies) != 2 ||
		!bytes.Equal(fixture.store.publicationBodies[1], fixture.artifact.artifact.Body) {
		t.Fatalf("scout evidence publications = %#v, %#v", fixture.store.publicationKinds, fixture.store.publicationDeliveries)
	}
}

func TestCandidateSupervisor_RefusesChangedHeadOpenDecisionAndIncompleteValidation(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(*candidateSupervisorFixture)
	}{
		{name: "changed head", mutate: func(fixture *candidateSupervisorFixture) {
			changed := fixture.snapshot
			changed.HeadRevision = strings.Repeat("c", 40)
			fixture.git.snapshots = []devgit.CandidateSnapshot{fixture.snapshot, changed}
		}},
		{name: "open decision", mutate: func(fixture *candidateSupervisorFixture) {
			fixture.store.reports = []domain.AcceptedReport{{Report: domain.WorkerReport{Kind: domain.ReportDecision, ExternalKey: "decision-one"}}}
		}},
		{name: "missing receipt", mutate: func(fixture *candidateSupervisorFixture) {
			fixture.runner.receipt = validation.Receipt{}
			fixture.runner.err = errors.New("process identity unavailable")
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := newCandidateSupervisorFixture(t, domain.ShapeShip)
			test.mutate(fixture)
			supervisor, err := newCandidateSupervisor(fixture.config())
			if err != nil {
				t.Fatalf("newCandidateSupervisor() error = %v", err)
			}
			if _, _, err := supervisor.ValidateTask(context.Background(), fixture.task.Handle); err == nil {
				t.Fatal("ValidateTask() error = nil")
			}
			if fixture.store.evidence != nil {
				t.Fatal("invalid candidate evidence was committed")
			}
		})
	}
}

func TestCandidateSupervisor_FailsClosedForUnavailableDependenciesTimeAndShape(t *testing.T) {
	if _, err := newCandidateSupervisor(candidateSupervisorConfig{}); err == nil {
		t.Fatal("newCandidateSupervisor(empty) error = nil")
	}
	fixture := newCandidateSupervisorFixture(t, domain.ShapeShip)
	supervisor, err := newCandidateSupervisor(fixture.config())
	if err != nil {
		t.Fatalf("newCandidateSupervisor() error = %v", err)
	}
	if _, _, err := (*candidateSupervisor)(nil).ValidateTask(context.Background(), fixture.task.Handle); err == nil {
		t.Fatal("ValidateTask(nil supervisor) error = nil")
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err := supervisor.ValidateTask(cancelled, fixture.task.Handle); !errors.Is(err, context.Canceled) {
		t.Fatalf("ValidateTask(cancelled) error = %v", err)
	}
	if _, _, err := supervisor.ValidateTask(context.Background(), "bad handle"); err == nil {
		t.Fatal("ValidateTask(invalid handle) error = nil")
	}
	for _, test := range []struct {
		name   string
		mutate func(*candidateSupervisorFixture)
	}{
		{name: "missing profile", mutate: func(fixture *candidateSupervisorFixture) {
			fixture.store.task.ValidationProfile = "missing-profile"
		}},
		{name: "forge failure", mutate: func(fixture *candidateSupervisorFixture) {
			fixture.pullRequests.err = errors.New("forge unavailable")
		}},
		{name: "invalid clock", mutate: func(fixture *candidateSupervisorFixture) {
			fixture.now = fixture.now.In(time.FixedZone("other", 0))
		}},
		{name: "invalid shape", mutate: func(fixture *candidateSupervisorFixture) {
			fixture.store.task.Shape = "invented"
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			candidate := newCandidateSupervisorFixture(t, domain.ShapeShip)
			test.mutate(candidate)
			configured, configureErr := newCandidateSupervisor(candidate.config())
			if configureErr != nil {
				t.Fatalf("newCandidateSupervisor() error = %v", configureErr)
			}
			if _, _, err := configured.ValidateTask(context.Background(), candidate.task.Handle); err == nil {
				t.Fatal("ValidateTask() error = nil")
			}
		})
	}
	scout := newCandidateSupervisorFixture(t, domain.ShapeScout)
	scout.artifact.err = errors.New("artifact unavailable")
	scoutSupervisor, err := newCandidateSupervisor(scout.config())
	if err != nil {
		t.Fatalf("newCandidateSupervisor(scout) error = %v", err)
	}
	if _, _, err := scoutSupervisor.ValidateTask(context.Background(), scout.task.Handle); err == nil {
		t.Fatal("ValidateTask(artifact failure) error = nil")
	}
	if candidateCleanliness(devgit.CandidateClean) != domain.WorktreeClean ||
		candidateCleanliness(devgit.CandidateDirty) != domain.WorktreeDirty ||
		candidateCleanliness("invented") != domain.WorktreeUnknown {
		t.Fatal("candidateCleanliness() did not fail closed")
	}
	decisionReports := []domain.AcceptedReport{
		{Report: domain.WorkerReport{Kind: domain.ReportDecision, ExternalKey: "decision-one"}},
		{Report: domain.WorkerReport{Kind: domain.ReportResolution, ExternalKey: "decision-one"}},
	}
	if unresolvedDecisionCount(decisionReports) != 0 {
		t.Fatal("resolved decision remained open")
	}
}

func TestCandidateSupervisor_RunRecoversDurableValidatingTaskAndJoinsCancellation(t *testing.T) {
	if err := (*candidateSupervisor)(nil).Run(context.Background()); err == nil {
		t.Fatal("Run(nil supervisor) error = nil")
	}
	alreadyCancelled, cancelAlready := context.WithCancel(context.Background())
	cancelAlready()
	fixtureForCancellation := newCandidateSupervisorFixture(t, domain.ShapeShip)
	cancelledSupervisor, err := newCandidateSupervisor(fixtureForCancellation.config())
	if err != nil {
		t.Fatalf("newCandidateSupervisor(cancelled) error = %v", err)
	}
	if err := cancelledSupervisor.Run(alreadyCancelled); !errors.Is(err, context.Canceled) {
		t.Fatalf("Run(already cancelled) error = %v", err)
	}
	fixture := newCandidateSupervisorFixture(t, domain.ShapeShip)
	ctx, cancel := context.WithCancel(context.Background())
	fixture.store.onCommit = cancel
	supervisor, err := newCandidateSupervisor(fixture.config())
	if err != nil {
		t.Fatalf("newCandidateSupervisor() error = %v", err)
	}
	if err := supervisor.Run(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("Run() error = %v, want context.Canceled", err)
	}
	if fixture.store.evidence == nil || fixture.store.task.State != domain.TaskCandidateComplete {
		t.Fatal("Run() did not recover and validate the durable task")
	}
	queueFixture := newCandidateSupervisorFixture(t, domain.ShapeShip)
	queueContext, cancelQueue := context.WithCancel(context.Background())
	queueFixture.store.list = func(ctx context.Context) ([]domain.Task, error) {
		cancelQueue()
		return nil, ctx.Err()
	}
	queueSupervisor, err := newCandidateSupervisor(queueFixture.config())
	if err != nil {
		t.Fatalf("newCandidateSupervisor(queue cancellation) error = %v", err)
	}
	if err := queueSupervisor.Run(queueContext); !errors.Is(err, context.Canceled) {
		t.Fatalf("Run(queue cancellation) error = %v", err)
	}
}

type candidateSupervisorFixture struct {
	task         domain.Task
	preparation  application.ManagedRunPreparation
	snapshot     devgit.CandidateSnapshot
	store        *candidateSupervisorStore
	git          *candidateSupervisorGit
	catalog      *validation.Catalog
	runner       *candidateSupervisorRunner
	pullRequests *candidateSupervisorPullRequests
	artifact     *candidateSupervisorArtifact
	now          time.Time
}

func newCandidateSupervisorFixture(t *testing.T, shape domain.TaskShape) *candidateSupervisorFixture {
	t.Helper()
	now := time.Date(2026, time.August, 11, 20, 0, 0, 0, time.UTC)
	task := serviceTask()
	task.Handle = "task-candidate"
	task.State = domain.TaskValidating
	task.ManagedRunID = "managed-run-candidate"
	task.WorkspaceLeaseID = "workspace-lease-candidate"
	task.Shape = shape
	if shape == domain.ShapeScout {
		task.DeliveryMode = domain.DeliveryReport
	}
	task, err := task.PinBriefRevision()
	if err != nil {
		t.Fatalf("PinBriefRevision() error = %v", err)
	}
	worktree := "/approved/worktrees/task-candidate"
	head := strings.Repeat("b", 40)
	profile := validation.Profile{
		ID: task.ValidationProfile,
		LocalChecks: []validation.LocalCheck{{
			ID: "unit", ProgramID: "go-test", Required: true, Timeout: time.Minute,
			Arguments: []validation.ArgumentTemplate{{Kind: validation.ArgumentLiteral, Value: "test"}},
		}},
		EvidenceTTL: 10 * time.Minute,
	}
	if shape == domain.ShapeShip {
		profile.ForgeChecks = []validation.ForgeCheck{{Name: "ci/unit", Required: true}}
	} else {
		profile.ArtifactRules = []validation.ArtifactRule{{
			Kind: validation.ArtifactRegularFile, RelativePath: "report.md", MediaType: "text/markdown", MaxBytes: 16 << 10,
		}}
	}
	catalog, err := validation.NewCatalog(validation.CatalogConfig{
		Programs: []validation.Program{{ID: "go-test", Executable: "/usr/bin/go"}}, Profiles: []validation.Profile{profile},
	})
	if err != nil {
		t.Fatalf("NewCatalog() error = %v", err)
	}
	snapshot := devgit.CandidateSnapshot{
		RepositoryID: task.RepositoryID, WorktreePath: worktree, Branch: "devcrew/task-candidate",
		HeadRevision: head, Cleanliness: devgit.CandidateClean,
	}
	receipt := validation.Receipt{
		OperationID: candidateValidationOperationID(task.Handle, head, "unit"), TaskHandle: task.Handle, ProfileID: task.ValidationProfile,
		CheckID: "unit", ProgramID: "go-test", HeadRevision: head,
		StartedAt: now.Add(-2 * time.Minute), CompletedAt: now.Add(-time.Minute), ExitCode: 0, Passed: true,
		OutputHash: strings.Repeat("d", 64), OutputBytes: 16,
	}
	fixture := &candidateSupervisorFixture{
		task:        task,
		preparation: application.ManagedRunPreparation{RequestedWorkspaceRoot: worktree},
		snapshot:    snapshot, catalog: catalog, now: now,
	}
	fixture.store = &candidateSupervisorStore{task: task, preparation: fixture.preparation}
	fixture.git = &candidateSupervisorGit{snapshots: []devgit.CandidateSnapshot{snapshot, snapshot}}
	fixture.runner = &candidateSupervisorRunner{receipt: receipt}
	fixture.pullRequests = &candidateSupervisorPullRequests{truth: forge.PullRequestTruth{
		URL: "https://example.com/pull/17",
		Evidence: domain.ForgeEvidence{
			Repository: task.RepositoryID, PullRequestID: "github-pr-17", HeadRevision: head,
			CheckConclusions: []domain.ForgeCheckEvidence{{Name: "ci/unit", Conclusion: domain.CheckPassed}},
		},
	}}
	reportBody := []byte("report body")
	fixture.artifact = &candidateSupervisorArtifact{artifact: delivery.InspectedReportArtifact{
		ReportArtifactEvidence: domain.ReportArtifactEvidence{
			ContentHash: fmt.Sprintf("%x", sha256.Sum256(reportBody)), Size: int64(len(reportBody)), MediaType: "text/markdown",
		},
		Body: reportBody,
	}}
	return fixture
}

func (fixture *candidateSupervisorFixture) config() candidateSupervisorConfig {
	return candidateSupervisorConfig{
		Store: fixture.store, Git: fixture.git, Catalog: fixture.catalog, Runner: fixture.runner,
		PullRequests: fixture.pullRequests, InspectArtifact: fixture.artifact.inspect,
		Clock: func() time.Time { return fixture.now }, PollInterval: time.Millisecond,
	}
}

type candidateSupervisorStore struct {
	task                  domain.Task
	preparation           application.ManagedRunPreparation
	reports               []domain.AcceptedReport
	evidence              *domain.SealedDeliveryEvidence
	publicationKinds      []string
	publicationDeliveries []string
	publicationBodies     [][]byte
	onCommit              func()
	list                  func(context.Context) ([]domain.Task, error)
}

func (store *candidateSupervisorStore) ListTasks(ctx context.Context) ([]domain.Task, error) {
	if store.list != nil {
		return store.list(ctx)
	}
	return []domain.Task{store.task}, nil
}

func (store *candidateSupervisorStore) GetTask(context.Context, string) (domain.Task, error) {
	return store.task, nil
}

func (store *candidateSupervisorStore) GetManagedRunPreparation(context.Context, string) (application.ManagedRunPreparation, error) {
	return store.preparation, nil
}

func (store *candidateSupervisorStore) ListAcceptedReports(context.Context, string) ([]domain.AcceptedReport, error) {
	return append([]domain.AcceptedReport(nil), store.reports...), nil
}

func (store *candidateSupervisorStore) CommitCandidateEvidence(
	_ context.Context,
	_ string,
	evidence *domain.SealedDeliveryEvidence,
	requiredLocalChecks []string,
	requiredForgeChecks []string,
	judgedAt time.Time,
) (domain.Task, domain.CandidateJudgment, error) {
	store.evidence = evidence
	judgment := domain.JudgeCandidate(domain.CandidateJudgeInput{
		Task: store.task, Evidence: evidence, RequiredLocalChecks: requiredLocalChecks,
		RequiredForgeChecks: requiredForgeChecks, Now: judgedAt,
	})
	updated := store.task
	if judgment.Outcome == domain.CandidateAccepted {
		updated.State = domain.TaskCandidateComplete
	}
	store.task = updated
	if store.onCommit != nil {
		store.onCommit()
	}
	return updated, judgment, nil
}

type candidateSupervisorGit struct {
	snapshots []devgit.CandidateSnapshot
	calls     int
}

func (git *candidateSupervisorGit) InspectCandidate(context.Context, devgit.CandidateSnapshotRequest) (devgit.CandidateSnapshot, error) {
	index := git.calls
	git.calls++
	if index >= len(git.snapshots) {
		return devgit.CandidateSnapshot{}, errors.New("snapshot unavailable")
	}
	return git.snapshots[index], nil
}

type candidateSupervisorRunner struct {
	receipt validation.Receipt
	err     error
	calls   int
}

func (runner *candidateSupervisorRunner) Run(context.Context, validation.RunRequest) (validation.Receipt, error) {
	runner.calls++
	return runner.receipt, runner.err
}

type candidateSupervisorPullRequests struct {
	truth   forge.PullRequestTruth
	request forge.PullRequestRequest
	calls   int
	err     error
}

func (pullRequests *candidateSupervisorPullRequests) DeliverPullRequest(
	_ context.Context,
	request forge.PullRequestRequest,
) (forge.PullRequestTruth, error) {
	pullRequests.calls++
	pullRequests.request = request
	return pullRequests.truth, pullRequests.err
}

type candidateSupervisorArtifact struct {
	artifact     delivery.InspectedReportArtifact
	path         string
	maximumBytes int64
	mediaType    string
	calls        int
	err          error
}

func (artifact *candidateSupervisorArtifact) inspect(
	_ context.Context,
	path string,
	maximumBytes int64,
	mediaType string,
) (delivery.InspectedReportArtifact, error) {
	artifact.calls++
	artifact.path = path
	artifact.maximumBytes = maximumBytes
	artifact.mediaType = mediaType
	return artifact.artifact, artifact.err
}
