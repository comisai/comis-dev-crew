package service

import (
	"context"
	"errors"
	"testing"

	"github.com/comisai/comis-dev-crew/internal/domain"
	devgit "github.com/comisai/comis-dev-crew/internal/git"
	"github.com/comisai/comis-dev-crew/internal/validation"
)

func TestCandidateSupervisor_PreservesDirtyBaseCandidateWithoutForgeMutation(t *testing.T) {
	fixture := newCandidateSupervisorFixture(t, domain.ShapeShip)
	fixture.snapshot.HeadRevision = fixture.task.BaseRevision
	fixture.snapshot.Cleanliness = devgit.CandidateDirty
	fixture.git.snapshots = []devgit.CandidateSnapshot{fixture.snapshot, fixture.snapshot}
	fixture.runner.receipt.HeadRevision = fixture.task.BaseRevision
	fixture.pullRequests.err = errors.New("forge must not receive an invalid candidate")
	supervisor, err := newCandidateSupervisor(fixture.config())
	if err != nil {
		t.Fatalf("newCandidateSupervisor() error = %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	fixture.store.onCommit = cancel
	if err := supervisor.Run(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("Run() error = %v, want context.Canceled", err)
	}
	judgment := domain.JudgeCandidate(domain.CandidateJudgeInput{
		Task: fixture.task, Evidence: fixture.store.evidence,
		RequiredLocalChecks: []string{"unit"}, RequiredForgeChecks: []string{"ci/unit"}, Now: fixture.now,
	})
	if judgment.Outcome != domain.CandidateUnknown || judgment.Reason != domain.CandidateWorktreeUnverified ||
		fixture.store.task.State != domain.TaskValidating {
		t.Fatalf("Run() task = %#v, judgment = %#v", fixture.store.task, judgment)
	}
	if fixture.runner.calls != 0 || fixture.pullRequests.calls != 0 || fixture.store.evidence == nil {
		t.Fatalf("invalid candidate effects: validation=%d forge=%d evidence=%t",
			fixture.runner.calls, fixture.pullRequests.calls, fixture.store.evidence != nil)
	}
	if bundle := fixture.store.evidence.Bundle(); bundle.WorktreeCleanliness != domain.WorktreeDirty ||
		bundle.HeadRevision != fixture.task.BaseRevision || bundle.ForgeEvidence != nil {
		t.Fatalf("invalid candidate evidence = %#v", bundle)
	}
}

func TestCandidateSupervisor_DoesNotRepeatValidationForUnchangedDirtyCandidate(t *testing.T) {
	fixture := newCandidateSupervisorFixture(t, domain.ShapeShip)
	fixture.snapshot.HeadRevision = fixture.task.BaseRevision
	fixture.snapshot.Cleanliness = devgit.CandidateDirty
	fixture.git.snapshots = []devgit.CandidateSnapshot{
		fixture.snapshot, fixture.snapshot, fixture.snapshot, fixture.snapshot,
	}
	fixture.runner.receipt.HeadRevision = fixture.task.BaseRevision
	supervisor, err := newCandidateSupervisor(fixture.config())
	if err != nil {
		t.Fatalf("newCandidateSupervisor() error = %v", err)
	}
	for attempt := range 2 {
		_, judgment, err := supervisor.ValidateTask(context.Background(), fixture.task.Handle)
		if err != nil {
			t.Fatalf("ValidateTask(attempt %d) error = %v", attempt+1, err)
		}
		if judgment.Outcome != domain.CandidateUnknown || judgment.Reason != domain.CandidateWorktreeUnverified {
			t.Fatalf("ValidateTask(attempt %d) judgment = %#v", attempt+1, judgment)
		}
	}
	if fixture.runner.calls != 0 || fixture.pullRequests.calls != 0 {
		t.Fatalf("unchanged dirty candidate effects: validation=%d forge=%d",
			fixture.runner.calls, fixture.pullRequests.calls)
	}
}

func TestCandidateSupervisor_DoesNotRepeatValidationForUnchangedBaseCandidate(t *testing.T) {
	for _, shape := range []domain.TaskShape{domain.ShapeShip, domain.ShapeScout} {
		t.Run(string(shape), func(t *testing.T) {
			fixture := newCandidateSupervisorFixture(t, shape)
			fixture.snapshot.HeadRevision = fixture.task.BaseRevision
			fixture.git.snapshots = []devgit.CandidateSnapshot{
				fixture.snapshot, fixture.snapshot, fixture.snapshot, fixture.snapshot,
			}
			fixture.runner.receipt.HeadRevision = fixture.task.BaseRevision
			supervisor, err := newCandidateSupervisor(fixture.config())
			if err != nil {
				t.Fatalf("newCandidateSupervisor() error = %v", err)
			}
			for attempt := range 2 {
				_, judgment, err := supervisor.ValidateTask(context.Background(), fixture.task.Handle)
				if err != nil {
					t.Fatalf("ValidateTask(attempt %d) error = %v", attempt+1, err)
				}
				if judgment.Outcome != domain.CandidateUnknown || judgment.Reason != domain.CandidateWorktreeUnverified {
					t.Fatalf("ValidateTask(attempt %d) judgment = %#v", attempt+1, judgment)
				}
			}
			if fixture.runner.calls != 0 || fixture.pullRequests.calls != 0 || fixture.artifact.calls != 0 {
				t.Fatalf("unchanged base candidate effects: validation=%d forge=%d artifact=%d",
					fixture.runner.calls, fixture.pullRequests.calls, fixture.artifact.calls)
			}
		})
	}
}

func TestCandidateSupervisor_CommitsDirtyPostureBeforeUnavailableValidation(t *testing.T) {
	fixture := newCandidateSupervisorFixture(t, domain.ShapeShip)
	fixture.snapshot.Cleanliness = devgit.CandidateDirty
	fixture.git.snapshots = []devgit.CandidateSnapshot{fixture.snapshot}
	fixture.runner.receipt = validation.Receipt{}
	fixture.runner.err = errors.New("validation unavailable")
	supervisor, err := newCandidateSupervisor(fixture.config())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	fixture.store.onCommit = cancel
	if err := supervisor.Run(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("Run() error = %v, want context.Canceled after durable unverified evidence", err)
	}
	if fixture.runner.calls != 0 || fixture.store.evidence == nil {
		t.Fatalf("dirty candidate effects: validation=%d evidence=%t", fixture.runner.calls, fixture.store.evidence != nil)
	}
	judgment := domain.JudgeCandidate(domain.CandidateJudgeInput{
		Task: fixture.task, Evidence: fixture.store.evidence,
		RequiredLocalChecks: fixture.store.requiredLocalChecks,
		RequiredForgeChecks: fixture.store.requiredForgeChecks, Now: fixture.store.judgedAt,
	})
	if judgment.Outcome != domain.CandidateUnknown || judgment.Reason != domain.CandidateWorktreeUnverified {
		t.Fatalf("dirty candidate judgment = %#v", judgment)
	}
}
