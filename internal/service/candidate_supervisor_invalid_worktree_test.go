package service

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/comisai/comis-dev-crew/internal/domain"
	devgit "github.com/comisai/comis-dev-crew/internal/git"
)

func TestCandidateSupervisorPersistsStructuralWorktreeFailureOnce(t *testing.T) {
	fixture := newCandidateSupervisorFixture(t, domain.ShapeShip)
	structural := fmt.Errorf("fixture worktree disappeared: %w", devgit.ErrCandidateWorktreeUnverified)
	fixture.git.errors = []error{structural, structural}
	commits := 0
	fixture.store.onCommit = func() { commits++ }
	supervisor, err := newCandidateSupervisor(fixture.config())
	if err != nil {
		t.Fatal(err)
	}
	for attempt := 0; attempt < 2; attempt++ {
		_, judgment, validateErr := supervisor.ValidateTask(context.Background(), fixture.task.Handle)
		if validateErr != nil || judgment.Outcome != domain.CandidateUnknown ||
			judgment.Reason != domain.CandidateWorktreeUnverified {
			t.Fatalf("ValidateTask(structural attempt %d) = %#v, %v", attempt, judgment, validateErr)
		}
	}
	if commits != 1 || fixture.runner.calls != 0 || fixture.pullRequests.calls != 0 {
		t.Fatalf("structural worktree effects: commits=%d validation=%d forge=%d", commits, fixture.runner.calls, fixture.pullRequests.calls)
	}
	bundle := fixture.store.evidence.Bundle()
	if bundle.HeadRevision != fixture.task.BaseRevision || bundle.WorktreeCleanliness != domain.WorktreeUnknown {
		t.Fatalf("structural worktree evidence = %#v", bundle)
	}
}

func TestCandidateSupervisorKeepsInfrastructureInspectionFailureFatal(t *testing.T) {
	fixture := newCandidateSupervisorFixture(t, domain.ShapeShip)
	fixture.git.errors = []error{errors.New("Git process unavailable")}
	supervisor, err := newCandidateSupervisor(fixture.config())
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := supervisor.ValidateTask(context.Background(), fixture.task.Handle); err == nil {
		t.Fatal("ValidateTask(infrastructure failure) error = nil")
	}
	if fixture.store.evidence != nil || fixture.runner.calls != 0 {
		t.Fatal("infrastructure failure was converted into task evidence")
	}
}
