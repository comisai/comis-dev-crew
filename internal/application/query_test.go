package application

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/comisai/comis-dev-crew/internal/domain"
)

func TestQueries_ProduceStablePartialFleetAndDiagnosticSnapshots(t *testing.T) {
	now := time.Date(2026, time.August, 8, 21, 0, 0, 123000000, time.UTC)
	repository := &queryRepository{
		tasks: []domain.Task{
			queryTask("task-0002", domain.TaskWorking, 2),
			queryTask("task-0001", domain.TaskPrepared, 1),
		},
		stateVersion: 2,
	}
	queries, err := NewQueries(repository, func() time.Time { return now })
	if err != nil {
		t.Fatalf("NewQueries() error = %v", err)
	}

	diagnostic, err := queries.Diagnose(context.Background())
	if err != nil {
		t.Fatalf("Diagnose() error = %v", err)
	}
	if diagnostic.SchemaVersion != 1 || diagnostic.CapturedAtMs != now.UnixMilli() {
		t.Fatalf("Diagnose() identity = %#v, want schema 1 and injected capture time", diagnostic)
	}
	if diagnostic.Completeness != CompletenessPartial || diagnostic.ServiceHealth != HealthHealthy || diagnostic.ComisHealth != HealthUnavailable {
		t.Fatalf("Diagnose() health = %#v, want explicit partial host-integration posture", diagnostic)
	}
	if len(diagnostic.Checks) != 3 || diagnostic.Checks[2].Status != CheckUnknown {
		t.Fatalf("Diagnose() checks = %#v, want service/store pass and host unknown", diagnostic.Checks)
	}

	fleet, err := queries.Fleet(context.Background())
	if err != nil {
		t.Fatalf("Fleet() error = %v", err)
	}
	if fleet.StateVersion != 2 || fleet.Completeness != CompletenessPartial || len(fleet.Tasks) != 2 {
		t.Fatalf("Fleet() = %#v, want versioned partial two-task snapshot", fleet)
	}
	if !repository.snapshotCalled {
		t.Fatal("Fleet() did not use the atomic task snapshot port")
	}
	if fleet.Tasks[0].TaskHandle != "task-0002" || fleet.Tasks[0].StateSource != StateSourceStore || fleet.Tasks[0].Freshness != FreshnessCurrent {
		t.Fatalf("Fleet() first task = %#v, want stable source and freshness", fleet.Tasks[0])
	}
	if fleet.Tasks[0].ElapsedMs != now.Sub(repository.tasks[0].CreatedAt).Milliseconds() {
		t.Fatalf("Fleet() elapsed = %d, want injected-clock duration", fleet.Tasks[0].ElapsedMs)
	}
}

func TestQueries_ListShowExplainAndOperationShareCanonicalProjections(t *testing.T) {
	now := time.Date(2026, time.August, 8, 21, 0, 0, 0, time.UTC)
	task := queryTask("task-0001", domain.TaskBlocked, 4)
	operation := queryOperation("op-0001", 5)
	repository := &queryRepository{tasks: []domain.Task{task}, operation: operation, stateVersion: 5}
	queries, err := NewQueries(repository, func() time.Time { return now })
	if err != nil {
		t.Fatalf("NewQueries() error = %v", err)
	}

	list, err := queries.ListTasks(context.Background())
	if err != nil {
		t.Fatalf("ListTasks() error = %v", err)
	}
	show, err := queries.ShowTask(context.Background(), task.Handle)
	if err != nil {
		t.Fatalf("ShowTask() error = %v", err)
	}
	explanation, err := queries.ExplainTask(context.Background(), task.Handle)
	if err != nil {
		t.Fatalf("ExplainTask() error = %v", err)
	}
	if len(list.Tasks) != 1 || !reflect.DeepEqual(list.Tasks[0], show.Summary) || !reflect.DeepEqual(explanation.Summary, show.Summary) {
		t.Fatalf("query projections diverged: list=%#v show=%#v explain=%#v", list, show, explanation)
	}
	if explanation.ReasonCode != "task_blocked" || len(explanation.NextSafeActions) == 0 {
		t.Fatalf("ExplainTask() = %#v, want explicit reason and repair action", explanation)
	}
	if show.BaseRevision != task.BaseRevision || show.DeliveryMode != task.DeliveryMode || show.CreatedAtMs != task.CreatedAt.UnixMilli() {
		t.Fatalf("ShowTask() = %#v, want durable task detail", show)
	}

	operationView, err := queries.Operation(context.Background(), operation.ID)
	if err != nil {
		t.Fatalf("Operation() error = %v", err)
	}
	if operationView.OperationID != operation.ID || operationView.Status != operation.Status || operationView.SubjectDigest != operation.SubjectDigest {
		t.Fatalf("Operation() = %#v, want durable operation projection", operationView)
	}
}

func TestQueries_RejectInvalidRefsAndTranslateRepositoryFailuresSafely(t *testing.T) {
	privateCause := errors.New("private database path and row detail")
	tests := []struct {
		name     string
		invoke   func(*Queries) error
		repoErr  error
		wantCode domain.ErrorCode
		wantRead bool
	}{
		{
			name: "invalid task handle",
			invoke: func(queries *Queries) error {
				_, err := queries.ShowTask(context.Background(), "../escape")
				return err
			},
			wantCode: domain.ErrorInvalidArgument,
		},
		{
			name: "missing task",
			invoke: func(queries *Queries) error {
				_, err := queries.ExplainTask(context.Background(), "missing-task")
				return err
			},
			repoErr:  ErrNotFound,
			wantCode: domain.ErrorNotFound,
			wantRead: true,
		},
		{
			name: "invalid operation id",
			invoke: func(queries *Queries) error {
				_, err := queries.Operation(context.Background(), "bad id")
				return err
			},
			wantCode: domain.ErrorInvalidArgument,
		},
		{
			name: "private repository failure",
			invoke: func(queries *Queries) error {
				_, err := queries.ShowTask(context.Background(), "task-0001")
				return err
			},
			repoErr:  privateCause,
			wantCode: domain.ErrorInternal,
			wantRead: true,
		},
		{
			name: "cancelled read",
			invoke: func(queries *Queries) error {
				_, err := queries.ListTasks(context.Background())
				return err
			},
			repoErr:  context.Canceled,
			wantCode: domain.ErrorDeadlineExceeded,
			wantRead: true,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			repository := &queryRepository{readErr: test.repoErr}
			queries, err := NewQueries(repository, time.Now)
			if err != nil {
				t.Fatalf("NewQueries() error = %v", err)
			}
			err = test.invoke(queries)
			var failure *domain.Failure
			if !errors.As(err, &failure) || failure.Code != test.wantCode {
				t.Fatalf("query error = %v, want Failure code %q", err, test.wantCode)
			}
			if strings.Contains(err.Error(), privateCause.Error()) {
				t.Fatalf("query error leaked private cause: %q", err)
			}
			if repository.readCalled != test.wantRead {
				t.Fatalf("repository read called = %v, want %v", repository.readCalled, test.wantRead)
			}
		})
	}
}

func TestNewQueries_RejectsMissingDependencies(t *testing.T) {
	if _, err := NewQueries(nil, time.Now); err == nil {
		t.Fatal("NewQueries(nil repository) error = nil")
	}
	if _, err := NewQueries(&queryRepository{}, nil); err == nil {
		t.Fatal("NewQueries(nil clock) error = nil")
	}
}

func TestQueries_FailureBranchesAndClosedStateExplanations(t *testing.T) {
	privateCause := errors.New("private adapter detail")

	t.Run("diagnostic read failure", func(t *testing.T) {
		queries, err := NewQueries(&queryRepository{readErr: privateCause}, time.Now)
		if err != nil {
			t.Fatalf("NewQueries() error = %v", err)
		}
		if _, err := queries.Diagnose(context.Background()); failureCode(err) != domain.ErrorInternal {
			t.Fatalf("Diagnose() error = %v, want internal failure", err)
		}
	})

	t.Run("fleet list failure", func(t *testing.T) {
		queries, err := NewQueries(&queryRepository{readErr: privateCause}, time.Now)
		if err != nil {
			t.Fatalf("NewQueries() error = %v", err)
		}
		if _, err := queries.Fleet(context.Background()); failureCode(err) != domain.ErrorInternal {
			t.Fatalf("Fleet() error = %v, want internal failure", err)
		}
	})

	t.Run("state version failure", func(t *testing.T) {
		queries, err := NewQueries(&queryRepository{stateVersionErr: privateCause}, time.Now)
		if err != nil {
			t.Fatalf("NewQueries() error = %v", err)
		}
		if _, err := queries.ListTasks(context.Background()); failureCode(err) != domain.ErrorInternal {
			t.Fatalf("ListTasks() error = %v, want internal failure", err)
		}
	})

	t.Run("operation read failure", func(t *testing.T) {
		queries, err := NewQueries(&queryRepository{readErr: ErrNotFound}, time.Now)
		if err != nil {
			t.Fatalf("NewQueries() error = %v", err)
		}
		if _, err := queries.Operation(context.Background(), "op-0001"); failureCode(err) != domain.ErrorNotFound {
			t.Fatalf("Operation() error = %v, want not-found failure", err)
		}
	})

	t.Run("future task clamps elapsed duration", func(t *testing.T) {
		now := time.Date(2026, time.August, 8, 20, 0, 0, 0, time.UTC)
		task := queryTask("task-0001", domain.TaskPrepared, 1)
		task.CreatedAt = now.Add(time.Hour)
		queries, err := NewQueries(&queryRepository{tasks: []domain.Task{task}}, func() time.Time { return now })
		if err != nil {
			t.Fatalf("NewQueries() error = %v", err)
		}
		fleet, err := queries.Fleet(context.Background())
		if err != nil {
			t.Fatalf("Fleet() error = %v", err)
		}
		if fleet.Tasks[0].ElapsedMs != 0 {
			t.Fatalf("Fleet() elapsed = %d, want zero", fleet.Tasks[0].ElapsedMs)
		}
	})

	for _, state := range []domain.TaskState{
		domain.TaskPrepared,
		domain.TaskBlocked,
		domain.TaskUnknown,
		domain.TaskFailed,
		domain.TaskCancelled,
		domain.TaskCleanupHeld,
		domain.TaskDelivered,
		domain.TaskCleaned,
		domain.TaskWorking,
	} {
		reason, explanation, rootCause, actions := explainState(state)
		if reason == "" || explanation == "" || rootCause == "" || len(actions) == 0 {
			t.Fatalf("explainState(%q) returned incomplete output", state)
		}
	}

	if err := newSafeFailure(domain.ErrorCode("invalid"), false, "message", "hint", nil); err == nil {
		t.Fatal("newSafeFailure(invalid code) error = nil")
	}
}

func failureCode(err error) domain.ErrorCode {
	var failure *domain.Failure
	if !errors.As(err, &failure) {
		return ""
	}
	return failure.Code
}

type queryRepository struct {
	tasks           []domain.Task
	operation       domain.OperationRecord
	stateVersion    int64
	stateVersionErr error
	readErr         error
	readCalled      bool
	snapshotCalled  bool
}

func (repository *queryRepository) CreateTask(context.Context, domain.Task) error { return nil }

func (repository *queryRepository) RecordOperation(context.Context, domain.OperationRecord) error {
	return nil
}

func (repository *queryRepository) ListTasks(context.Context) ([]domain.Task, error) {
	repository.readCalled = true
	return repository.tasks, repository.readErr
}

func (repository *queryRepository) GetTask(_ context.Context, handle string) (domain.Task, error) {
	repository.readCalled = true
	if repository.readErr != nil {
		return domain.Task{}, repository.readErr
	}
	for _, task := range repository.tasks {
		if task.Handle == handle {
			return task, nil
		}
	}
	return domain.Task{}, ErrNotFound
}

func (repository *queryRepository) GetOperation(context.Context, string) (domain.OperationRecord, error) {
	repository.readCalled = true
	if repository.readErr != nil {
		return domain.OperationRecord{}, repository.readErr
	}
	return repository.operation, nil
}

func (repository *queryRepository) CurrentStateVersion(context.Context) (int64, error) {
	repository.readCalled = true
	if repository.stateVersionErr != nil {
		return 0, repository.stateVersionErr
	}
	return repository.stateVersion, repository.readErr
}

func (repository *queryRepository) TaskSnapshot(context.Context) ([]domain.Task, int64, error) {
	repository.readCalled = true
	repository.snapshotCalled = true
	if repository.readErr != nil {
		return nil, 0, repository.readErr
	}
	if repository.stateVersionErr != nil {
		return nil, 0, repository.stateVersionErr
	}
	return repository.tasks, repository.stateVersion, nil
}

func queryTask(handle string, state domain.TaskState, stateVersion int64) domain.Task {
	created := time.Date(2026, time.August, 8, 20, 0, 0, 0, time.UTC)
	return domain.Task{
		SchemaVersion:     1,
		Handle:            handle,
		State:             state,
		Shape:             domain.ShapeShip,
		RepositoryID:      "product-api",
		BaseRevision:      strings.Repeat("a", 40),
		BriefRevision:     1,
		ValidationProfile: "go-default",
		DeliveryMode:      domain.DeliveryPullRequest,
		WorkerProfileID:   "codex-standard",
		StateVersion:      stateVersion,
		CreatedAt:         created,
		UpdatedAt:         created.Add(time.Minute),
	}
}

func queryOperation(id string, stateVersion int64) domain.OperationRecord {
	created := time.Date(2026, time.August, 8, 20, 0, 0, 0, time.UTC)
	return domain.OperationRecord{
		SchemaVersion: 1,
		ID:            id,
		Command:       "PrepareTask",
		SubjectDigest: strings.Repeat("b", 64),
		Status:        domain.OperationCompleted,
		StateVersion:  stateVersion,
		CreatedAt:     created,
		UpdatedAt:     created.Add(time.Minute),
	}
}
