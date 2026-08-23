package service

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/comisai/comis-dev-crew/internal/application"
	"github.com/comisai/comis-dev-crew/internal/domain"
	"github.com/comisai/comis-dev-crew/internal/validation"
)

type candidatePathPolicyGit struct {
	*candidateSupervisorGit
	view  application.TaskDiffView
	calls int
	err   error
}

func (git *candidatePathPolicyGit) InspectTaskDiff(
	_ context.Context,
	_ application.TaskDiffRequest,
) (application.TaskDiffView, error) {
	git.calls++
	return git.view, git.err
}

func TestTaskDiffTotalsRejectContradictoryNumericEvidence(t *testing.T) {
	for _, test := range []struct {
		name    string
		changes []application.TaskFileChange
		totals  application.TaskDiffTotals
		want    bool
	}{
		{name: "binary totals match", changes: []application.TaskFileChange{{Path: "asset.bin", Binary: true}},
			totals: application.TaskDiffTotals{Files: 1, BinaryFiles: 1}, want: true},
		{name: "negative change is refused", changes: []application.TaskFileChange{{Path: "report.md", Added: -1}},
			totals: application.TaskDiffTotals{Files: 1}},
		{name: "change exceeds aggregate", changes: []application.TaskFileChange{{Path: "report.md", Added: 2}},
			totals: application.TaskDiffTotals{Files: 1, Added: 1}},
		{name: "binary aggregate differs", changes: []application.TaskFileChange{{Path: "asset.bin", Binary: true}},
			totals: application.TaskDiffTotals{Files: 1}},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := taskDiffTotalsMatch(test.changes, test.totals); got != test.want {
				t.Fatalf("taskDiffTotalsMatch() = %t, want %t", got, test.want)
			}
		})
	}
}

func TestCandidatePathPolicyRejectsUnavailableGitAndInvalidTime(t *testing.T) {
	fixture := newCandidateSupervisorFixture(t, domain.ShapeScout)
	profile, err := fixture.catalog.ResolveProfile(fixture.task.ValidationProfile)
	if err != nil {
		t.Fatal(err)
	}
	validView := application.TaskDiffView{
		TaskHandle: fixture.task.Handle, RepositoryID: fixture.task.RepositoryID,
		BaseRevision: fixture.task.BaseRevision, HeadRevision: fixture.snapshot.HeadRevision,
		Committed:       []application.TaskFileChange{{Path: "report.md", Added: 1}},
		CommittedTotals: application.TaskDiffTotals{Files: 1, Added: 1}, Uncommitted: []application.TaskFileChange{},
	}
	for _, test := range []struct {
		name    string
		ctx     context.Context
		gitErr  error
		clock   application.Clock
		wantErr error
	}{
		{name: "zero observation time", ctx: context.Background(), clock: func() time.Time { return time.Time{} }},
		{name: "Git evidence unavailable", ctx: context.Background(), gitErr: errors.New("fixture unavailable"),
			clock: func() time.Time { return fixture.now }},
		{name: "cancelled Git observation", ctx: cancelledCandidatePathContext(), gitErr: errors.New("fixture cancelled"),
			clock: func() time.Time { return fixture.now }, wantErr: context.Canceled},
		{name: "regressive evidence time", ctx: context.Background(), clock: candidatePathRegressiveClock(fixture.now)},
	} {
		t.Run(test.name, func(t *testing.T) {
			git := &candidatePathPolicyGit{candidateSupervisorGit: fixture.git, view: validView, err: test.gitErr}
			supervisor := &candidateSupervisor{config: candidateSupervisorConfig{Git: git, Clock: test.clock}}
			_, _, gotErr := supervisor.inspectCandidatePathPolicy(
				test.ctx, fixture.task, profile, fixture.snapshot, nil, domain.CandidateJudgment{},
			)
			if gotErr == nil || (test.wantErr != nil && !errors.Is(gotErr, test.wantErr)) {
				t.Fatalf("inspectCandidatePathPolicy() error = %v, want %v", gotErr, test.wantErr)
			}
		})
	}
}

func TestCandidatePathPolicyRefusesUnsealableReceipt(t *testing.T) {
	fixture := newCandidateSupervisorFixture(t, domain.ShapeScout)
	profile := validation.Profile{EvidenceTTL: time.Minute}
	receipt := domain.ValidationEvidenceReceipt{
		CheckID: validation.CandidatePathPolicyCheckID, ProgramID: candidatePathPolicyProgramID,
		HeadRevision: strings.Repeat("b", 40), Conclusion: domain.CheckFailed, Required: true,
		OutputHash: strings.Repeat("d", 64), StartedAt: fixture.now, CompletedAt: fixture.now,
	}
	supervisor := &candidateSupervisor{config: fixture.config()}
	if _, _, err := supervisor.commitCandidatePathPolicyEvidence(
		context.Background(), domain.Task{}, profile, fixture.snapshot, 0, receipt,
	); err == nil {
		t.Fatal("commitCandidatePathPolicyEvidence(unsealable) error = nil")
	}
}

func cancelledCandidatePathContext() context.Context {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	return ctx
}

func candidatePathRegressiveClock(now time.Time) application.Clock {
	calls := 0
	return func() time.Time {
		calls++
		if calls == 1 {
			return now
		}
		return now.Add(-time.Second)
	}
}

func (git *candidateSupervisorGit) InspectTaskDiff(
	_ context.Context,
	request application.TaskDiffRequest,
) (application.TaskDiffView, error) {
	return application.TaskDiffView{
		TaskHandle: request.TaskHandle, RepositoryID: request.RepositoryID,
		BaseRevision: request.BaseRevision, HeadRevision: git.promotionSnapshot.HeadRevision,
		Committed:       []application.TaskFileChange{{Path: "report.md", Added: 1}},
		CommittedTotals: application.TaskDiffTotals{Files: 1, Added: 1},
		Uncommitted:     []application.TaskFileChange{},
	}, nil
}

func TestCandidateSupervisorRejectsScoutCandidateWithUnexpectedCommittedPath(t *testing.T) {
	fixture := newCandidateSupervisorFixture(t, domain.ShapeScout)
	policyGit := &candidatePathPolicyGit{
		candidateSupervisorGit: fixture.git,
		view: application.TaskDiffView{
			TaskHandle: fixture.task.Handle, RepositoryID: fixture.task.RepositoryID,
			BaseRevision: fixture.task.BaseRevision, HeadRevision: fixture.snapshot.HeadRevision,
			Committed: []application.TaskFileChange{
				{Path: "generated/w11-forbidden-policy.txt", Added: 1},
				{Path: "report.md", Added: 28},
			},
			CommittedTotals: application.TaskDiffTotals{Files: 2, Added: 29},
			Uncommitted:     []application.TaskFileChange{},
		},
	}
	config := fixture.config()
	config.Git = policyGit
	supervisor, err := newCandidateSupervisor(config)
	if err != nil {
		t.Fatalf("newCandidateSupervisor() error = %v", err)
	}
	updated, judgment, err := supervisor.ValidateTask(context.Background(), fixture.task.Handle)
	if err != nil {
		t.Fatalf("ValidateTask() error = %v", err)
	}
	if judgment.Outcome != domain.CandidateRejected || judgment.Reason != domain.CandidateValidationFailed ||
		updated.State != domain.TaskFailed {
		t.Fatalf("unexpected-path candidate = task %q judgment %#v, want failed validation", updated.State, judgment)
	}
	if policyGit.calls != 1 || fixture.runner.calls != 0 || fixture.artifact.calls != 0 ||
		fixture.pullRequests.calls != 0 || len(fixture.store.publicationKinds) != 0 {
		t.Fatalf("unexpected-path effects = diff %d validation %d artifact %d forge %d publications %d",
			policyGit.calls, fixture.runner.calls, fixture.artifact.calls,
			fixture.pullRequests.calls, len(fixture.store.publicationKinds))
	}
}

func TestCandidateSupervisorRefusesIncompleteOrRenamedPathEvidence(t *testing.T) {
	for _, test := range []struct {
		name       string
		mutate     func(*application.TaskDiffView)
		wantReason domain.CandidateReason
		wantState  domain.TaskState
	}{
		{
			name: "truncated file inventory remains unknown",
			mutate: func(view *application.TaskDiffView) {
				view.FileListTruncated = true
			},
			wantReason: domain.CandidateValidationUnknown, wantState: domain.TaskValidating,
		},
		{
			name: "inconsistent totals remain unknown",
			mutate: func(view *application.TaskDiffView) {
				view.CommittedTotals.Files = 2
			},
			wantReason: domain.CandidateValidationUnknown, wantState: domain.TaskValidating,
		},
		{
			name: "rename source outside policy is rejected",
			mutate: func(view *application.TaskDiffView) {
				view.Committed[0].PreviousPath = "generated/previous-report.md"
			},
			wantReason: domain.CandidateValidationFailed, wantState: domain.TaskFailed,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := newCandidateSupervisorFixture(t, domain.ShapeScout)
			view := application.TaskDiffView{
				TaskHandle: fixture.task.Handle, RepositoryID: fixture.task.RepositoryID,
				BaseRevision: fixture.task.BaseRevision, HeadRevision: fixture.snapshot.HeadRevision,
				Committed:       []application.TaskFileChange{{Path: "report.md", Added: 1}},
				CommittedTotals: application.TaskDiffTotals{Files: 1, Added: 1},
				Uncommitted:     []application.TaskFileChange{},
			}
			test.mutate(&view)
			policyGit := &candidatePathPolicyGit{candidateSupervisorGit: fixture.git, view: view}
			commits := 0
			fixture.store.onCommit = func() { commits++ }
			config := fixture.config()
			config.Git = policyGit
			supervisor, err := newCandidateSupervisor(config)
			if err != nil {
				t.Fatal(err)
			}
			updated, judgment, err := supervisor.ValidateTask(context.Background(), fixture.task.Handle)
			if err != nil {
				t.Fatalf("ValidateTask() error = %v", err)
			}
			wantDiffCalls := 1
			if test.wantState == domain.TaskValidating {
				wantDiffCalls = 2
				if _, replayJudgment, replayErr := supervisor.ValidateTask(context.Background(), fixture.task.Handle); replayErr != nil || replayJudgment != judgment || commits != 1 {
					t.Fatalf("unchanged path evidence replay = %#v, %v, commits %d", replayJudgment, replayErr, commits)
				}
			}
			if judgment.Reason != test.wantReason || updated.State != test.wantState ||
				policyGit.calls != wantDiffCalls || fixture.runner.calls != 0 || fixture.artifact.calls != 0 ||
				fixture.pullRequests.calls != 0 || len(fixture.store.publicationKinds) != 0 {
				t.Fatalf("path evidence result = task %q judgment %#v effects diff=%d validation=%d artifact=%d forge=%d publications=%d",
					updated.State, judgment, policyGit.calls, fixture.runner.calls, fixture.artifact.calls,
					fixture.pullRequests.calls, len(fixture.store.publicationKinds))
			}
		})
	}
}
