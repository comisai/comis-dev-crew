package application

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/comisai/comis-dev-crew/internal/domain"
)

type nonAtomicQueryRepository struct {
	inner *queryRepository
}

func (repository *nonAtomicQueryRepository) CreateTask(ctx context.Context, task domain.Task) error {
	return repository.inner.CreateTask(ctx, task)
}

func (repository *nonAtomicQueryRepository) RecordOperation(ctx context.Context, operation domain.OperationRecord) error {
	return repository.inner.RecordOperation(ctx, operation)
}

func (repository *nonAtomicQueryRepository) ListTasks(ctx context.Context) ([]domain.Task, error) {
	return repository.inner.ListTasks(ctx)
}

func (repository *nonAtomicQueryRepository) TaskSnapshot(ctx context.Context) ([]domain.Task, int64, error) {
	return repository.inner.TaskSnapshot(ctx)
}

func (repository *nonAtomicQueryRepository) GetTask(ctx context.Context, handle string) (domain.Task, error) {
	return repository.inner.GetTask(ctx, handle)
}

func (repository *nonAtomicQueryRepository) GetManagedRunPreparation(ctx context.Context, handle string) (ManagedRunPreparation, error) {
	return repository.inner.GetManagedRunPreparation(ctx, handle)
}

func (repository *nonAtomicQueryRepository) GetOperation(ctx context.Context, operationID string) (domain.OperationRecord, error) {
	return repository.inner.GetOperation(ctx, operationID)
}

func (repository *nonAtomicQueryRepository) CurrentStateVersion(ctx context.Context) (int64, error) {
	return repository.inner.CurrentStateVersion(ctx)
}

func TestQueriesFallbackRepositoryReadsOneDurableTaskSnapshot(t *testing.T) {
	now := time.Date(2026, time.August, 14, 10, 0, 0, 0, time.UTC)
	task := queryTask("task-fallback-query", domain.TaskAwaitingDecision, 7)
	inner := &queryRepository{tasks: []domain.Task{task}, stateVersion: task.StateVersion}
	queries, err := NewQueries(QueryConfig{
		Repository: &nonAtomicQueryRepository{inner: inner}, Clock: func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	fleet, err := queries.Fleet(context.Background())
	if err != nil {
		t.Fatalf("Fleet() error = %v", err)
	}
	if !inner.snapshotCalled || len(fleet.Tasks) != 1 || fleet.Tasks[0].TaskHandle != task.Handle ||
		fleet.Tasks[0].BlockedBy != "open_decision" || fleet.Tasks[0].Head != "unknown" {
		t.Fatalf("fallback fleet = %#v, snapshotCalled=%t", fleet, inner.snapshotCalled)
	}
	show, err := queries.ShowTask(context.Background(), task.Handle)
	if err != nil {
		t.Fatalf("ShowTask() error = %v", err)
	}
	if show.Summary.TaskHandle != task.Handle || show.Evidence.Candidate.Status != "" {
		t.Fatalf("fallback task detail = %#v", show)
	}
}

func TestQueriesFallbackRepositoryTranslatesSnapshotAndLookupFailures(t *testing.T) {
	privateCause := errors.New("private fallback repository failure")
	inner := &queryRepository{readErr: privateCause}
	queries, err := NewQueries(QueryConfig{Repository: &nonAtomicQueryRepository{inner: inner}, Clock: time.Now})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := queries.Fleet(context.Background()); failureCode(err) != domain.ErrorInternal {
		t.Fatalf("Fleet(failed fallback) error = %v", err)
	}
	if _, err := queries.ShowTask(context.Background(), "task-fallback-missing"); failureCode(err) != domain.ErrorInternal {
		t.Fatalf("ShowTask(failed fallback) error = %v", err)
	}
}

func TestDependencyFailureExposesOnlyApplicationOwnedStage(t *testing.T) {
	privateCause := errors.New("private dependency response")
	failure := &dependencyFailure{message: "activate managed run: host dependency failed", cause: privateCause}
	if failure.SafeDependencyMessage() != "activate managed run: host dependency failed" || !errors.Is(failure, privateCause) {
		t.Fatalf("dependency failure = %#v", failure)
	}
}
