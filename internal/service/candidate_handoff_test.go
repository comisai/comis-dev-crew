package service

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/comisai/comis-dev-crew/internal/application"
	"github.com/comisai/comis-dev-crew/internal/domain"
	devgit "github.com/comisai/comis-dev-crew/internal/git"
)

func (git *candidateSupervisorGit) PromoteReconciliationCandidate(
	_ context.Context,
	request application.ReconciliationWorkspaceRequest,
) (application.WorkspaceSnapshot, error) {
	git.promotions++
	git.promotionRequest = request
	if git.onPromote != nil {
		git.onPromote()
	}
	return git.promotionSnapshot, git.promotionErr
}

func TestCandidateSupervisorPromotesOperationBoundCandidateBeforeValidation(t *testing.T) {
	fixture := newCandidateSupervisorFixture(t, domain.ShapeShip)
	supervisor, err := newCandidateSupervisor(fixture.config())
	if err != nil {
		t.Fatalf("newCandidateSupervisor() error = %v", err)
	}
	if _, _, err := supervisor.ValidateTask(context.Background(), fixture.task.Handle); err != nil {
		t.Fatalf("ValidateTask() error = %v", err)
	}
	want := application.ReconciliationWorkspaceRequest{
		PreparationOperationID: fixture.store.preparationOperationID,
		TaskHandle:             fixture.task.Handle,
		RepositoryID:           fixture.task.RepositoryID,
		WorktreePath:           fixture.preparation.RequestedWorkspaceRoot,
		BaseRevision:           fixture.task.BaseRevision,
	}
	if fixture.git.promotions != 1 || !reflect.DeepEqual(fixture.git.promotionRequest, want) {
		t.Fatalf("candidate promotions = %d/%#v, want one %#v", fixture.git.promotions, fixture.git.promotionRequest, want)
	}
}

func TestCandidateSupervisorRefusesCandidateWhenHandoffAuthorityDiffers(t *testing.T) {
	fixture := newCandidateSupervisorFixture(t, domain.ShapeShip)
	fixture.git.promotionSnapshot.HeadRevision = strings.Repeat("c", 40)
	supervisor, err := newCandidateSupervisor(fixture.config())
	if err != nil {
		t.Fatalf("newCandidateSupervisor() error = %v", err)
	}
	_, judgment, err := supervisor.ValidateTask(context.Background(), fixture.task.Handle)
	if err != nil || judgment.Outcome != domain.CandidateUnknown ||
		judgment.Reason != domain.CandidateWorktreeUnverified {
		t.Fatalf("ValidateTask(mismatched handoff) = %#v, %v", judgment, err)
	}
	if fixture.runner.calls != 0 || fixture.pullRequests.calls != 0 || fixture.store.evidence == nil ||
		fixture.store.evidence.Bundle().UnverifiedReason != domain.CandidateReconciliationMismatch {
		t.Fatalf("mismatched handoff effects: validation=%d forge=%d evidence=%v",
			fixture.runner.calls, fixture.pullRequests.calls, fixture.store.evidence)
	}
}

func TestCandidateSupervisorKeepsHandoffInfrastructureFailureFatalForCleanCandidate(t *testing.T) {
	fixture := newCandidateSupervisorFixture(t, domain.ShapeShip)
	fixture.git.promotionErr = errors.New("private candidate handoff unavailable")
	supervisor, err := newCandidateSupervisor(fixture.config())
	if err != nil {
		t.Fatalf("newCandidateSupervisor() error = %v", err)
	}
	if _, _, err := supervisor.ValidateTask(context.Background(), fixture.task.Handle); err == nil {
		t.Fatal("ValidateTask(handoff failure) error = nil")
	}
	if fixture.runner.calls != 0 || fixture.pullRequests.calls != 0 || fixture.store.evidence != nil {
		t.Fatal("handoff infrastructure failure was converted into candidate evidence")
	}
}

func TestCandidateSupervisorRejectsUnavailableOrInvalidHandoffAuthority(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(*candidateSupervisorFixture)
	}{
		{name: "durable authority read fails", mutate: func(fixture *candidateSupervisorFixture) {
			fixture.store.handoffAuthorityErr = errors.New("durable authority unavailable")
		}},
		{name: "preparation operation is invalid", mutate: func(fixture *candidateSupervisorFixture) {
			fixture.store.preparationOperationID = "invalid operation"
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
				t.Fatal("ValidateTask(invalid handoff authority) error = nil")
			}
			if fixture.git.promotions != 0 || fixture.runner.calls != 0 || fixture.store.evidence != nil {
				t.Fatalf("invalid authority effects: promotions=%d validation=%d evidence=%v",
					fixture.git.promotions, fixture.runner.calls, fixture.store.evidence)
			}
		})
	}
}

func TestCandidateSupervisorPersistsDirtyCandidateWhenPrivateHandoffIsUnavailable(t *testing.T) {
	fixture := newCandidateSupervisorFixture(t, domain.ShapeShip)
	fixture.git.promotionErr = errors.New("private candidate handoff unavailable")
	dirty := fixture.snapshot
	dirty.Cleanliness = devgit.CandidateDirty
	fixture.git.snapshots = []devgit.CandidateSnapshot{dirty}
	supervisor, err := newCandidateSupervisor(fixture.config())
	if err != nil {
		t.Fatalf("newCandidateSupervisor() error = %v", err)
	}
	_, judgment, err := supervisor.ValidateTask(context.Background(), fixture.task.Handle)
	if err != nil || judgment.Outcome != domain.CandidateUnknown || fixture.store.evidence == nil {
		t.Fatalf("ValidateTask(dirty handoff) = %#v, evidence=%v, error=%v", judgment, fixture.store.evidence, err)
	}
	if fixture.runner.calls != 0 || fixture.pullRequests.calls != 0 {
		t.Fatalf("dirty handoff ran validation=%d forge=%d", fixture.runner.calls, fixture.pullRequests.calls)
	}
}

func TestCandidateSupervisorJoinsCancellationDuringCandidateHandoff(t *testing.T) {
	fixture := newCandidateSupervisorFixture(t, domain.ShapeShip)
	ctx, cancel := context.WithCancel(context.Background())
	fixture.git.onPromote = cancel
	fixture.git.promotionErr = context.Canceled
	supervisor, err := newCandidateSupervisor(fixture.config())
	if err != nil {
		t.Fatalf("newCandidateSupervisor() error = %v", err)
	}
	if err := supervisor.Run(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("Run(cancelled handoff) error = %v, want context.Canceled", err)
	}
	if fixture.runner.calls != 0 || fixture.store.evidence != nil {
		t.Fatal("cancelled handoff ran validation or committed evidence")
	}
}
