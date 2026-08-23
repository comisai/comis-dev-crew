package service

import (
	"context"
	"testing"

	"github.com/comisai/comis-dev-crew/internal/application"
	"github.com/comisai/comis-dev-crew/internal/domain"
)

type candidatePathPolicyGit struct {
	*candidateSupervisorGit
	view  application.TaskDiffView
	calls int
}

func (git *candidatePathPolicyGit) InspectTaskDiff(
	_ context.Context,
	_ application.TaskDiffRequest,
) (application.TaskDiffView, error) {
	git.calls++
	return git.view, nil
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
