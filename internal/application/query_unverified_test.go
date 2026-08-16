package application

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/comisai/comis-dev-crew/internal/domain"
)

func TestQueriesExplainUnknownWorktreeWithoutInventingGitFacts(t *testing.T) {
	task := queryTask("task-candidate-unknown-worktree", domain.TaskValidating, 8)
	bundle := queryCandidateEvidence(t, task, time.Now().UTC()).Bundle()
	bundle.HeadRevision = task.BaseRevision
	bundle.WorktreeCleanliness = domain.WorktreeUnknown
	for index := range bundle.ValidationReceipts {
		bundle.ValidationReceipts[index].HeadRevision = task.BaseRevision
	}
	if bundle.ForgeEvidence != nil {
		bundle.ForgeEvidence.HeadRevision = task.BaseRevision
	}
	sealed, err := domain.SealDeliveryEvidence(bundle)
	if err != nil {
		t.Fatal(err)
	}
	repository := &queryRepository{
		tasks: []domain.Task{task}, candidateEvidence: sealed,
		candidateJudgment: domain.CandidateJudgment{
			Outcome: domain.CandidateUnknown, Reason: domain.CandidateWorktreeUnverified,
		},
	}
	queries, err := NewQueries(QueryConfig{Repository: repository, Clock: time.Now})
	if err != nil {
		t.Fatal(err)
	}
	explanation, err := queries.ExplainTask(context.Background(), task.Handle)
	if err != nil {
		t.Fatal(err)
	}
	cause := strings.ToLower(explanation.LikelyRootCause)
	if !strings.Contains(cause, "unknown") || strings.Contains(cause, "not clean") ||
		strings.Contains(cause, "equals the pinned base") {
		t.Fatalf("ExplainTask(unknown worktree) root cause = %q", explanation.LikelyRootCause)
	}
}
