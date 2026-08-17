package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/comisai/comis-dev-crew/internal/application"
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
	if bundle.HeadRevision != "" || bundle.WorktreeCleanliness != domain.WorktreeUnknown {
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

func TestCandidateSupervisorPersistsReconciledDirtySnapshotBeforeComparison(t *testing.T) {
	fixture := newCandidateSupervisorFixture(t, domain.ShapeShip)
	fixture.store.reconciled = true
	fixture.store.reconciledSnapshot = application.WorkspaceSnapshot{
		TaskHandle: fixture.task.Handle, RepositoryID: fixture.snapshot.RepositoryID,
		WorktreePath: fixture.snapshot.WorktreePath, Branch: fixture.snapshot.Branch,
		HeadRevision: fixture.snapshot.HeadRevision, Cleanliness: application.WorkspaceClean,
	}
	dirty := fixture.snapshot
	dirty.Cleanliness = devgit.CandidateDirty
	fixture.git.snapshots = []devgit.CandidateSnapshot{dirty}
	supervisor, err := newCandidateSupervisor(fixture.config())
	if err != nil {
		t.Fatal(err)
	}
	_, judgment, err := supervisor.ValidateTask(context.Background(), fixture.task.Handle)
	if err != nil || judgment.Outcome != domain.CandidateUnknown ||
		judgment.Reason != domain.CandidateWorktreeUnverified {
		t.Fatalf("ValidateTask(reconciled dirty) = %#v, %v", judgment, err)
	}
	if fixture.store.evidence == nil || fixture.runner.calls != 0 {
		t.Fatalf("reconciled dirty evidence=%v validation=%d", fixture.store.evidence, fixture.runner.calls)
	}
}

func TestCandidateSupervisorPersistsStructuralFailureAfterValidation(t *testing.T) {
	fixture := newCandidateSupervisorFixture(t, domain.ShapeShip)
	fixture.git.errors = []error{nil, fmt.Errorf("fixture worktree disappeared: %w", devgit.ErrCandidateWorktreeUnverified)}
	supervisor, err := newCandidateSupervisor(fixture.config())
	if err != nil {
		t.Fatal(err)
	}
	_, judgment, err := supervisor.ValidateTask(context.Background(), fixture.task.Handle)
	if err != nil || judgment.Outcome != domain.CandidateUnknown ||
		judgment.Reason != domain.CandidateWorktreeUnverified {
		t.Fatalf("ValidateTask(post-validation structural failure) = %#v, %v", judgment, err)
	}
	if fixture.runner.calls != 1 || fixture.store.evidence == nil ||
		fixture.store.evidence.Bundle().WorktreeCleanliness != domain.WorktreeUnknown {
		t.Fatalf("post-validation structural evidence=%v validation=%d", fixture.store.evidence, fixture.runner.calls)
	}
}

func TestCandidateSupervisorPersistsHeadDriftAfterValidationOnce(t *testing.T) {
	fixture := newCandidateSupervisorFixture(t, domain.ShapeShip)
	changed := fixture.snapshot
	changed.HeadRevision = strings.Repeat("c", 40)
	fixture.git.snapshots = []devgit.CandidateSnapshot{fixture.snapshot, changed, changed}
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
			t.Fatalf("ValidateTask(head drift attempt %d) = %#v, %v", attempt, judgment, validateErr)
		}
	}
	if commits != 1 || fixture.runner.calls != 1 || fixture.pullRequests.calls != 0 ||
		fixture.store.evidence == nil || fixture.store.evidence.Bundle().HeadRevision != changed.HeadRevision ||
		fixture.store.evidence.Bundle().UnverifiedReason != domain.CandidateValidationDrift {
		t.Fatalf("head drift effects: commits=%d validation=%d forge=%d evidence=%v",
			commits, fixture.runner.calls, fixture.pullRequests.calls, fixture.store.evidence)
	}
}
