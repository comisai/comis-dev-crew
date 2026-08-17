package service

import (
	"context"
	"errors"
	"testing"

	"github.com/comisai/comis-dev-crew/internal/application"
	"github.com/comisai/comis-dev-crew/internal/domain"
	devgit "github.com/comisai/comis-dev-crew/internal/git"
	"github.com/comisai/comis-dev-crew/internal/validation"
)

func TestCandidateSupervisorRejectsUnavailableDurableInputs(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*candidateSupervisorFixture, *candidateBoundaryStore)
	}{
		{name: "task read", mutate: func(_ *candidateSupervisorFixture, store *candidateBoundaryStore) {
			store.getTaskErr = errors.New("unavailable")
		}},
		{name: "task state", mutate: func(fixture *candidateSupervisorFixture, _ *candidateBoundaryStore) {
			fixture.store.task.State = domain.TaskPrepared
		}},
		{name: "preparation read", mutate: func(_ *candidateSupervisorFixture, store *candidateBoundaryStore) {
			store.preparationErr = errors.New("unavailable")
		}},
		{name: "reconciliation read", mutate: func(_ *candidateSupervisorFixture, store *candidateBoundaryStore) {
			store.reconciliationErr = errors.New("unavailable")
		}},
		{name: "profile resolution", mutate: func(fixture *candidateSupervisorFixture, _ *candidateBoundaryStore) {
			fixture.store.task.ValidationProfile = "unknown-profile"
		}},
		{name: "decision inventory", mutate: func(_ *candidateSupervisorFixture, store *candidateBoundaryStore) {
			store.reportsErr = errors.New("unavailable")
		}},
		{name: "prior unverified evidence", mutate: func(fixture *candidateSupervisorFixture, store *candidateBoundaryStore) {
			fixture.git.errors = []error{devgit.ErrCandidateWorktreeUnverified}
			store.latestErr = errors.New("unavailable")
		}},
		{name: "prior clean evidence", mutate: func(_ *candidateSupervisorFixture, store *candidateBoundaryStore) {
			store.latestErr = errors.New("unavailable")
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newCandidateSupervisorFixture(t, domain.ShapeShip)
			store := &candidateBoundaryStore{candidateSupervisorStore: fixture.store}
			test.mutate(fixture, store)
			config := fixture.config()
			config.Store = store
			supervisor, err := newCandidateSupervisor(config)
			if err != nil {
				t.Fatal(err)
			}
			if _, _, err := supervisor.ValidateTask(context.Background(), fixture.store.task.Handle); err == nil {
				t.Fatal("ValidateTask() error = nil")
			}
		})
	}
}

func TestCandidateEvidenceHelpersRejectIncompleteDeliveryMaterial(t *testing.T) {
	fixture := newCandidateSupervisorFixture(t, domain.ShapeShip)
	supervisor, err := newCandidateSupervisor(fixture.config())
	if err != nil {
		t.Fatal(err)
	}
	profile, err := fixture.catalog.ResolveProfile(fixture.task.ValidationProfile)
	if err != nil {
		t.Fatal(err)
	}
	profile.LocalChecks[0].Required = false
	if _, _, err := supervisor.runLocalChecks(context.Background(), fixture.task, profile, fixture.snapshot); err == nil {
		t.Fatal("runLocalChecks accepted a profile without a required check")
	}
	if _, _, err := supervisor.attachDeliveryEvidence(
		context.Background(), fixture.task, validation.Profile{}, fixture.snapshot, &domain.DeliveryEvidenceBundle{},
	); err == nil {
		t.Fatal("attachDeliveryEvidence accepted a ship profile without a forge check")
	}
	scout := fixture.task
	scout.Shape = domain.ShapeScout
	if _, _, err := supervisor.attachDeliveryEvidence(
		context.Background(), scout, validation.Profile{}, fixture.snapshot, &domain.DeliveryEvidenceBundle{},
	); err == nil {
		t.Fatal("attachDeliveryEvidence accepted a scout profile without one artifact rule")
	}
	invalid := fixture.task
	invalid.Shape = domain.TaskShape("invalid")
	if _, _, err := supervisor.attachDeliveryEvidence(
		context.Background(), invalid, profile, fixture.snapshot, &domain.DeliveryEvidenceBundle{},
	); err == nil {
		t.Fatal("attachDeliveryEvidence accepted an invalid task shape")
	}
	if _, err := candidateEvidencePublications(fixture.task, nil, candidateDeliveryMaterial{}); err == nil {
		t.Fatal("candidateEvidencePublications accepted missing sealed evidence")
	}

	accepted, _, err := supervisor.ValidateTask(context.Background(), fixture.task.Handle)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := candidateEvidencePublications(accepted, fixture.store.evidence, candidateDeliveryMaterial{}); err == nil {
		t.Fatal("candidateEvidencePublications accepted missing delivery material")
	}

	scoutFixture := newCandidateSupervisorFixture(t, domain.ShapeScout)
	scoutSupervisor, err := newCandidateSupervisor(scoutFixture.config())
	if err != nil {
		t.Fatal(err)
	}
	acceptedScout, _, err := scoutSupervisor.ValidateTask(context.Background(), scoutFixture.task.Handle)
	if err != nil {
		t.Fatal(err)
	}
	changedArtifact := scoutFixture.artifact.artifact
	changedArtifact.Body = []byte("changed body")
	if _, err := candidateEvidencePublications(acceptedScout, scoutFixture.store.evidence, candidateDeliveryMaterial{
		artifact: &changedArtifact, fileName: "report.md",
	}); err == nil {
		t.Fatal("candidateEvidencePublications accepted changed artifact bytes")
	}
}

func TestCandidateSupervisorCoversRetryAndPostCheckBoundaries(t *testing.T) {
	fixture := newCandidateSupervisorFixture(t, domain.ShapeShip)
	ctx, cancel := context.WithCancel(context.Background())
	fixture.store.list = func(context.Context) ([]domain.Task, error) {
		cancel()
		skipped := fixture.task
		skipped.State = domain.TaskPrepared
		return []domain.Task{skipped}, nil
	}
	supervisor, err := newCandidateSupervisor(fixture.config())
	if err != nil {
		t.Fatal(err)
	}
	if err := supervisor.Run(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("Run() error = %v", err)
	}

	tests := []struct {
		name      string
		mutate    func(*candidateSupervisorFixture)
		wantError bool
	}{
		{name: "post-check Git failure", mutate: func(fixture *candidateSupervisorFixture) {
			fixture.git.errors = []error{nil, errors.New("unavailable")}
		}, wantError: true},
		{name: "post-check dirty worktree", mutate: func(fixture *candidateSupervisorFixture) {
			dirty := fixture.snapshot
			dirty.Cleanliness = devgit.CandidateDirty
			fixture.git.snapshots = []devgit.CandidateSnapshot{fixture.snapshot, dirty}
		}},
		{name: "publication identity unavailable", mutate: func(fixture *candidateSupervisorFixture) {
			fixture.task.ManagedRunID = ""
			fixture.store.task.ManagedRunID = ""
		}, wantError: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newCandidateSupervisorFixture(t, domain.ShapeShip)
			test.mutate(fixture)
			supervisor, err := newCandidateSupervisor(fixture.config())
			if err != nil {
				t.Fatal(err)
			}
			_, _, err = supervisor.ValidateTask(context.Background(), fixture.task.Handle)
			if test.wantError && err == nil {
				t.Fatal("ValidateTask() error = nil")
			}
			if !test.wantError && err != nil {
				t.Fatalf("ValidateTask() error = %v", err)
			}
		})
	}
}

type candidateBoundaryStore struct {
	*candidateSupervisorStore
	getTaskErr        error
	preparationErr    error
	reconciliationErr error
	reportsErr        error
	latestErr         error
}

func (store *candidateBoundaryStore) GetTask(context.Context, string) (domain.Task, error) {
	return store.task, store.getTaskErr
}

func (store *candidateBoundaryStore) GetManagedRunPreparation(
	context.Context,
	string,
) (application.ManagedRunPreparation, error) {
	return store.preparation, store.preparationErr
}

func (store *candidateBoundaryStore) ReadReconciledCandidateSnapshot(
	context.Context,
	string,
) (application.WorkspaceSnapshot, bool, error) {
	return store.reconciledSnapshot, store.reconciled, store.reconciliationErr
}

func (store *candidateBoundaryStore) ListAcceptedReports(
	context.Context,
	string,
) ([]domain.AcceptedReport, error) {
	return append([]domain.AcceptedReport(nil), store.reports...), store.reportsErr
}

func (store *candidateBoundaryStore) LatestCandidateEvidence(
	context.Context,
	string,
) (*domain.SealedDeliveryEvidence, domain.CandidateJudgment, error) {
	if store.latestErr != nil {
		return nil, domain.CandidateJudgment{}, store.latestErr
	}
	return store.candidateSupervisorStore.LatestCandidateEvidence(context.Background(), store.task.Handle)
}

var _ candidateEvidenceStore = (*candidateBoundaryStore)(nil)
